package radsort

import (
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"testing"
)

// sizes chosen to exercise the boundaries of the block/scratch machinery:
// empty, tiny, sub-block, exactly one block, block+1, spanning many blocks,
// exact multiples of blockSize, and sizes near radix*blockSize.
var testSizes = []int{
	0, 1, 2, 3, 7, 255, 256, 257,
	blockSize - 1, blockSize, blockSize + 1,
	2*blockSize - 1, 2 * blockSize, 2*blockSize + 1,
	radix + 3, 5000, 1 << 16, 100_000,
	nscratch*blockSize/8 + 123,
}

func newRNG() *rand.Rand { return rand.New(rand.NewPCG(0x9e3779b9, 0x1234567)) }

func TestUint32s(t *testing.T) {
	r := newRNG()
	for _, n := range testSizes {
		x := make([]uint32, n)
		for i := range x {
			x[i] = r.Uint32()
		}
		want := slices.Clone(x)
		slices.Sort(want)
		Uint32s(x)
		if !slices.Equal(x, want) {
			t.Fatalf("n=%d: not sorted", n)
		}
	}
}

func TestUint64s(t *testing.T) {
	r := newRNG()
	for _, n := range testSizes {
		x := make([]uint64, n)
		for i := range x {
			x[i] = r.Uint64()
		}
		want := slices.Clone(x)
		slices.Sort(want)
		Uint64s(x)
		if !slices.Equal(x, want) {
			t.Fatalf("n=%d: not sorted", n)
		}
	}
}

func TestInt64s(t *testing.T) {
	r := newRNG()
	for _, n := range testSizes {
		x := make([]int64, n)
		for i := range x {
			x[i] = int64(r.Uint64())
		}
		want := slices.Clone(x)
		slices.Sort(want)
		Int64s(x)
		if !slices.Equal(x, want) {
			t.Fatalf("n=%d: not sorted", n)
		}
	}
}

func TestInt32s(t *testing.T) {
	r := newRNG()
	for _, n := range testSizes {
		x := make([]int32, n)
		for i := range x {
			x[i] = int32(r.Uint32())
		}
		want := slices.Clone(x)
		slices.Sort(want)
		Int32s(x)
		if !slices.Equal(x, want) {
			t.Fatalf("n=%d: not sorted", n)
		}
	}
}

func TestFloat64s(t *testing.T) {
	r := newRNG()
	for _, n := range testSizes {
		x := make([]float64, n)
		for i := range x {
			x[i] = (r.Float64() - 0.5) * math.MaxFloat64
		}
		want := slices.Clone(x)
		slices.Sort(want)
		Float64s(x)
		if !slices.Equal(x, want) {
			t.Fatalf("n=%d: not sorted", n)
		}
	}
}

// distributions covers input shapes that stress a comparison sort's fast paths
// and a radix sort's key handling differently.
func TestDistributions(t *testing.T) {
	r := newRNG()
	const n = 50_000
	gens := map[string]func() []uint32{
		"uniform": func() []uint32 {
			x := make([]uint32, n)
			for i := range x {
				x[i] = r.Uint32()
			}
			return x
		},
		"sorted": func() []uint32 {
			x := make([]uint32, n)
			for i := range x {
				x[i] = uint32(i)
			}
			return x
		},
		"reverse": func() []uint32 {
			x := make([]uint32, n)
			for i := range x {
				x[i] = uint32(n - i)
			}
			return x
		},
		"fewUnique": func() []uint32 {
			x := make([]uint32, n)
			for i := range x {
				x[i] = r.Uint32() % 8
			}
			return x
		},
		"allEqual": func() []uint32 {
			x := make([]uint32, n)
			for i := range x {
				x[i] = 42
			}
			return x
		},
		"smallRange": func() []uint32 {
			x := make([]uint32, n)
			for i := range x {
				x[i] = r.Uint32() % 1000
			}
			return x
		},
	}
	for name, gen := range gens {
		x := gen()
		want := slices.Clone(x)
		slices.Sort(want)
		Uint32s(x)
		if !slices.Equal(x, want) {
			t.Fatalf("dist=%s: not sorted", name)
		}
	}
}

// TestStability sorts key/value pairs by key and checks that equal keys keep
// their original relative order — the defining property Radsort provides and a
// plain slices.Sort does not.
func TestStability(t *testing.T) {
	r := newRNG()
	for _, n := range []int{257, blockSize + 1, 5000, 100_000} {
		type pair struct {
			key uint32
			seq int
		}
		x := make([]pair, n)
		for i := range x {
			x[i] = pair{key: r.Uint32() % 100, seq: i} // heavy key collisions
		}
		SortKey(x, 4, func(p pair) uint64 { return uint64(p.key) })

		for i := 1; i < len(x); i++ {
			if x[i-1].key > x[i].key {
				t.Fatalf("n=%d: keys out of order at %d", n, i)
			}
			if x[i-1].key == x[i].key && x[i-1].seq > x[i].seq {
				t.Fatalf("n=%d: unstable at %d (seq %d before %d)", n, i, x[i-1].seq, x[i].seq)
			}
		}
	}
}

// TestExhaustiveSizes checks every length across the first few block
// boundaries, where off-by-one errors in the block machinery would surface.
func TestExhaustiveSizes(t *testing.T) {
	r := newRNG()
	for n := 0; n <= 4*blockSize+radix; n++ {
		x := make([]uint32, n)
		for i := range x {
			x[i] = r.Uint32() % 300 // force many partial/duplicate buckets
		}
		want := slices.Clone(x)
		slices.Sort(want)
		Uint32s(x)
		if !slices.Equal(x, want) {
			t.Fatalf("n=%d: not sorted", n)
		}
	}
}

// TestSorterReuse sorts many differently-sized inputs with a single reused
// Sorter (growing then shrinking), guarding against stale buffer state leaking
// between calls.
func TestSorterReuse(t *testing.T) {
	r := newRNG()
	key := func(v uint32) uint64 { return uint64(v) }
	var gen, fast Sorter[uint32]
	for _, n := range []int{100_000, 5000, blockSize + 1, 100_000, 257, 0, 1, 2, 50_000} {
		x := make([]uint32, n)
		for i := range x {
			x[i] = r.Uint32() % 1000
		}
		want := slices.Clone(x)
		slices.Sort(want)

		y := slices.Clone(x)
		gen.SortKey(x, 4, key) // generic recycled path
		sortU32(&fast, y)      // monomorphised recycled path
		if !slices.Equal(x, want) {
			t.Fatalf("reuse (generic) n=%d: not sorted", n)
		}
		if !slices.Equal(y, want) {
			t.Fatalf("reuse (fast) n=%d: not sorted", n)
		}
	}
}

// TestSorterZeroAlloc confirms a warmed-up Sorter sorts without allocating.
func TestSorterZeroAlloc(t *testing.T) {
	r := newRNG()
	x := make([]uint32, 50_000)
	for i := range x {
		x[i] = r.Uint32()
	}
	key := func(v uint32) uint64 { return uint64(v) }
	var s Sorter[uint32]
	s.SortKey(x, 4, key) // warm up: grow buffers

	if got := testing.AllocsPerRun(5, func() { s.SortKey(x, 4, key) }); got != 0 {
		t.Errorf("recycled SortKey: %v allocs/op, want 0", got)
	}
	var fs Sorter[uint32]
	sortU32(&fs, x)
	if got := testing.AllocsPerRun(5, func() { sortU32(&fs, x) }); got != 0 {
		t.Errorf("recycled fast sort: %v allocs/op, want 0", got)
	}
}

// TestPreservesMultiset guards against elements being lost or duplicated by the
// block-reuse machinery, independent of ordering.
func TestPreservesMultiset(t *testing.T) {
	r := newRNG()
	for _, n := range testSizes {
		x := make([]uint64, n)
		for i := range x {
			x[i] = r.Uint64()
		}
		want := slices.Clone(x)
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
		Uint64s(x)
		if !slices.Equal(x, want) {
			t.Fatalf("n=%d: multiset changed", n)
		}
	}
}

// TestLargeDispatch sorts inputs above the unsafe/safe size threshold so the
// size-dispatched sort phase is exercised end-to-end through the public entry
// points (the unsafe pointer-cursor phase in the default build, the safe phase
// under -tags nounsafe/appengine).
func TestLargeDispatch(t *testing.T) {
	r := newRNG()
	for _, n := range []int{1 << 18, 1<<18 + 12345, 400_000} {
		x := make([]uint32, n)
		y := make([]uint64, n)
		for i := range x {
			x[i] = r.Uint32()
			y[i] = r.Uint64()
		}
		wx := slices.Clone(x)
		slices.Sort(wx)
		wy := slices.Clone(y)
		slices.Sort(wy)
		Uint32s(x)
		Uint64s(y)
		if !slices.Equal(x, wx) {
			t.Fatalf("Uint32s n=%d: not sorted", n)
		}
		if !slices.Equal(y, wy) {
			t.Fatalf("Uint64s n=%d: not sorted", n)
		}
	}
}
