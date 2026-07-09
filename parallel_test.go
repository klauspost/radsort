package radsort

import (
	"cmp"
	"fmt"
	"slices"
	"sync"
	"testing"
)

// sizes spanning the serial fallback (< parallelMinLen) and the parallel path.
var parallelSizes = []int{0, 1, 1000, 100_000, parallelMinLen - 1, parallelMinLen, 3_000_000}

func TestParallelUint32s(t *testing.T) {
	r := newRNG()
	for _, n := range parallelSizes {
		x := make([]uint32, n)
		for i := range x {
			x[i] = r.Uint32()
		}
		want := slices.Clone(x)
		slices.Sort(want)
		ParallelUint32s(x)
		if !slices.Equal(x, want) {
			t.Fatalf("uint32 n=%d: not sorted", n)
		}
	}
}

func TestParallelUint64s(t *testing.T) {
	r := newRNG()
	for _, n := range parallelSizes {
		x := make([]uint64, n)
		for i := range x {
			x[i] = r.Uint64()
		}
		want := slices.Clone(x)
		slices.Sort(want)
		ParallelUint64s(x)
		if !slices.Equal(x, want) {
			t.Fatalf("uint64 n=%d: not sorted", n)
		}
	}
}

// TestParallelReuse reuses one ParallelSorter across differing sizes (growing
// then shrinking) to catch stale-buffer bugs, and exercises the generic
// (non-built-in) element path plus its stability guarantee.
func TestParallelReuse(t *testing.T) {
	r := newRNG()
	var ps ParallelSorter[uint32]
	for _, n := range []int{3_000_000, 1000, 3_000_000, parallelMinLen} {
		x := make([]uint32, n)
		for i := range x {
			x[i] = r.Uint32()
		}
		want := slices.Clone(x)
		slices.Sort(want)
		ps.Sort(x) // fast, reusable path
		if !slices.Equal(x, want) {
			t.Fatalf("reuse n=%d: not sorted", n)
		}
	}

	// Generic element type through the parallel path, with heavy key collisions
	// to check stability across the split and concurrent sub-sorts.
	type pair struct {
		key uint32
		seq int
	}
	const n = 3_000_000
	px := make([]pair, n)
	for i := range px {
		px[i] = pair{key: r.Uint32() % 1000, seq: i}
	}
	var pps ParallelSorter[pair]
	pps.SortKey(px, 4, func(p pair) uint64 { return uint64(p.key) })
	for i := 1; i < len(px); i++ {
		if px[i-1].key > px[i].key {
			t.Fatalf("generic parallel: keys out of order at %d", i)
		}
		if px[i-1].key == px[i].key && px[i-1].seq > px[i].seq {
			t.Fatalf("generic parallel: unstable at %d", i)
		}
	}
}

// TestParallelMatchesSerial cross-checks the parallel and serial results on the
// same data, including skewed distributions that collapse the MSD split.
func TestParallelMatchesSerial(t *testing.T) {
	r := newRNG()
	const n = 3_000_000
	dists := []string{"uniform", "smallRange", "fewUnique", "sorted", "reverse", "nearlySorted"}
	for _, d := range dists {
		x := genUint32(n, d, r)
		want := slices.Clone(x)
		Uint32s(want)
		ParallelUint32s(x)
		if !slices.Equal(x, want) {
			t.Fatalf("uint32 dist=%s: parallel != serial", d)
		}
	}
	for _, d := range dists {
		x := genUint64Dist(n, d, r)
		want := slices.Clone(x)
		Uint64s(want)
		ParallelUint64s(x)
		if !slices.Equal(x, want) {
			t.Fatalf("uint64 dist=%s: parallel != serial", d)
		}
	}
}

// TestParallelSortKeyCustom guards the fix that ParallelSorter.SortKey honours a
// custom key even for the built-in element types (the monomorphised sub-sort
// must not silently sort by raw value). The key inverts the order, so a raw-value
// sub-sort would produce the wrong result.
func TestParallelSortKeyCustom(t *testing.T) {
	r := newRNG()
	for _, n := range []int{1000, parallelMinLen + 100} { // serial-fallback and parallel paths
		x := make([]uint32, n)
		for i := range x {
			x[i] = r.Uint32()
		}
		want := slices.Clone(x)
		slices.SortStableFunc(want, func(a, b uint32) int { return cmp.Compare(b, a) }) // descending
		var ps ParallelSorter[uint32]
		ps.SortKey(x, 4, func(v uint32) uint64 { return uint64(^v) }) // ascending ^v == descending v
		if !slices.Equal(x, want) {
			t.Fatalf("uint32 custom key n=%d: not honoured", n)
		}

		y := make([]uint64, n)
		for i := range y {
			y[i] = r.Uint64()
		}
		wy := slices.Clone(y)
		slices.SortStableFunc(wy, func(a, b uint64) int { return cmp.Compare(b, a) })
		var ps64 ParallelSorter[uint64]
		ps64.SortKey(y, 8, func(v uint64) uint64 { return ^v })
		if !slices.Equal(y, wy) {
			t.Fatalf("uint64 custom key n=%d: not honoured", n)
		}
	}
}

// BenchmarkParallel compares the serial and parallel paths at sizes above the
// fallback threshold; the recycled variant shows the allocation saving.
func BenchmarkParallel(b *testing.B) {
	r := newRNG()
	for _, n := range []int{10_000_000, 30_000_000} {
		data := genUint32(n, "uniform", r)
		b.Run(fmt.Sprintf("serial/%d", n), func(b *testing.B) {
			benchSort(b, data, Uint32s)
		})
		b.Run(fmt.Sprintf("parallel-fresh/%d", n), func(b *testing.B) {
			benchSort(b, data, ParallelUint32s)
		})
		b.Run(fmt.Sprintf("parallel-recycled/%d", n), func(b *testing.B) {
			ps := new(ParallelSorter[uint32])
			benchSort(b, data, ps.Sort)
		})
	}
}

// BenchmarkConcurrentSerialSorts measures how aggregate throughput scales when
// P independent *serial* Uint32s calls run concurrently. It is a diagnostic for
// the memory-bandwidth ceiling that limits any single parallel sort, not a
// benchmark of the parallel sort itself (see BenchmarkParallel for that).
func BenchmarkConcurrentSerialSorts(b *testing.B) {
	r := newRNG()
	for _, n := range []int{1_000_000, 16_000_000} {
		for _, p := range []int{1, 2, 4, 8, 16} {
			datas := make([][]uint32, p)
			works := make([][]uint32, p)
			for i := range datas {
				datas[i] = genUint32(n, "uniform", r)
				works[i] = make([]uint32, n)
			}
			b.Run(fmt.Sprintf("n=%d/p=%d", n, p), func(b *testing.B) {
				b.SetBytes(int64(n) * 4 * int64(p))
				b.ResetTimer()
				for range b.N {
					b.StopTimer()
					for j := range works {
						copy(works[j], datas[j])
					}
					b.StartTimer()
					var wg sync.WaitGroup
					for _, x := range works {
						wg.Go(func() { Uint32s(x) })
					}
					wg.Wait()
				}
			})
		}
	}
}
