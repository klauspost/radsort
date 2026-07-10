package radsort

import "math"

// Order-preserving mappings to unsigned keys. Signed integers only need their
// sign bit flipped; IEEE-754 floats need the sign bit flipped for positives and
// all bits flipped for negatives so that the unsigned bit pattern sorts in
// ascending numeric order (NaNs sort to the end).

// Uint32s sorts a slice of uint32 in ascending order. It allocates working
// buffers per call; see Sorter to reuse buffers across calls.
func Uint32s(x []uint32) { sortU32(new(Sorter[uint32]), x) }

// Uint64s sorts a slice of uint64 in ascending order. It allocates working
// buffers per call; see Sorter to reuse buffers across calls.
func Uint64s(x []uint64) { sortU64(new(Sorter[uint64]), x) }

// sortU32/sortU64 run the monomorphised path on the given sorter, so the same
// code serves both the allocating wrappers above and a recycled sorter.
func sortU32(s *Sorter[uint32], x []uint32) {
	if len(x) < 2 {
		return
	}
	s.prepare(x)
	sortRoundsU32(s)
	compact(s)
}

func sortU64(s *Sorter[uint64], x []uint64) {
	if len(x) < 2 {
		return
	}
	s.prepare(x)
	sortRoundsU64(s)
	compact(s)
}

// Int32s sorts a slice of int32 in ascending order.
func Int32s(x []int32) { SortKey(x, 4, int32Key) }

// Int64s sorts a slice of int64 in ascending order.
func Int64s(x []int64) { SortKey(x, 8, int64Key) }

// Float64s sorts a slice of float64 in ascending order (NaNs last).
func Float64s(x []float64) { SortKey(x, 8, float64Key) }

// Float32s sorts a slice of float32 in ascending order (NaNs last).
func Float32s(x []float32) { SortKey(x, 4, float32Key) }

// Key mappings, shared with the Section 4.1 iterators (seq.go) used by Map.
func int32Key(v int32) uint64 { return uint64(uint32(v) ^ 1<<31) }
func int64Key(v int64) uint64 { return uint64(v) ^ 1<<63 }

func float32Key(v float32) uint64 {
	b := math.Float32bits(v)
	return uint64(b ^ (uint32(int32(b)>>31) | 1<<31))
}

func float64Key(v float64) uint64 {
	b := math.Float64bits(v)
	return b ^ (uint64(int64(b)>>63) | 1<<63)
}
