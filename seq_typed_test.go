package radsort

import (
	"cmp"
	"iter"
	"slices"
	"testing"
)

func checkSeq[E cmp.Ordered](t *testing.T, name string, x []E, seq func([]E) iter.Seq[E]) {
	want := slices.Clone(x)
	slices.Sort(want)
	got := slices.Collect(seq(x))
	if !slices.Equal(got, want) {
		t.Fatalf("%s n=%d: mismatch", name, len(x))
	}
}

// TestTypedSeqs checks the per-type iterators against slices.Sort. Uint32Seq has
// its own exhaustive test; the others share the same iterate machinery, so
// testSizes suffices. Float data avoids NaN, which slices.Sort orders
// differently than radsort's NaNs-last key mapping.
func TestTypedSeqs(t *testing.T) {
	r := newRNG()
	for _, n := range testSizes {
		u64 := make([]uint64, n)
		uw := make([]uint, n)
		iw := make([]int, n)
		i32 := make([]int32, n)
		i64 := make([]int64, n)
		f32 := make([]float32, n)
		f64 := make([]float64, n)
		for i := range u64 {
			u64[i] = r.Uint64()
			uw[i] = uint(r.Uint64())
			iw[i] = int(r.Uint64())
			i32[i] = int32(r.Uint32())
			i64[i] = int64(r.Uint64())
			f32[i] = float32(int32(r.Uint32()))
			f64[i] = float64(int64(r.Uint64()))
		}
		checkSeq(t, "Uint64Seq", u64, Uint64Seq)
		checkSeq(t, "UintSeq", uw, UintSeq)
		checkSeq(t, "IntSeq", iw, IntSeq)
		checkSeq(t, "Int32Seq", i32, Int32Seq)
		checkSeq(t, "Int64Seq", i64, Int64Seq)
		checkSeq(t, "Float32Seq", f32, Float32Seq)
		checkSeq(t, "Float64Seq", f64, Float64Seq)
	}
}

func checkEarlyStop[E cmp.Ordered](t *testing.T, name string, x []E, seq func([]E) iter.Seq[E], k int) {
	want := slices.Clone(x)
	slices.Sort(want)
	var got []E
	for v := range seq(x) {
		got = append(got, v)
		if len(got) == k {
			break
		}
	}
	if !slices.Equal(got, want[:k]) {
		t.Fatalf("%s early-stop: prefix mismatch", name)
	}
}

// TestTypedSeqEarlyStop exercises the yield-returns-false path for both the
// monomorphised (uint32) and generic keyed (int64, float64) iterators.
func TestTypedSeqEarlyStop(t *testing.T) {
	r := newRNG()
	const n, k = 10_000, 100
	u := make([]uint32, n)
	i := make([]int64, n)
	f := make([]float64, n)
	for j := range u {
		u[j] = r.Uint32()
		i[j] = int64(r.Uint64())
		f[j] = float64(int64(r.Uint64()))
	}
	checkEarlyStop(t, "Uint32Seq", u, Uint32Seq, k)
	checkEarlyStop(t, "Int64Seq", i, Int64Seq, k)
	checkEarlyStop(t, "Float64Seq", f, Float64Seq, k)
}

// TestSortKeySeq checks the generic iterator: equivalence with a plain sort,
// stability under heavy key collisions, and the early-stop path.
func TestSortKeySeq(t *testing.T) {
	r := newRNG()
	u32Key := func(v uint32) uint64 { return uint64(v) }

	for _, n := range testSizes { // equivalence with slices.Sort
		x := make([]uint32, n)
		for i := range x {
			x[i] = r.Uint32()
		}
		want := slices.Clone(x)
		slices.Sort(want)
		if got := slices.Collect(SortKeySeq(x, 4, u32Key)); !slices.Equal(got, want) {
			t.Fatalf("equivalence n=%d: mismatch", n)
		}
	}

	for n := 0; n <= 4*blockSize+radix; n++ { // exhaustive boundaries, generic seqKey path
		x := make([]uint32, n)
		for i := range x {
			x[i] = r.Uint32() % 300 // heavy collisions, many partial blocks
		}
		want := slices.Clone(x)
		slices.Sort(want)
		if got := slices.Collect(SortKeySeq(x, 4, u32Key)); !slices.Equal(got, want) {
			t.Fatalf("exhaustive n=%d: mismatch", n)
		}
	}

	type pair struct { // stability: equal keys keep input order
		key uint32
		seq int
	}
	byKey := func(p pair) uint64 { return uint64(p.key) }
	for _, n := range []int{257, blockSize + 1, 5000, 100_000} {
		px := make([]pair, n)
		for i := range px {
			px[i] = pair{key: r.Uint32() % 100, seq: i} // heavy collisions
		}
		got := slices.Collect(SortKeySeq(px, 4, byKey))
		if len(got) != n {
			t.Fatalf("stability n=%d: got %d elements", n, len(got))
		}
		for i := 1; i < len(got); i++ {
			if got[i-1].key > got[i].key || (got[i-1].key == got[i].key && got[i-1].seq > got[i].seq) {
				t.Fatalf("stability n=%d: violated at %d", n, i)
			}
		}
	}

	x := make([]uint32, 10_000) // early stop yields a correct sorted prefix
	for i := range x {
		x[i] = r.Uint32()
	}
	want := slices.Clone(x)
	slices.Sort(want)
	var got []uint32
	for v := range SortKeySeq(x, 4, u32Key) {
		if got = append(got, v); len(got) == 50 {
			break
		}
	}
	if !slices.Equal(got, want[:50]) {
		t.Fatalf("early-stop: prefix mismatch")
	}
}
