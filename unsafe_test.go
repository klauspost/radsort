//go:build !nounsafe && !appengine

package radsort

import (
	"fmt"
	"slices"
	"testing"
)

// sortU32ForceUnsafe / sortU64ForceUnsafe run the unsafe sort phase regardless
// of size, so the tests exercise it at every block boundary (the size dispatch
// in sortRoundsU32/U64 would otherwise use the safe phase for small inputs).

func sortU32ForceUnsafe(x []uint32) {
	if len(x) < 2 {
		return
	}
	var s Sorter[uint32]
	s.prepare(x)
	for r := range 4 {
		sortStepU32Unsafe(&s, uint(r)*rshift)
	}
	compact(&s)
}

func sortU64ForceUnsafe(x []uint64) {
	if len(x) < 2 {
		return
	}
	var s Sorter[uint64]
	s.prepare(x)
	for r := range 8 {
		sortStepU64Unsafe(&s, uint(r)*rshift)
	}
	compact(&s)
}

func TestUnsafeU32(t *testing.T) {
	r := newRNG()
	check := func(n int, mod uint32) {
		x := make([]uint32, n)
		for i := range x {
			if mod != 0 {
				x[i] = r.Uint32() % mod
			} else {
				x[i] = r.Uint32()
			}
		}
		want := slices.Clone(x)
		slices.Sort(want)
		sortU32ForceUnsafe(x)
		if !slices.Equal(x, want) {
			t.Fatalf("n=%d mod=%d: not sorted", n, mod)
		}
	}
	for _, n := range testSizes {
		check(n, 0)
	}
	for n := 0; n <= 4*blockSize+radix; n++ { // exhaustive small sizes, many partial blocks
		check(n, 300)
	}
}

func TestUnsafeU64(t *testing.T) {
	r := newRNG()
	for _, n := range testSizes {
		x := make([]uint64, n)
		for i := range x {
			x[i] = r.Uint64()
		}
		want := slices.Clone(x)
		slices.Sort(want)
		sortU64ForceUnsafe(x)
		if !slices.Equal(x, want) {
			t.Fatalf("n=%d: not sorted", n)
		}
	}
}

// BenchmarkUnsafeStep compares the safe and unsafe sort phases directly across
// the crossover, for tuning unsafeMinLen. Both run on a recycled Sorter (0
// allocs), so only the inner loop differs.
func BenchmarkUnsafeStep(b *testing.B) {
	r := newRNG()
	run := func(step func(*Sorter[uint32], uint)) func([]uint32) {
		var s Sorter[uint32]
		return func(x []uint32) {
			s.prepare(x)
			for rr := range 4 {
				step(&s, uint(rr)*rshift)
			}
			compact(&s)
		}
	}
	for _, n := range []int{100_000, 300_000, 1_000_000} {
		data := genUint32(n, "uniform", r)
		b.Run(fmt.Sprintf("safe/%d", n), func(b *testing.B) { benchSort(b, data, run(sortStepU32)) })
		b.Run(fmt.Sprintf("unsafe/%d", n), func(b *testing.B) { benchSort(b, data, run(sortStepU32Unsafe)) })
	}
}
