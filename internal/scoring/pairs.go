// Package scoring implements pairwise comparison runs and the
// Bradley–Terry score model that combines every run of a metric.
package scoring

import "math"

// NumPairs is n choose 2.
func NumPairs(n int) uint64 {
	if n < 2 {
		return 0
	}
	return uint64(n) * uint64(n-1) / 2
}

// PairAt maps k in [0, NumPairs(n)) to the pair (i, j), i < j, using the
// order (0,1), (0,2), (1,2), (0,3), (1,3), (2,3), ...
func PairAt(k uint64) (i, j int) {
	jj := uint64((1 + math.Sqrt(1+8*float64(k))) / 2)
	for jj*(jj-1)/2 > k {
		jj--
	}
	for (jj+1)*jj/2 <= k {
		jj++
	}
	return int(k - jj*(jj-1)/2), int(jj)
}

// PairIndex is the inverse of PairAt.
func PairIndex(i, j int) uint64 {
	if i > j {
		i, j = j, i
	}
	return uint64(j)*uint64(j-1)/2 + uint64(i)
}

// Perm is a keyed pseudo-random permutation of [0, m): a balanced Feistel
// network over the next even power of two, with cycle-walking back into
// range. It needs O(1) memory, so a run over any number of images can jump
// straight to any position.
type Perm struct {
	m    uint64
	half uint
	mask uint64
	keys [4]uint64
}

// NewPerm creates the permutation of [0, m) for seed.
func NewPerm(m uint64, seed uint64) Perm {
	bits := uint(2)
	for bits < 64 && (uint64(1)<<bits) < m {
		bits += 2
	}
	p := Perm{m: m, half: bits / 2, mask: (uint64(1) << (bits / 2)) - 1}
	s := seed
	for i := range p.keys {
		s += 0x9E3779B97F4A7C15
		p.keys[i] = splitmix(s)
	}
	return p
}

// At returns the permuted value of i (i < m).
func (p Perm) At(i uint64) uint64 {
	x := i
	for {
		x = p.round(x)
		if x < p.m {
			return x
		}
	}
}

func (p Perm) round(x uint64) uint64 {
	l, r := x>>p.half, x&p.mask
	for _, k := range p.keys {
		l, r = r, l^(splitmix(r^k)&p.mask)
	}
	return l<<p.half | r
}

func splitmix(z uint64) uint64 {
	z += 0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// Swap reports whether the pair at pos is shown with its images swapped
// (right-to-left), to avoid position bias.
func Swap(seed int64, pos uint64) bool {
	return splitmix(uint64(seed)^(pos*0xD6E8FEB86659FD93))&1 == 1
}

// PairForPos returns the ordinal indexes (into the run's image list) of the
// left and right image at position pos.
func PairForPos(seed int64, n int, pos uint64) (left, right int) {
	perm := NewPerm(NumPairs(n), uint64(seed))
	i, j := PairAt(perm.At(pos))
	if Swap(seed, pos) {
		return j, i
	}
	return i, j
}
