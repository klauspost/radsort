// Package radsort implements Radsort, a stable LSD radix sort with O(sqrt n)
// space overhead, as described in "Parallel O(sqrt n) Overhead LSD Radix Sort"
// by Robert Clausecker and Florian Schintke (arXiv:2607.05302v1, 2026).
//
// Unlike a conventional out-of-place LSD radix sort, which needs a second array
// of n elements, Radsort treats the input as a sequence of blocks and reuses
// each input block for output once it has been consumed. Only a constant number
// of scratch blocks plus O(n/b) bookkeeping is required. With a block size b in
// Theta(sqrt n) the overhead is O(sqrt n); this implementation uses a fixed
// block size, giving a small fixed fraction of the input as overhead.
//
// The port follows the reference C implementation (radixsort_permuted.c). Only
// the inner loop of the sort phase inspects keys; all block/permutation
// bookkeeping is key-agnostic and therefore shared across element types.
package radsort

const (
	radix     = 256       // sigma: number of buckets, one byte per round
	rshift    = 8         // bits consumed per round
	blockSize = 512       // b: elements per block
	nscratch  = 2 * radix // T: scratch blocks (sigma head start + sigma partials)
)

// partial tracks a block that is only partly filled (P in the paper).
type partial struct {
	index  uint32 // logical block index
	length uint32 // number of live elements at the block's start
}

// obucket is the output-block cursor for one bucket during the sort phase.
type obucket[E any] struct {
	blk  []E // current output block (len blockSize)
	off  int // next free offset == elements written into blk
	phys int // physical index of blk, for partial-block identification
}

// Sorter holds the scratch and bookkeeping buffers a sort needs. Reusing one
// across calls avoids per-call allocation: after the first sort has grown the
// buffers to len(data), later sorts of the same-or-smaller length allocate
// nothing. The zero value is ready to use. A Sorter is tied to element type E
// and must not be used concurrently.
type Sorter[E any] struct {
	// reusable buffers, grown on demand
	scratch     []E      // T: nscratch blocks of blockSize elements
	perm        []uint32 // block permutation, logical -> physical (double buffered)
	perm2       []uint32
	assignments []uint8 // U: logical block -> bucket, for the current round

	// per-call state
	pairs    []E // the input array; blocks overlay it as they are consumed
	n        int // len(pairs)
	fill     int // f: logical index one past the last allocated block
	partials [radix]partial
}

func nperm(n int) int { return nscratch + n/blockSize }

// blockat returns the block at physical index j (length blockSize). The first
// nscratch indices live in the scratch buffer, the rest overlay the input.
func (s *Sorter[E]) blockat(j int) []E {
	if j < nscratch {
		b := j * blockSize
		return s.scratch[b : b+blockSize : b+blockSize]
	}
	b := (j - nscratch) * blockSize
	return s.pairs[b : b+blockSize : b+blockSize]
}

// blocksize returns the number of live elements in the block at logical index
// i, advancing *ip past it if it is the next partial block.
func (s *Sorter[E]) blocksize(i int, ip *int) int {
	if int(s.partials[*ip].index) == i {
		l := int(s.partials[*ip].length)
		*ip++
		return l
	}
	return blockSize
}

// grow returns a slice of length n backed by b, reusing b's storage when it is
// large enough. The reused prefix is not cleared; callers must not read it
// before writing (the radix machinery always does).
func grow[T any](b []T, n int) []T {
	if cap(b) >= n {
		return b[:n]
	}
	return make([]T, n)
}

// prepare (re)initialises s to sort pairs, reusing existing buffers where they
// are large enough. This establishes the data-structure invariant for round 0.
func (s *Sorter[E]) prepare(pairs []E) {
	n := len(pairs)
	np := nperm(n)
	s.pairs = pairs
	s.n = n
	s.fill = radix + n/blockSize + 1
	s.perm = grow(s.perm, np)
	s.perm2 = grow(s.perm2, np)
	s.assignments = grow(s.assignments, np)
	s.scratch = grow(s.scratch, nscratch*blockSize)

	i := 0
	for ; i < radix; i++ { // head start: first sigma scratch blocks
		s.perm[i] = uint32(i)
	}
	for ; i < s.fill-1; i++ { // identity over the full blocks overlaying pairs
		s.perm[i] = uint32(i + (nscratch - radix))
	}
	for ; i < np; i++ { // remaining (second half of) scratch blocks
		s.perm[i] = uint32(i - (s.fill - 1) + radix)
	}

	nTail := n % blockSize
	copy(s.blockat(radix), pairs[blockSize*(n/blockSize):]) // tail -> scratch block sigma
	s.partials[0] = partial{index: uint32(s.fill - 1), length: uint32(nTail)}
}

// sortStep performs one LSD round, sorting stably by the byte at the given bit
// shift of the key returned by keyOf. This is the only routine that reads keys.
func sortStep[E any](s *Sorter[E], shift uint, keyOf func(E) uint64) {
	var buckets [radix]obucket[E]
	var counts [radix]uint32

	// One initial output block per bucket, taken from the head start.
	iOut := 0
	for ; iOut < radix; iOut++ {
		p := int(s.perm[iOut])
		buckets[iOut] = obucket[E]{blk: s.blockat(p), phys: p}
		s.assignments[iOut] = uint8(iOut)
		counts[iOut] = 1
	}

	ip := 0
	for iIn := radix; iIn < s.fill; iIn++ { // traverse allocated blocks in logical order
		in := s.blockat(int(s.perm[iIn]))
		l := s.blocksize(iIn, &ip)
		for _, e := range in[:l] {
			c := uint8(keyOf(e) >> shift)
			bkt := &buckets[c]
			bkt.blk[bkt.off] = e
			bkt.off++
			if bkt.off == blockSize { // block full: draw the next block (Lemma 1: iOut < iIn)
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

// fixPerm deinterleaves the buckets produced by the sort phase into a fresh
// permutation and rebuilds the partial-block table, restoring the invariant.
func fixPerm[E any](s *Sorter[E], counts *[radix]uint32, buckets *[radix]obucket[E]) {
	var starts [radix]uint32
	starts[0] = radix
	for i := 1; i < radix; i++ {
		starts[i] = starts[i-1] + counts[i-1]
	}

	newperm := s.perm2
	i := 0
	for ; i < s.fill; i++ {
		b := s.assignments[i]
		j := starts[b]
		starts[b]++
		p := int(s.perm[i])
		newperm[j] = uint32(p)
		if p == buckets[b].phys { // this is bucket b's trailing (partial) block
			s.partials[b] = partial{index: j, length: uint32(buckets[b].off)}
		}
	}

	np := nperm(s.n)
	copy(newperm[:radix], s.perm[i:i+radix]) // next round's head start (Lemma 2)
	i += radix
	copy(newperm[i:], s.perm[i:np])

	s.fill += radix
	s.perm, s.perm2 = s.perm2, s.perm
}

// compact moves the logically ordered blocks back into pairs, closing the gaps
// left by partial blocks. Push-pull: push whatever occupies a target slot to a
// known free slot, then pull the correct block in. Ported from the reference.
func compact[E any](s *Sorter[E]) {
	perm, pinv := s.perm, s.perm2
	np := nperm(s.n)
	for i := range np {
		pinv[perm[i]] = uint32(i)
	}

	start := 0 // next write position in pairs
	ip := 0
	jFree := int(perm[0])
	iFree := 0

	jOut := nscratch
	for ; jOut < np; jOut++ { // physical blocks overlaying pairs
		iIn := jOut - radix
		iOut := int(pinv[jOut])
		jIn := int(perm[iIn])

		if iOut > iIn && iOut < s.fill { // an unrelated allocated block sits here: push it away
			copy(s.blockat(jFree), s.blockat(jOut))
			perm[iFree], perm[iOut] = uint32(jOut), uint32(jFree)
			pinv[jFree], pinv[jOut] = uint32(iOut), uint32(iFree)
			iFree = iOut
			iOut = int(pinv[jOut])
		}

		l := s.blocksize(iIn, &ip)
		copy(s.pairs[start:start+l], s.blockat(jIn)[:l]) // pull block into place
		perm[iIn], perm[iOut] = uint32(jOut), uint32(jIn)
		pinv[jIn], pinv[jOut] = uint32(iOut), uint32(iIn)
		start += l
		if iIn != iOut { // pulled from elsewhere: its old spot is now free
			jFree, iFree = jIn, iOut
		}
	}

	for iIn := jOut - radix; iIn < s.fill; iIn++ { // remaining blocks (all in scratch)
		jIn := int(perm[iIn])
		l := s.blocksize(iIn, &ip)
		copy(s.pairs[start:start+l], s.blockat(jIn)[:l])
		start += l
	}
}

// SortKey sorts data stably in ascending order of the unsigned key returned by
// keyOf, using the given number of byte rounds (least significant first). Only
// the low rounds*8 bits of the key are considered.
//
// This is the generic entry point; the byte-sized helpers (Uint32s, Uint64s,
// ...) call it with the appropriate round count and key mapping. It allocates
// working buffers per call; to sort repeatedly without allocating, reuse a
// Sorter via its SortKey method.
func SortKey[E any](data []E, rounds int, keyOf func(E) uint64) {
	var s Sorter[E]
	s.SortKey(data, rounds, keyOf)
}

// SortKey sorts data using s's buffers, reusing them across calls. See the
// package-level SortKey for the sorting semantics and Sorter for reuse.
func (s *Sorter[E]) SortKey(data []E, rounds int, keyOf func(E) uint64) {
	if len(data) < 2 {
		return
	}
	s.prepare(data)
	for r := range rounds {
		sortStep(s, uint(r*rshift), keyOf)
	}
	compact(s)
}
