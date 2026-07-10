//go:build nounsafe || appengine

package radsort

// Safe fallback for builds tagged `nounsafe` or `appengine`: the pointer-cursor
// phase (unsafe.go) is omitted and every sort uses the safe index-based phase.
// See unsafe.go for the rationale.

func sortRoundsU32(s *Sorter[uint32]) {
	for r := range 4 {
		sortStepU32(s, uint(r)*rshift)
	}
}

func sortRoundsU64(s *Sorter[uint64]) {
	for r := range 8 {
		sortStepU64(s, uint(r)*rshift)
	}
}
