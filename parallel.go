package radsort

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// maxParallelWorkers caps the goroutines a parallel sort uses. Radix sorting is
// memory-bandwidth bound, so aggregate throughput plateaus well before the core
// count on typical hardware; more workers just contend for memory channels.
const maxParallelWorkers = 8

// parallelMinLen is the input length below which the parallel path falls back
// to a serial sort. Below it the MSD split's per-bucket setup and the O(n)
// buffer allocation outweigh the speedup. The measured crossover is ~1M
// elements; since the caller explicitly asked for a parallel sort, we sit right
// at it rather than adding a safety margin. It is in elements, as bucket
// occupancy — not byte size — governs the crossover.
const parallelMinLen = 1 << 20 // ~1.05M elements

func workerCount() int {
	if p := runtime.GOMAXPROCS(0); p < maxParallelWorkers {
		return p
	}
	return maxParallelWorkers
}

// ParallelSorter holds the reusable buffers a concurrent sort needs: an
// O(len(data)) buffer that the most-significant-byte split scatters into, plus
// one scratch Sorter per worker. Reusing a ParallelSorter across calls avoids
// re-allocating these, which for large inputs is the dominant cost — a fresh
// 64M-element sort otherwise allocates a ~256 MiB buffer every call.
//
// The zero value is ready to use. A ParallelSorter runs its own goroutines, so
// a single one must not be used from multiple goroutines at the same time.
type ParallelSorter[E any] struct {
	out     []E
	workers []Sorter[E]
}

// Sort sorts data in ascending natural order using multiple goroutines and ps's
// reusable buffers — the fast, low-allocation way to sort the same slice type
// repeatedly. Supported element types are uint32 and uint64; for anything else
// use SortKey with a key function.
func (ps *ParallelSorter[E]) Sort(data []E) {
	switch p := any(ps).(type) {
	case *ParallelSorter[uint32]:
		parallelU32(p, any(data).([]uint32))
	case *ParallelSorter[uint64]:
		parallelU64(p, any(data).([]uint64))
	default:
		panic("radsort: ParallelSorter[E].Sort supports only uint32 and uint64; use SortKey for other types")
	}
}

// SortKey sorts data stably in ascending order of the unsigned key from keyOf,
// using rounds byte-passes (least significant first) and up to
// min(GOMAXPROCS, 8) goroutines, reusing ps's buffers.
//
// It splits on the most significant byte (byte rounds-1) into radix independent
// buckets and sorts them concurrently by the remaining low bytes, needing an
// O(len(data)) scratch buffer. Inputs below a size threshold, or on a single
// CPU, fall back to a serial sort. For the built-in integer types the dedicated
// ParallelUint32s/ParallelUint64s are faster (their split avoids the per-element
// key call); SortKey is the general and reusable entry point.
func (ps *ParallelSorter[E]) SortKey(data []E, rounds int, keyOf func(E) uint64) {
	n := len(data)
	w := workerCount()
	if n < parallelMinLen || w < 2 || rounds < 1 {
		ps.reserve(1)
		subsort(&ps.workers[0], data, rounds, keyOf) // full serial sort
		return
	}
	ps.reserve(w)

	msd := uint((rounds - 1) * rshift)
	var cnt [radix]int
	for _, v := range data {
		cnt[byte(keyOf(v)>>msd)]++
	}
	start := prefix(&cnt)

	ps.out = grow(ps.out, n)
	out := ps.out
	pos := start
	for _, v := range data {
		c := byte(keyOf(v) >> msd)
		out[pos[c]] = v
		pos[c]++
	}

	ps.finish(data, out, start, cnt, rounds-1, keyOf, w)
}

// finish sorts each already-split bucket concurrently by its low subRounds
// bytes, then copies the result back into data. Buckets are disjoint, so a
// worker only needs to claim the next bucket index.
func (ps *ParallelSorter[E]) finish(data, out []E, start, cnt [radix]int, subRounds int, keyOf func(E) uint64, w int) {
	var next atomic.Int32
	var wg sync.WaitGroup
	for i := range w {
		s := &ps.workers[i]
		wg.Go(func() {
			for {
				c := int(next.Add(1)) - 1
				if c >= radix {
					return
				}
				subsort(s, out[start[c]:start[c]+cnt[c]], subRounds, keyOf)
			}
		})
	}
	wg.Wait()
	copy(data, out)
}

// reserve makes sure ps has at least n worker scratches.
func (ps *ParallelSorter[E]) reserve(n int) {
	if len(ps.workers) < n {
		ps.workers = make([]Sorter[E], n)
	}
}

// prefix turns a histogram into bucket start offsets.
func prefix(cnt *[radix]int) [radix]int {
	var start [radix]int
	sum := 0
	for c := range cnt {
		start[c] = sum
		sum += cnt[c]
	}
	return start
}

// subsort sorts x by its low `rounds` bytes, using the monomorphised path for
// the built-in integer types and the generic path otherwise.
func subsort[E any](s *Sorter[E], x []E, rounds int, keyOf func(E) uint64) {
	switch st := any(s).(type) {
	case *Sorter[uint32]:
		sortU32Rounds(st, any(x).([]uint32), rounds)
	case *Sorter[uint64]:
		sortU64Rounds(st, any(x).([]uint64), rounds)
	default:
		s.SortKey(x, rounds, keyOf)
	}
}

func sortU32Rounds(s *Sorter[uint32], x []uint32, rounds int) {
	if len(x) < 2 {
		return
	}
	s.prepare(x)
	for r := range rounds {
		sortStepU32(s, uint(r)*rshift)
	}
	compact(s)
}

func sortU64Rounds(s *Sorter[uint64], x []uint64, rounds int) {
	if len(x) < 2 {
		return
	}
	s.prepare(x)
	for r := range rounds {
		sortStepU64(s, uint(r)*rshift)
	}
	compact(s)
}

// ParallelUint32s sorts x in ascending order using multiple goroutines. It
// allocates its working buffers per call.
func ParallelUint32s(x []uint32) {
	var ps ParallelSorter[uint32]
	parallelU32(&ps, x)
}

// ParallelUint64s sorts x in ascending order using multiple goroutines. It
// allocates its working buffers per call.
func ParallelUint64s(x []uint64) {
	var ps ParallelSorter[uint64]
	parallelU64(&ps, x)
}

// parallelU32/parallelU64 are the monomorphised parallel sorts: the split
// extracts the top byte inline rather than through a key call. They take a
// ParallelSorter so the buffers can be reused.
func parallelU32(ps *ParallelSorter[uint32], x []uint32) {
	const rounds, msd = 4, (4 - 1) * rshift
	n := len(x)
	w := workerCount()
	if n < parallelMinLen || w < 2 {
		ps.reserve(1)
		sortU32Rounds(&ps.workers[0], x, rounds)
		return
	}
	ps.reserve(w)

	var cnt [radix]int
	for _, v := range x {
		cnt[byte(v>>msd)]++
	}
	start := prefix(&cnt)

	ps.out = grow(ps.out, n)
	out := ps.out
	pos := start
	for _, v := range x {
		c := byte(v >> msd)
		out[pos[c]] = v
		pos[c]++
	}

	ps.finish(x, out, start, cnt, rounds-1, nil, w)
}

func parallelU64(ps *ParallelSorter[uint64], x []uint64) {
	const rounds, msd = 8, (8 - 1) * rshift
	n := len(x)
	w := workerCount()
	if n < parallelMinLen || w < 2 {
		ps.reserve(1)
		sortU64Rounds(&ps.workers[0], x, rounds)
		return
	}
	ps.reserve(w)

	var cnt [radix]int
	for _, v := range x {
		cnt[byte(v>>msd)]++
	}
	start := prefix(&cnt)

	ps.out = grow(ps.out, n)
	out := ps.out
	pos := start
	for _, v := range x {
		c := byte(v >> msd)
		out[pos[c]] = v
		pos[c]++
	}

	ps.finish(x, out, start, cnt, rounds-1, nil, w)
}
