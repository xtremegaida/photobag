package scoring

import (
	"math"
)

// Edge records that item I beat item J with weight W (the net number of
// wins after contradicting results cancelled out).
type Edge struct {
	I, J int
	W    float64
}

// Fit is a Bradley–Terry maximum a posteriori estimate.
type Fit struct {
	// Theta are log-strengths; P(i beats j) = σ(θi − θj).
	Theta []float64
	// SE are standard errors of Theta.
	SE []float64
	// Approx is true when SE uses the diagonal approximation (large n).
	Approx     bool
	Iterations int
	Converged  bool
}

// ExactLimit is the largest item count for which Newton refinement and the
// exact inverse-Hessian standard errors are computed (O(n³)).
const ExactLimit = 1000

// FitBT fits strengths for n items. Every item additionally gets one
// virtual win and one virtual loss against a fixed anchor with θ = 0. This
// acts as a prior: undefeated items stay finite, items in disconnected
// groups remain comparable on one scale, and no renormalisation is needed.
func FitBT(n int, edges []Edge) Fit {
	f := Fit{Theta: make([]float64, n), SE: make([]float64, n)}
	if n == 0 {
		f.Converged = true
		return f
	}
	type adj struct {
		j int
		n float64
	}
	wins := make([]float64, n)
	nbr := make([][]adj, n)
	for _, e := range edges {
		if e.W <= 0 || e.I == e.J {
			continue
		}
		wins[e.I] += e.W
		nbr[e.I] = append(nbr[e.I], adj{e.J, e.W})
		nbr[e.J] = append(nbr[e.J], adj{e.I, e.W})
	}
	for i := range wins {
		wins[i]++ // virtual win against the anchor
	}

	// Minorise–maximise (Hunter 2004), Gauss–Seidel order.
	gamma := make([]float64, n)
	for i := range gamma {
		gamma[i] = 1
	}
	const tol = 1e-6
	const maxIter = 20000
	for f.Iterations = 1; f.Iterations <= maxIter; f.Iterations++ {
		var delta float64
		for i := 0; i < n; i++ {
			denom := 2 / (gamma[i] + 1) // anchor: 2 games against γ = 1
			for _, a := range nbr[i] {
				denom += a.n / (gamma[i] + gamma[a.j])
			}
			g := wins[i] / denom
			delta = math.Max(delta, math.Abs(math.Log(g/gamma[i])))
			gamma[i] = g
		}
		if delta < tol {
			f.Converged = true
			break
		}
	}
	for i := range gamma {
		f.Theta[i] = math.Log(gamma[i])
	}

	if n > ExactLimit {
		// Diagonal (Fisher information) approximation.
		f.Approx = true
		for i := 0; i < n; i++ {
			h := anchorInfo(f.Theta[i])
			for _, a := range nbr[i] {
				p := sigmoid(f.Theta[i] - f.Theta[a.j])
				h += a.n * p * (1 - p)
			}
			f.SE[i] = 1 / math.Sqrt(h)
		}
		return f
	}

	// Newton refinement on the exact Hessian, then SE = sqrt(diag(H⁻¹)).
	hess := func() ([]float64, []float64) {
		H := make([]float64, n*n)
		g := make([]float64, n)
		for i := 0; i < n; i++ {
			s := sigmoid(f.Theta[i])
			g[i] = wins[i] - 2*s
			H[i*n+i] = 2 * s * (1 - s)
			for _, a := range nbr[i] {
				p := sigmoid(f.Theta[i] - f.Theta[a.j])
				g[i] -= a.n * p
				w := a.n * p * (1 - p)
				H[i*n+i] += w
				H[i*n+a.j] -= w
			}
		}
		return H, g
	}
	var L []float64
	for step := 0; step < 50; step++ {
		H, g := hess()
		var ok bool
		L, ok = cholesky(H, n)
		if !ok {
			break
		}
		d := cholSolve(L, n, g)
		var m float64
		for i := range d {
			f.Theta[i] += d[i]
			m = math.Max(m, math.Abs(d[i]))
		}
		if m < 1e-10 {
			f.Converged = true
			break
		}
	}
	H, _ := hess()
	L, ok := cholesky(H, n)
	if !ok {
		f.Approx = true
		for i := 0; i < n; i++ {
			f.SE[i] = 1 / math.Sqrt(H[i*n+i])
		}
		return f
	}
	inv := lowerInverse(L, n)
	for i := 0; i < n; i++ {
		var s float64
		for k := i; k < n; k++ { // (L⁻¹) is lower triangular: rows k ≥ i
			v := inv[k*n+i]
			s += v * v
		}
		f.SE[i] = math.Sqrt(s)
	}
	return f
}

func anchorInfo(theta float64) float64 {
	s := sigmoid(theta)
	return 2 * s * (1 - s)
}

func sigmoid(x float64) float64 { return 1 / (1 + math.Exp(-x)) }

// cholesky factors the symmetric positive definite n×n matrix A = L·Lᵀ.
func cholesky(A []float64, n int) ([]float64, bool) {
	L := make([]float64, n*n)
	for j := 0; j < n; j++ {
		s := A[j*n+j]
		rowJ := L[j*n : j*n+j]
		for _, v := range rowJ {
			s -= v * v
		}
		if s <= 0 {
			return nil, false
		}
		d := math.Sqrt(s)
		L[j*n+j] = d
		for i := j + 1; i < n; i++ {
			s := A[i*n+j]
			rowI := L[i*n : i*n+j]
			for k, v := range rowJ {
				s -= rowI[k] * v
			}
			L[i*n+j] = s / d
		}
	}
	return L, true
}

// cholSolve solves L·Lᵀ·x = b.
func cholSolve(L []float64, n int, b []float64) []float64 {
	y := make([]float64, n)
	for i := 0; i < n; i++ {
		s := b[i]
		for k := 0; k < i; k++ {
			s -= L[i*n+k] * y[k]
		}
		y[i] = s / L[i*n+i]
	}
	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		s := y[i]
		for k := i + 1; k < n; k++ {
			s -= L[k*n+i] * x[k]
		}
		x[i] = s / L[i*n+i]
	}
	return x
}

// lowerInverse inverts a lower-triangular matrix.
func lowerInverse(L []float64, n int) []float64 {
	inv := make([]float64, n*n)
	for j := 0; j < n; j++ {
		inv[j*n+j] = 1 / L[j*n+j]
		for i := j + 1; i < n; i++ {
			var s float64
			for k := j; k < i; k++ {
				s += L[i*n+k] * inv[k*n+j]
			}
			inv[i*n+j] = -s / L[i*n+i]
		}
	}
	return inv
}

// DisplayScore maps θ to an Elo-like scale centred on 1000.
func DisplayScore(theta float64) float64 { return 1000 + 400*theta/math.Ln10 }
