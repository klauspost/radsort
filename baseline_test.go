package radsort

import (
	"fmt"
	"slices"
	"testing"
)

// lsdUint32 is a conventional out-of-place LSD radix sort: the baseline the
// paper compares against ("generic"). It allocates a full second array of n
// elements (O(n) overhead) and scatters each round's output across 256 buckets
// that are not resident in cache, incurring the read-for-ownership traffic the
// paper's block-reuse scheme avoids. No output prefetching.
func lsdUint32(x []uint32) {
	n := len(x)
	buf := make([]uint32, n)
	src, dst := x, buf
	for r := 0; r < 4; r++ {
		shift := uint(r * 8)
		var count [radix + 1]int
		for _, v := range src {
			count[(v>>shift)&(radix-1)+1]++
		}
		for i := 1; i < radix; i++ {
			count[i] += count[i-1]
		}
		for _, v := range src {
			c := (v >> shift) & (radix - 1)
			dst[count[c]] = v
			count[c]++
		}
		src, dst = dst, src
	}
	// 4 rounds is even, so src == x holds the sorted result.
}

func TestLSDBaseline(t *testing.T) {
	r := newRNG()
	for _, n := range []int{0, 1, 257, blockSize + 1, 100_000} {
		x := make([]uint32, n)
		for i := range x {
			x[i] = r.Uint32()
		}
		want := slices.Clone(x)
		slices.Sort(want)
		lsdUint32(x)
		if !slices.Equal(x, want) {
			t.Fatalf("n=%d: baseline not sorted", n)
		}
	}
}

// BenchmarkVsPlainLSD pits Radsort against the conventional out-of-place LSD
// radix sort. Both do 4 byte-passes; the only difference is memory layout and
// cache behavior. The paper predicts a crossover around 2 MiB (~512K uint32).
func BenchmarkVsPlainLSD(b *testing.B) {
	r := newRNG()
	for _, n := range benchSizes {
		data := genUint32(n, "uniform", r)
		b.Run(fmt.Sprintf("radsort/%d", n), func(b *testing.B) { benchSort(b, data, Uint32s) })
		b.Run(fmt.Sprintf("plainLSD/%d", n), func(b *testing.B) { benchSort(b, data, lsdUint32) })
	}
}
