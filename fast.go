package radsort

// Specialised sort phases for the common integer types. These are byte-for-byte
// identical to the generic sortStep except that the digit is extracted inline
// (c := byte(key >> shift)) rather than through an indirect keyOf call. Because
// the sort phase is the memory/throughput bottleneck, removing the per-element
// call is worthwhile; the paper makes the same monomorphisation argument.

func sortStepU32(s *Sorter[uint32], shift uint) {
	var buckets [radix]obucket[uint32]
	var counts [radix]uint32

	iOut := 0
	for ; iOut < radix; iOut++ {
		p := int(s.perm[iOut])
		buckets[iOut] = obucket[uint32]{blk: s.blockat(p), phys: p}
		s.assignments[iOut] = uint8(iOut)
		counts[iOut] = 1
	}

	ip := 0
	for iIn := radix; iIn < s.fill; iIn++ {
		in := s.blockat(int(s.perm[iIn]))
		l := s.blocksize(iIn, &ip)
		for _, e := range in[:l] {
			c := uint8(e >> shift)
			bkt := &buckets[c]
			bkt.blk[bkt.off] = e
			bkt.off++
			if bkt.off == blockSize {
				p := int(s.perm[iOut])
				bkt.blk, bkt.off, bkt.phys = s.blockat(p), 0, p
				s.assignments[iOut] = c
				counts[c]++
				iOut++
			}
		}
	}

	s.fill = iOut
	fixPerm(s, &counts, &buckets)
}

func sortStepU64(s *Sorter[uint64], shift uint) {
	var buckets [radix]obucket[uint64]
	var counts [radix]uint32

	iOut := 0
	for ; iOut < radix; iOut++ {
		p := int(s.perm[iOut])
		buckets[iOut] = obucket[uint64]{blk: s.blockat(p), phys: p}
		s.assignments[iOut] = uint8(iOut)
		counts[iOut] = 1
	}

	ip := 0
	for iIn := radix; iIn < s.fill; iIn++ {
		in := s.blockat(int(s.perm[iIn]))
		l := s.blocksize(iIn, &ip)
		for _, e := range in[:l] {
			c := uint8(e >> shift)
			bkt := &buckets[c]
			bkt.blk[bkt.off] = e
			bkt.off++
			if bkt.off == blockSize {
				p := int(s.perm[iOut])
				bkt.blk, bkt.off, bkt.phys = s.blockat(p), 0, p
				s.assignments[iOut] = c
				counts[c]++
				iOut++
			}
		}
	}

	s.fill = iOut
	fixPerm(s, &counts, &buckets)
}
