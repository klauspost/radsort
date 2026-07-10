package radsort

import (
	"runtime"
	"sync"
)

// maxParallelWorkers caps the goroutines a parallel sort uses. Radix sorting is
// memory-bandwidth bound, so aggregate throughput plateaus well before the core
// count on typical hardware; more workers just contend for memory channels.
const maxParallelWorkers = 8

// parallelMinLen is the input length below which the parallel path falls back to
// a serial sort. The Section 4.3 core's fixed overhead — per-thread sigma-block
// head starts and the goroutine launches per round — dominates for small inputs;
// measured on a Zen 5, the block-chunk parallel sort overtakes serial around
// 128K elements (serial still wins at 64K).
const parallelMinLen = 1 << 17 // 128K elements

func workerCount() int {
	if p := runtime.GOMAXPROCS(0); p < maxParallelWorkers {
		return p
	}
	return maxParallelWorkers
}

// pthread is one worker's private state for a round: the output-block cursors
// and per-bucket block counts for its chunk, plus where its chunk begins.
type pthread[E any] struct {
	iStart   int // first block of this thread's chunk in perm2
	iPartial int // this thread's first partial block in partials
	fill     int // end of this thread's allocated blocks
	buckets  [radix]obucket[E]
	counts   [radix]uint32
}

// ParallelSorter holds the reusable buffers a concurrent sort needs. Unlike the
// serial Sorter its scratch is 2*sigma blocks *per worker* — a fixed few MiB
// independent of n — so the parallel sort keeps Radsort's O(sqrt n) space
// property (the paper's Section 4.3 block-chunk scheme) rather than the O(n)
// buffer a most-significant-byte split would need.
//
// The zero value is ready to use. A ParallelSorter runs its own goroutines, so a
// single one must not be used from multiple goroutines at the same time.
type ParallelSorter[E any] struct {
	pairs       []E
	n           int
	nThread     int
	nScratch    int // 2*radix*nThread
	nPartial    int
	fill        int
	perm        []uint32
	perm2       []uint32
	assignments []uint8
	partials    []partial // nThread*radix entries; first nPartial live, sorted by index
	threads     []pthread[E]
	scratch     []E
	fallback    Sorter[E] // serial sort for inputs below parallelMinLen
}

func (ps *ParallelSorter[E]) nperm() int { return ps.nScratch + ps.n/blockSize }

func (ps *ParallelSorter[E]) blockat(j int) []E {
	if j < ps.nScratch {
		b := j * blockSize
		return ps.scratch[b : b+blockSize : b+blockSize]
	}
	b := (j - ps.nScratch) * blockSize
	return ps.pairs[b : b+blockSize : b+blockSize]
}

func (ps *ParallelSorter[E]) blocksize(i int, ip *int) int {
	if int(ps.partials[*ip].index) == i {
		l := int(ps.partials[*ip].length)
		*ip++
		return l
	}
	return blockSize
}

// prepare (re)initialises ps to sort pairs across nThread workers, reusing
// buffers where large enough. Data blocks overlay pairs at perm[0..fill); the
// tail goes to scratch block 0; all scratch blocks sit at perm[fill..nperm).
func (ps *ParallelSorter[E]) prepare(pairs []E, nThread int) {
	n := len(pairs)
	ps.pairs = pairs
	ps.n = n
	ps.nThread = nThread
	ps.nScratch = nscratch * nThread
	np := ps.nperm()
	ps.perm = grow(ps.perm, np)
	ps.perm2 = grow(ps.perm2, np)
	ps.assignments = grow(ps.assignments, np)
	ps.partials = grow(ps.partials, nThread*radix)
	ps.scratch = grow(ps.scratch, ps.nScratch*blockSize)
	if len(ps.threads) < nThread {
		ps.threads = make([]pthread[E], nThread)
	}

	iPerm := 0
	for ; iPerm*blockSize < n; iPerm++ {
		ps.perm[iPerm] = uint32(ps.nScratch + iPerm)
	}
	ps.fill = iPerm

	if nTail := n % blockSize; nTail != 0 {
		iPerm--
		copy(ps.blockat(0), pairs[iPerm*blockSize:]) // tail -> scratch block 0
		ps.partials[0] = partial{index: uint32(iPerm), length: uint32(nTail)}
	} else {
		ps.partials[0] = partial{index: uint32(iPerm - 1), length: blockSize} // ensure >=1 partial
	}
	ps.nPartial = 1

	for iScratch := range ps.nScratch {
		ps.perm[iPerm] = uint32(iScratch)
		iPerm++
	}
}

// threadQuota returns the number of elements assigned to threads 0..t inclusive,
// dividing n as evenly as possible.
func threadQuota(n, nThread, t int) int {
	quot, rem := n/nThread, n%nThread
	t++
	if rem <= t {
		return t*quot + rem
	}
	return t*quot + t
}

// distributeWork groups the allocated blocks into nThread element-balanced
// chunks in perm2, each preceded by a sigma-block head start drawn from the free
// scratch blocks, and records each thread's span. Partial-block indices are
// rewritten to their new perm2 positions.
func (ps *ParallelSorter[E]) distributeWork() {
	np := ps.nperm()
	iPerm, iPerm2, iPartial, items := 0, 0, 0, 0
	for t := range ps.nThread {
		thr := &ps.threads[t]
		thr.iStart = iPerm2
		thr.iPartial = iPartial
		quota := threadQuota(ps.n, ps.nThread, t)

		copy(ps.perm2[iPerm2:iPerm2+radix], ps.perm[ps.fill+radix*t:ps.fill+radix*t+radix])
		iPerm2 += radix

		last := t == ps.nThread-1
		for (last && iPerm < ps.fill) || (!last && items < quota) {
			if int(ps.partials[iPartial].index) == iPerm {
				ps.partials[iPartial].index = uint32(iPerm2)
				items += int(ps.partials[iPartial].length)
				iPartial++
			} else {
				items += blockSize
			}
			ps.perm2[iPerm2] = ps.perm[iPerm]
			iPerm2++
			iPerm++
		}
		thr.fill = iPerm2
	}
	copy(ps.perm2[iPerm2:np], ps.perm[iPerm2:np]) // remaining free (scratch) blocks
}

// sortStepThread performs thread t's share of one round at the given shift. Only
// this routine reads keys; it and the monomorphised variants run concurrently on
// disjoint chunks, so they touch disjoint perm2/assignments ranges and blocks.
func sortStepThread[E any](ps *ParallelSorter[E], shift uint, keyOf func(E) uint64, t int) {
	thr := &ps.threads[t]
	iOut := thr.iStart
	for ; iOut < thr.iStart+radix; iOut++ {
		bucket := iOut - thr.iStart
		p := int(ps.perm2[iOut])
		thr.buckets[bucket] = obucket[E]{blk: ps.blockat(p), phys: p}
		ps.assignments[iOut] = uint8(bucket)
		thr.counts[bucket] = 1
	}

	ip := thr.iPartial
	for iIn := iOut; iIn < thr.fill; iIn++ {
		in := ps.blockat(int(ps.perm2[iIn]))
		l := ps.blocksize(iIn, &ip)
		for _, e := range in[:l] {
			c := uint8(keyOf(e) >> shift)
			bkt := &thr.buckets[c]
			bkt.blk[bkt.off] = e
			bkt.off++
			if bkt.off == blockSize {
				p := int(ps.perm2[iOut])
				bkt.blk, bkt.off, bkt.phys = ps.blockat(p), 0, p
				ps.assignments[iOut] = c
				thr.counts[c]++
				iOut++
			}
		}
	}
	thr.fill = iOut
}

// sortBlocks (sequential) interleaves the per-thread bucket arrays into one
// global ordering in perm — bucket by bucket, thread by thread — and collects
// the up-to-nThread partial blocks per bucket, restoring the invariant.
func (ps *ParallelSorter[E]) sortBlocks() {
	for i := range ps.nThread * radix {
		ps.partials[i].index = ^uint32(0)
	}

	var starts [radix]uint32
	blockCount := 0
	for i := range radix {
		starts[i] = uint32(blockCount)
		for j := range ps.nThread {
			blockCount += int(ps.threads[j].counts[i])
		}
	}

	freeStart := blockCount
	for t := range ps.nThread {
		thr := &ps.threads[t]
		for j := thr.iStart; j < thr.fill; j++ {
			bucket := ps.assignments[j]
			k := starts[bucket]
			starts[bucket]++
			ps.perm[k] = ps.perm2[j]
			if int(ps.perm2[j]) == thr.buckets[bucket].phys { // bucket's trailing (partial) block
				ps.partials[int(bucket)*ps.nThread+t] = partial{index: k, length: uint32(thr.buckets[bucket].off)}
			}
		}

		chunkEnd := ps.nperm()
		if t < ps.nThread-1 {
			chunkEnd = ps.threads[t+1].iStart
		}
		freeCount := chunkEnd - thr.fill
		copy(ps.perm[freeStart:freeStart+freeCount], ps.perm2[thr.fill:thr.fill+freeCount])
		freeStart += freeCount
	}

	ps.nPartial = 0
	for i := range ps.nThread * radix {
		if ps.partials[i].index != ^uint32(0) {
			ps.partials[ps.nPartial] = ps.partials[i]
			ps.nPartial++
		}
	}
	ps.fill = blockCount
}

// compact moves the logically ordered blocks back into pairs, closing partial
// gaps. Ported from the reference; the runahead offset is n_scratch (not sigma).
func (ps *ParallelSorter[E]) compact() {
	perm, pinv := ps.perm, ps.perm2
	np := ps.nperm()
	for i := range np {
		pinv[perm[i]] = uint32(i)
	}

	start, ip := 0, 0
	iFree := ps.fill
	jFree := int(perm[iFree])

	jOut := ps.nScratch
	for ; jOut < np; jOut++ {
		iIn := jOut - ps.nScratch
		iOut := int(pinv[jOut])
		jIn := int(perm[iIn])

		if iOut > iIn && iOut < ps.fill {
			copy(ps.blockat(jFree), ps.blockat(jOut))
			perm[iFree], perm[iOut] = uint32(jOut), uint32(jFree)
			pinv[jFree], pinv[jOut] = uint32(iOut), uint32(iFree)
			iFree = iOut
			iOut = int(pinv[jOut])
		}

		l := ps.blocksize(iIn, &ip)
		copy(ps.pairs[start:start+l], ps.blockat(jIn)[:l])
		perm[iIn], perm[iOut] = uint32(jOut), uint32(jIn)
		pinv[jIn], pinv[jOut] = uint32(iOut), uint32(iIn)
		start += l
		if iIn != iOut {
			jFree, iFree = jIn, iOut
		}
	}

	for iIn := jOut - ps.nScratch; iIn < ps.fill; iIn++ {
		jIn := int(perm[iIn])
		l := ps.blocksize(iIn, &ip)
		copy(ps.pairs[start:start+l], ps.blockat(jIn)[:l])
		start += l
	}
}

// runThreads runs work(0..nThread) concurrently and waits.
func (ps *ParallelSorter[E]) runThreads(work func(t int)) {
	var wg sync.WaitGroup
	for t := range ps.nThread {
		wg.Go(func() { work(t) })
	}
	wg.Wait()
}

// Sort sorts data in ascending natural order using multiple goroutines and ps's
// reusable buffers. Supported element types are uint32 and uint64; for anything
// else use SortKey with a key function.
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
// using rounds byte-passes (least significant first) and up to min(GOMAXPROCS, 8)
// goroutines, reusing ps's buffers. It preserves Radsort's O(sqrt n) working set
// (Section 4.3). Inputs below a size threshold, or on a single CPU, fall back to
// a serial sort. For the built-in integer types the dedicated ParallelUint32s /
// ParallelUint64s are faster (their inner loop avoids the per-element key call).
func (ps *ParallelSorter[E]) SortKey(data []E, rounds int, keyOf func(E) uint64) {
	n, w := len(data), workerCount()
	if n < parallelMinLen || w < 2 || rounds < 1 {
		ps.fallback.SortKey(data, rounds, keyOf)
		return
	}
	ps.prepare(data, w)
	for r := range rounds {
		ps.distributeWork()
		shift := uint(r * rshift)
		ps.runThreads(func(t int) { sortStepThread(ps, shift, keyOf, t) })
		ps.sortBlocks()
	}
	ps.compact()
}

// ParallelUint32s sorts x in ascending order using multiple goroutines. It
// allocates its working buffers per call.
func ParallelUint32s(x []uint32) { parallelU32(new(ParallelSorter[uint32]), x) }

// ParallelUint64s sorts x in ascending order using multiple goroutines. It
// allocates its working buffers per call.
func ParallelUint64s(x []uint64) { parallelU64(new(ParallelSorter[uint64]), x) }

func parallelU32(ps *ParallelSorter[uint32], x []uint32) {
	const rounds = 4
	n, w := len(x), workerCount()
	if n < parallelMinLen || w < 2 {
		sortU32(&ps.fallback, x)
		return
	}
	ps.prepare(x, w)
	for r := range rounds {
		ps.distributeWork()
		shift := uint(r) * rshift
		ps.runThreads(func(t int) { sortStepThreadU32(ps, shift, t) })
		ps.sortBlocks()
	}
	ps.compact()
}

func parallelU64(ps *ParallelSorter[uint64], x []uint64) {
	const rounds = 8
	n, w := len(x), workerCount()
	if n < parallelMinLen || w < 2 {
		sortU64(&ps.fallback, x)
		return
	}
	ps.prepare(x, w)
	for r := range rounds {
		ps.distributeWork()
		shift := uint(r) * rshift
		ps.runThreads(func(t int) { sortStepThreadU64(ps, shift, t) })
		ps.sortBlocks()
	}
	ps.compact()
}

func sortStepThreadU32(ps *ParallelSorter[uint32], shift uint, t int) {
	thr := &ps.threads[t]
	iOut := thr.iStart
	for ; iOut < thr.iStart+radix; iOut++ {
		bucket := iOut - thr.iStart
		p := int(ps.perm2[iOut])
		thr.buckets[bucket] = obucket[uint32]{blk: ps.blockat(p), phys: p}
		ps.assignments[iOut] = uint8(bucket)
		thr.counts[bucket] = 1
	}

	ip := thr.iPartial
	for iIn := iOut; iIn < thr.fill; iIn++ {
		in := ps.blockat(int(ps.perm2[iIn]))
		l := ps.blocksize(iIn, &ip)
		for _, e := range in[:l] {
			c := uint8(e >> shift)
			bkt := &thr.buckets[c]
			bkt.blk[bkt.off] = e
			bkt.off++
			if bkt.off == blockSize {
				p := int(ps.perm2[iOut])
				bkt.blk, bkt.off, bkt.phys = ps.blockat(p), 0, p
				ps.assignments[iOut] = c
				thr.counts[c]++
				iOut++
			}
		}
	}
	thr.fill = iOut
}

func sortStepThreadU64(ps *ParallelSorter[uint64], shift uint, t int) {
	thr := &ps.threads[t]
	iOut := thr.iStart
	for ; iOut < thr.iStart+radix; iOut++ {
		bucket := iOut - thr.iStart
		p := int(ps.perm2[iOut])
		thr.buckets[bucket] = obucket[uint64]{blk: ps.blockat(p), phys: p}
		ps.assignments[iOut] = uint8(bucket)
		thr.counts[bucket] = 1
	}

	ip := thr.iPartial
	for iIn := iOut; iIn < thr.fill; iIn++ {
		in := ps.blockat(int(ps.perm2[iIn]))
		l := ps.blocksize(iIn, &ip)
		for _, e := range in[:l] {
			c := uint8(e >> shift)
			bkt := &thr.buckets[c]
			bkt.blk[bkt.off] = e
			bkt.off++
			if bkt.off == blockSize {
				p := int(ps.perm2[iOut])
				bkt.blk, bkt.off, bkt.phys = ps.blockat(p), 0, p
				ps.assignments[iOut] = c
				thr.counts[c]++
				iOut++
			}
		}
	}
	thr.fill = iOut
}
