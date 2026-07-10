package radsort

import "iter"

// MapKey is the set of map-key types Map can order — the types with a dedicated
// typed sort entry point (Uint32s, Ints, Float64s, ...).
type MapKey interface {
	uint | int | uint32 | uint64 | int32 | int64 | float32 | float64
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
	return func(yield func(K, V) bool) {
		sortedKeys(keys, func(k K) bool { return yield(k, m[k]) })
	}
}

// sortedKeys sorts keys with radsort and calls yield for each key in ascending
// order, using Section 4.1 iteration to skip the compaction pass. Dispatch is on
// the slice header and the concrete yield is recovered once (not per element),
// so neither step boxes per element.
func sortedKeys[K MapKey](keys []K, yield func(K) bool) {
	switch s := any(keys).(type) {
	case []uint:
		seqKey(s, wordRounds, uintKey, any(yield).(func(uint) bool))
	case []int:
		seqKey(s, wordRounds, intKey, any(yield).(func(int) bool))
	case []uint32:
		seqU32(s, any(yield).(func(uint32) bool))
	case []uint64:
		seqU64(s, any(yield).(func(uint64) bool))
	case []int32:
		seqKey(s, 4, int32Key, any(yield).(func(int32) bool))
	case []int64:
		seqKey(s, 8, int64Key, any(yield).(func(int64) bool))
	case []float32:
		seqKey(s, 4, float32Key, any(yield).(func(float32) bool))
	case []float64:
		seqKey(s, 8, float64Key, any(yield).(func(float64) bool))
	default:
		panic("radsort: unexpected map key type")
	}
}
