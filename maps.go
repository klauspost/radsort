package radsort

import "iter"

// MapKey is the set of map-key types Map can order. These are exactly the
// types with a dedicated (monomorphised) sort helper.
type MapKey interface {
	uint32 | uint64 | int32 | int64 | float32 | float64
}

// Map returns an iterator over the key/value pairs of m in ascending key order,
// sorting the keys with radsort. It is a convenience for iterating a large map
// in key order.
//
// Values are fetched from m by key during iteration, so the map must not be
// mutated while the iterator is in use. A float NaN key sorts last and, because
// no map lookup can retrieve a NaN key, yields the zero value — NaN keys are
// pathological and should not be relied upon.
func Map[K MapKey, V any](m map[K]V) iter.Seq2[K, V] {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortKeys(keys)
	return func(yield func(K, V) bool) {
		for _, k := range keys {
			if !yield(k, m[k]) {
				return
			}
		}
	}
}

// sortKeys sorts keys in place, dispatching to the monomorphised helper for the
// concrete element type. The assertion is on the slice header, so there is no
// per-element boxing.
func sortKeys[K MapKey](keys []K) {
	switch s := any(keys).(type) {
	case []uint32:
		Uint32s(s)
	case []uint64:
		Uint64s(s)
	case []int32:
		Int32s(s)
	case []int64:
		Int64s(s)
	case []float32:
		Float32s(s)
	case []float64:
		Float64s(s)
	default:
		panic("unexpect map key type")
	}
}
