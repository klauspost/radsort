package radsort

import (
	"encoding/binary"
	"slices"
	"testing"
)

// FuzzUints feeds an arbitrary byte stream, read as uint64s, to Uint64s (the
// 8-round path), then truncates each value to uint32 and feeds that to Uint32s
// (the 4-round path). One corpus thus exercises both monomorphised inner loops
// and every block/partial boundary the structured tests might miss; each is
// cross-checked against slices.Sort. ([]byte is the only slice type Go's
// fuzzer accepts as an argument, so the values are decoded from it.)
func FuzzUints(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	f.Add([]byte{255, 255, 255, 255, 255, 255, 255, 255, 1, 0, 0, 0, 0, 0, 0, 0})
	f.Add(make([]byte, 8*(blockSize+3)))
	f.Fuzz(func(t *testing.T, b []byte) {
		x64 := make([]uint64, len(b)/8)
		x32 := make([]uint32, len(x64))
		for i := range x64 {
			x64[i] = binary.LittleEndian.Uint64(b[i*8:])
			x32[i] = uint32(x64[i])
		}

		want64 := slices.Clone(x64)
		slices.Sort(want64)
		Uint64s(x64)
		if !slices.Equal(x64, want64) {
			t.Fatalf("uint64 n=%d: not sorted", len(x64))
		}

		want32 := slices.Clone(x32)
		slices.Sort(want32)
		Uint32s(x32)
		if !slices.Equal(x32, want32) {
			t.Fatalf("uint32 n=%d: not sorted", len(x32))
		}
	})
}

// FuzzStable checks the stability guarantee on the generic SortKey path. The
// mask parameter folds keys into a smaller range, forcing heavy collisions that
// stress partial-block handling and the deinterleave step.
func FuzzStable(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 2}, uint32(0))
	f.Add(make([]byte, 4*(2*blockSize+5)), uint32(7))
	f.Fuzz(func(t *testing.T, b []byte, mask uint32) {
		type pair struct {
			key uint32
			seq int
		}
		x := make([]pair, len(b)/4)
		for i := range x {
			k := binary.LittleEndian.Uint32(b[i*4:])
			if mask != 0 {
				k %= mask
			}
			x[i] = pair{key: k, seq: i}
		}
		SortKey(x, 4, func(p pair) uint64 { return uint64(p.key) })
		for i := 1; i < len(x); i++ {
			if x[i-1].key > x[i].key {
				t.Fatalf("n=%d: keys out of order at %d", len(x), i)
			}
			if x[i-1].key == x[i].key && x[i-1].seq > x[i].seq {
				t.Fatalf("n=%d: unstable at %d", len(x), i)
			}
		}
	})
}
