package radsort

import "iter"

// iterate yields the sorted elements straight from the block permutation,
// skipping the O(n) compaction that compact performs (the paper's Section 4.1,
// "avoiding finalisation"). It runs in place of compact, after the sort rounds,
// reading the same [radix, fill) range of logical blocks that compact copies
// from — so it produces the identical order without moving any data. The input
// slice, used as scratch, is left in an unspecified order.
func (s *Sorter[E]) iterate(yield func(E) bool) {
	ip := 0
	for i := radix; i < s.fill; i++ {
		blk := s.blockat(int(s.perm[i]))
		l := s.blocksize(i, &ip)
		for _, e := range blk[:l] {
			if !yield(e) {
				return
			}
		}
	}
}

// Uint32Seq returns an iterator over the values of v in ascending order.
//
// It sorts using v as scratch and yields directly from radsort's internal block
// permutation, skipping the final compaction pass (Section 4.1 of the paper).
// The result is never written back, so v is left in an unspecified order once
// iteration begins; if you need v itself sorted, use Uint32s. The sort runs when
// iteration starts.
func Uint32Seq(v []uint32) iter.Seq[uint32] {
	return func(yield func(uint32) bool) { seqU32(v, yield) }
}

// Uint64Seq is Uint32Seq for []uint64.
func Uint64Seq(v []uint64) iter.Seq[uint64] {
	return func(yield func(uint64) bool) { seqU64(v, yield) }
}

// Int32Seq is Uint32Seq for []int32.
func Int32Seq(v []int32) iter.Seq[int32] {
	return func(yield func(int32) bool) { seqKey(v, 4, int32Key, yield) }
}

// Int64Seq is Uint32Seq for []int64.
func Int64Seq(v []int64) iter.Seq[int64] {
	return func(yield func(int64) bool) { seqKey(v, 8, int64Key, yield) }
}

// Float32Seq is Uint32Seq for []float32 (NaNs sort last).
func Float32Seq(v []float32) iter.Seq[float32] {
	return func(yield func(float32) bool) { seqKey(v, 4, float32Key, yield) }
}

// Float64Seq is Uint32Seq for []float64 (NaNs sort last).
func Float64Seq(v []float64) iter.Seq[float64] {
	return func(yield func(float64) bool) { seqKey(v, 8, float64Key, yield) }
}

// SortKeySeq returns an iterator over data's elements in ascending order of the
// unsigned key returned by keyOf, considering the low rounds key bytes
// (least-significant first). It is the generic counterpart to Uint32Seq, as
// SortKey is to Uint32s.
//
// Like Uint32Seq, it sorts using data as scratch and yields directly from the
// block permutation, skipping the final compaction pass (Section 4.1 of the
// paper); data is left in an unspecified order once iteration starts, so use
// SortKey if you need data itself sorted. Being generic, it pays one
// (non-inlined) keyOf call per element and allocates working buffers per call.
func SortKeySeq[E any](data []E, rounds int, keyOf func(E) uint64) iter.Seq[E] {
	return func(yield func(E) bool) { seqKey(data, rounds, keyOf, yield) }
}

func seqU32(v []uint32, yield func(uint32) bool) {
	if len(v) < 2 {
		for _, e := range v {
			if !yield(e) {
				return
			}
		}
		return
	}
	var s Sorter[uint32]
	s.prepare(v)
	for r := range 4 {
		sortStepU32(&s, uint(r)*rshift)
	}
	s.iterate(yield)
}

func seqU64(v []uint64, yield func(uint64) bool) {
	if len(v) < 2 {
		for _, e := range v {
			if !yield(e) {
				return
			}
		}
		return
	}
	var s Sorter[uint64]
	s.prepare(v)
	for r := range 8 {
		sortStepU64(&s, uint(r)*rshift)
	}
	s.iterate(yield)
}

// seqKey is the generic Section 4.1 iterator for element types sorted through a
// key function (the signed and float types). It mirrors SortKey but ends in
// iterate rather than compact.
func seqKey[E any](v []E, rounds int, keyOf func(E) uint64, yield func(E) bool) {
	if len(v) < 2 {
		for _, e := range v {
			if !yield(e) {
				return
			}
		}
		return
	}
	var s Sorter[E]
	s.prepare(v)
	for r := range rounds {
		sortStep(&s, uint(r*rshift), keyOf)
	}
	s.iterate(yield)
}
