package radsort

import (
	"slices"
	"testing"
)

func TestMap(t *testing.T) {
	r := newRNG()

	// uint64 keys, verifying key order and that each value still matches m.
	m := make(map[uint64]int, 2000)
	for i := range 2000 {
		m[r.Uint64()] = i
	}
	var keys []uint64
	for k, v := range Map(m) {
		if v != m[k] {
			t.Fatalf("value mismatch for key %d: got %d want %d", k, v, m[k])
		}
		keys = append(keys, k)
	}
	if len(keys) != len(m) {
		t.Fatalf("yielded %d keys, want %d", len(keys), len(m))
	}
	if !slices.IsSorted(keys) {
		t.Fatal("uint64 keys not in ascending order")
	}

	// signed keys must order negatives correctly.
	mi := map[int64]string{-5: "a", 3: "b", -100: "c", 0: "d", 42: "e", -1: "f"}
	var ik []int64
	for k := range Map(mi) {
		ik = append(ik, k)
	}
	if !slices.IsSorted(ik) {
		t.Fatalf("int64 keys not sorted: %v", ik)
	}

	// word-sized signed keys (negatives via the word-width sign flip).
	mw := map[int]string{-5: "a", 3: "b", -100: "c", 0: "d", 42: "e", -1: "f"}
	var wk []int
	for k := range Map(mw) {
		wk = append(wk, k)
	}
	if !slices.IsSorted(wk) {
		t.Fatalf("int keys not sorted: %v", wk)
	}

	// float keys, including negatives.
	mf := map[float64]int{}
	for i := range 500 {
		mf[(r.Float64()-0.5)*1e6] = i
	}
	var fk []float64
	for k := range Map(mf) {
		fk = append(fk, k)
	}
	if !slices.IsSorted(fk) {
		t.Fatal("float64 keys not sorted")
	}

	// early return (break) must stop iteration.
	count := 0
	for range Map(m) {
		count++
		if count == 3 {
			break
		}
	}
	if count != 3 {
		t.Fatalf("break not honored: count=%d", count)
	}
}
