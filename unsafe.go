//go:build !nounsafe && !appengine

package radsort

import "unsafe"

// This file holds the pointer-cursor sort phase (the paper's Section 4.2,
// "bitmanip"): each output bucket is tracked by a raw {cur,end} pointer pair
// rather than the safe {block []E, offset int} slice+index. That drops the
// per-write slice bounds check and shrinks the 256-entry bucket table, which is
// a few percent faster on large inputs but slower on small ones — so the
// dispatchers pick per size (see unsafeMinLen). Building with the `nounsafe` or
// `appengine` tag swaps in unsafe_disabled.go, which always uses the safe phase;
// results are identical, only speed differs.
//
// `end` is one past the block and is only ever compared, never dereferenced
// (cf. the paper's footnote 3: the equivalent C is formally UB but fine in
// practice). It is safe under Go's non-moving heap GC.

// unsafeMinLen is the input length at/above which the pointer-cursor phase
// overtakes the safe one. The measured crossover on a Zen 5 desktop is ~200-300K
// elements for both uint32 and uint64; below it the safe path wins, so both are
// kept. Approximate and hardware-dependent.
const unsafeMinLen = 1 << 18

type obucketP struct {
	cur, end unsafe.Pointer
	phys     int
}

func sortRoundsU32(s *Sorter[uint32]) {
	step := sortStepU32
	if s.n >= unsafeMinLen {
		step = sortStepU32Unsafe
	}
	for r := range 4 {
		step(s, uint(r)*rshift)
	}
}

func sortRoundsU64(s *Sorter[uint64]) {
	step := sortStepU64
	if s.n >= unsafeMinLen {
		step = sortStepU64Unsafe
	}
	for r := range 8 {
		step(s, uint(r)*rshift)
	}
}

func sortStepU32Unsafe(s *Sorter[uint32], shift uint) {
	var buckets [radix]obucketP
	var counts [radix]uint32

	iOut := 0
	for ; iOut < radix; iOut++ {
		p := int(s.perm[iOut])
		base := unsafe.Pointer(&s.blockat(p)[0])
		buckets[iOut] = obucketP{cur: base, end: unsafe.Add(base, blockSize*4), phys: p}
		s.assignments[iOut] = uint8(iOut)
		counts[iOut] = 1
	}

	ip := 0
	for iIn := radix; iIn < s.fill; iIn++ {
		in := s.blockat(int(s.perm[iIn]))
		l := s.blocksize(iIn, &ip)
		for _, e := range in[:l] {
			c := uint8(e >> shift)
			b := &buckets[c]
			*(*uint32)(b.cur) = e
			b.cur = unsafe.Add(b.cur, 4)
			if b.cur == b.end {
				p := int(s.perm[iOut])
				base := unsafe.Pointer(&s.blockat(p)[0])
				b.cur, b.end, b.phys = base, unsafe.Add(base, blockSize*4), p
				s.assignments[iOut] = c
				counts[c]++
				iOut++
			}
		}
	}

	s.fill = iOut

	// Rebuild the {phys, off} view fixPerm consumes (once per bucket, not per
	// element): off is the fill of each bucket's trailing block.
	var bk [radix]obucket[uint32]
	for c := range radix {
		base := unsafe.Pointer(&s.blockat(buckets[c].phys)[0])
		bk[c] = obucket[uint32]{phys: buckets[c].phys, off: int(uintptr(buckets[c].cur)-uintptr(base)) / 4}
	}
	fixPerm(s, &counts, &bk)
}

func sortStepU64Unsafe(s *Sorter[uint64], shift uint) {
	var buckets [radix]obucketP
	var counts [radix]uint32

	iOut := 0
	for ; iOut < radix; iOut++ {
		p := int(s.perm[iOut])
		base := unsafe.Pointer(&s.blockat(p)[0])
		buckets[iOut] = obucketP{cur: base, end: unsafe.Add(base, blockSize*8), phys: p}
		s.assignments[iOut] = uint8(iOut)
		counts[iOut] = 1
	}

	ip := 0
	for iIn := radix; iIn < s.fill; iIn++ {
		in := s.blockat(int(s.perm[iIn]))
		l := s.blocksize(iIn, &ip)
		for _, e := range in[:l] {
			c := uint8(e >> shift)
			b := &buckets[c]
			*(*uint64)(b.cur) = e
			b.cur = unsafe.Add(b.cur, 8)
			if b.cur == b.end {
				p := int(s.perm[iOut])
				base := unsafe.Pointer(&s.blockat(p)[0])
				b.cur, b.end, b.phys = base, unsafe.Add(base, blockSize*8), p
				s.assignments[iOut] = c
				counts[c]++
				iOut++
			}
		}
	}

	s.fill = iOut

	var bk [radix]obucket[uint64]
	for c := range radix {
		base := unsafe.Pointer(&s.blockat(buckets[c].phys)[0])
		bk[c] = obucket[uint64]{phys: buckets[c].phys, off: int(uintptr(buckets[c].cur)-uintptr(base)) / 8}
	}
	fixPerm(s, &counts, &bk)
}
