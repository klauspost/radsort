package radsort

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
	"unsafe"
)

// --- data generators -------------------------------------------------------

func genUint32(n int, dist string, r *rand.Rand) []uint32 {
	x := make([]uint32, n)
	switch dist {
	case "uniform":
		for i := range x {
			x[i] = r.Uint32()
		}
	case "sorted":
		for i := range x {
			x[i] = uint32(i)
		}
	case "reverse":
		for i := range x {
			x[i] = uint32(n - i)
		}
	case "fewUnique":
		for i := range x {
			x[i] = r.Uint32() % 16
		}
	case "smallRange":
		for i := range x {
			x[i] = r.Uint32() % 1000
		}
	case "nearlySorted":
		for i := range x {
			x[i] = uint32(i)
		}
		for k := 0; k < n/100; k++ { // perturb 1% of positions
			x[r.IntN(n)] = r.Uint32()
		}
	default:
		panic(dist)
	}
	return x
}

func genUint64(n int, r *rand.Rand) []uint64 {
	x := make([]uint64, n)
	for i := range x {
		x[i] = r.Uint64()
	}
	return x
}

type kv struct {
	key uint32
	val uint32
}

func genKV(n int, r *rand.Rand) []kv {
	x := make([]kv, n)
	for i := range x {
		x[i] = kv{key: r.Uint32(), val: uint32(i)}
	}
	return x
}

// --- generic timing helper -------------------------------------------------

func benchSort[E any](b *testing.B, data []E, sortFn func([]E)) {
	var z E
	work := make([]E, len(data))
	b.SetBytes(int64(len(data)) * int64(unsafe.Sizeof(z)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		copy(work, data)
		b.StartTimer()
		sortFn(work)
	}
}

func kvCmp(a, b kv) int {
	switch {
	case a.key < b.key:
		return -1
	case a.key > b.key:
		return 1
	}
	return 0
}

// --- standard benchmarks (go test -bench) ----------------------------------

var benchSizes = []int{1e3, 1e4, 1e5, 3e5, 1e6, 3e6, 1e7, 3e7}

func BenchmarkUint32(b *testing.B) {
	r := newRNG()
	for _, n := range benchSizes {
		data := genUint32(n, "uniform", r)
		b.Run(fmt.Sprintf("radsort/%d", n), func(b *testing.B) { benchSort(b, data, Uint32s) })
		b.Run(fmt.Sprintf("stdlib/%d", n), func(b *testing.B) { benchSort(b, data, slices.Sort[[]uint32]) })
	}
}

func BenchmarkUint64(b *testing.B) {
	r := newRNG()
	for _, n := range benchSizes {
		data := genUint64(n, r)
		b.Run(fmt.Sprintf("radsort/%d", n), func(b *testing.B) { benchSort(b, data, Uint64s) })
		b.Run(fmt.Sprintf("stdlib/%d", n), func(b *testing.B) { benchSort(b, data, slices.Sort[[]uint64]) })
	}
}

func BenchmarkKV(b *testing.B) {
	r := newRNG()
	kvSort := func(x []kv) { SortKey(x, 4, func(p kv) uint64 { return uint64(p.key) }) }
	stdSort := func(x []kv) { slices.SortFunc(x, kvCmp) }
	for _, n := range benchSizes {
		data := genKV(n, r)
		b.Run(fmt.Sprintf("radsort/%d", n), func(b *testing.B) { benchSort(b, data, kvSort) })
		b.Run(fmt.Sprintf("stdlib/%d", n), func(b *testing.B) { benchSort(b, data, stdSort) })
	}
}

// BenchmarkRecycle contrasts allocating a fresh Sorter per call against reusing
// one across calls, for both the generic (SortKey) and monomorphised (Uint32s)
// paths. The generic pair isolates the allocation cost, since both use the same
// inner loop and differ only in buffer reuse. Run with -benchmem.
func BenchmarkRecycle(b *testing.B) {
	r := newRNG()
	key := func(v uint32) uint64 { return uint64(v) }
	for _, n := range []int{1000, 10000, 100000, 1000000} {
		data := genUint32(n, "uniform", r)

		b.Run(fmt.Sprintf("generic-fresh/%d", n), func(b *testing.B) {
			benchSort(b, data, func(x []uint32) { SortKey(x, 4, key) })
		})
		b.Run(fmt.Sprintf("generic-recycled/%d", n), func(b *testing.B) {
			s := new(Sorter[uint32])
			benchSort(b, data, func(x []uint32) { s.SortKey(x, 4, key) })
		})
		b.Run(fmt.Sprintf("fast-fresh/%d", n), func(b *testing.B) {
			benchSort(b, data, Uint32s)
		})
		b.Run(fmt.Sprintf("fast-recycled/%d", n), func(b *testing.B) {
			s := new(Sorter[uint32])
			benchSort(b, data, func(x []uint32) { sortU32(s, x) })
		})
	}
}

func BenchmarkDistribution(b *testing.B) {
	r := newRNG()
	const n = 1e7
	for _, d := range []string{"uniform", "sorted", "reverse", "fewUnique", "smallRange", "nearlySorted"} {
		data := genUint32(n, d, r)
		b.Run(fmt.Sprintf("radsort/%s", d), func(b *testing.B) { benchSort(b, data, Uint32s) })
		b.Run(fmt.Sprintf("stdlib/%s", d), func(b *testing.B) { benchSort(b, data, slices.Sort[[]uint32]) })
	}
}
