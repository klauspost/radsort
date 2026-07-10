package radsort

import (
	"slices"
	"testing"
)

func TestUint32Seq(t *testing.T) {
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
		got := slices.Collect(Uint32Seq(x))
		if !slices.Equal(got, want) {
			t.Fatalf("n=%d mod=%d: Uint32Seq mismatch", n, mod)
		}
	}
	for _, n := range testSizes {
		check(n, 0)
	}
	for n := 0; n <= 4*blockSize+radix; n++ { // exhaustive small sizes, many partial blocks
		check(n, 300)
	}
}

// TestUint64Seq mirrors TestUint32Seq for the seqU64 mono path.
func TestUint64Seq(t *testing.T) {
	r := newRNG()
	check := func(n int, mod uint64) {
		x := make([]uint64, n)
		for i := range x {
			if mod != 0 {
				x[i] = r.Uint64() % mod
			} else {
				x[i] = r.Uint64()
			}
		}
		want := slices.Clone(x)
		slices.Sort(want)
		got := slices.Collect(Uint64Seq(x))
		if !slices.Equal(got, want) {
			t.Fatalf("n=%d mod=%d: Uint64Seq mismatch", n, mod)
		}
	}
	for _, n := range testSizes {
		check(n, 0)
	}
	for n := 0; n <= 4*blockSize+radix; n++ { // exhaustive small sizes, many partial blocks
		check(n, 300)
	}
}
