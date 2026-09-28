package scoring

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"path/filepath"
	"sort"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/dedup"
	"photobag/internal/importer"
	"photobag/internal/library"
	"photobag/internal/query"
	"photobag/internal/testimg"
)

func TestPermIsBijection(t *testing.T) {
	for _, m := range []uint64{1, 2, 3, 5, 10, 17, 100, 1000, 4097, 50000} {
		for _, seed := range []uint64{0, 1, 99} {
			p := NewPerm(m, seed)
			seen := make([]bool, m)
			for i := uint64(0); i < m; i++ {
				v := p.At(i)
				if v >= m || seen[v] {
					t.Fatalf("m=%d seed=%d: At(%d)=%d repeats or out of range", m, seed, i, v)
				}
				seen[v] = true
			}
		}
	}
}

func TestPairIndexRoundTrip(t *testing.T) {
	for k := uint64(0); k < 200000; k++ {
		i, j := PairAt(k)
		if i < 0 || i >= j || PairIndex(i, j) != k {
			t.Fatalf("k=%d -> (%d,%d)", k, i, j)
		}
	}
	for _, n := range []int{1_000_000, 3_000_000} {
		m := NumPairs(n)
		for _, k := range []uint64{m - 1, m - 2, m / 2, m / 3} {
			i, j := PairAt(k)
			if j >= n || PairIndex(i, j) != k {
				t.Fatalf("n=%d k=%d -> (%d,%d)", n, k, i, j)
			}
		}
	}
	// All pairs of a run appear exactly once, in both orientations randomly.
	n := 9
	seen := map[[2]int]bool{}
	swaps := 0
	for pos := uint64(0); pos < NumPairs(n); pos++ {
		l, r := PairForPos(42, n, pos)
		if l > r {
			swaps++
		}
		k := [2]int{min(l, r), max(l, r)}
		if seen[k] || l == r {
			t.Fatalf("pair %v repeated", k)
		}
		seen[k] = true
	}
	if len(seen) != 36 || swaps == 0 || swaps == 36 {
		t.Errorf("pairs=%d swaps=%d", len(seen), swaps)
	}
}

func kendallTau(a, b []float64) float64 {
	var c, d float64
	for i := range a {
		for j := i + 1; j < len(a); j++ {
			s := (a[i] - a[j]) * (b[i] - b[j])
			if s > 0 {
				c++
			} else if s < 0 {
				d++
			}
		}
	}
	return (c - d) / (c + d)
}

func TestBTRecoversPlantedRanking(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	n := 30
	truth := make([]float64, n)
	for i := range truth {
		truth[i] = float64(i)*0.15 - 2
	}
	var edges []Edge
	for rep := 0; rep < 3; rep++ {
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if r.Float64() < sigmoid(truth[i]-truth[j]) {
					edges = append(edges, Edge{I: i, J: j, W: 1})
				} else {
					edges = append(edges, Edge{I: j, J: i, W: 1})
				}
			}
		}
	}
	f := FitBT(n, edges)
	if !f.Converged {
		t.Fatal("did not converge")
	}
	if tau := kendallTau(truth, f.Theta); tau < 0.8 {
		t.Errorf("Kendall tau %.3f", tau)
	}
}

func TestBTUndefeatedAndDisconnected(t *testing.T) {
	// 0 beats everyone many times; {3,4} is a separate group.
	edges := []Edge{{0, 1, 10}, {0, 2, 10}, {1, 2, 1}, {3, 4, 2}}
	f := FitBT(5, edges)
	for i, th := range f.Theta {
		if math.IsNaN(th) || math.IsInf(th, 0) || math.Abs(th) > 20 {
			t.Fatalf("theta[%d] = %v", i, th)
		}
	}
	if !(f.Theta[0] > f.Theta[1] && f.Theta[1] > f.Theta[2] && f.Theta[3] > f.Theta[4]) {
		t.Errorf("order wrong: %v", f.Theta)
	}
	// The anchor keeps groups on one scale around 0.
	if math.Abs(f.Theta[3]+f.Theta[4]) > 1e-6 {
		t.Errorf("symmetric pair should be centred on 0: %v %v", f.Theta[3], f.Theta[4])
	}
}

// invert does Gauss-Jordan elimination (test reference).
func invert(A []float64, n int) []float64 {
	a := append([]float64{}, A...)
	inv := make([]float64, n*n)
	for i := 0; i < n; i++ {
		inv[i*n+i] = 1
	}
	for c := 0; c < n; c++ {
		p := a[c*n+c]
		for k := 0; k < n; k++ {
			a[c*n+k] /= p
			inv[c*n+k] /= p
		}
		for r := 0; r < n; r++ {
			if r == c {
				continue
			}
			f := a[r*n+c]
			for k := 0; k < n; k++ {
				a[r*n+k] -= f * a[c*n+k]
				inv[r*n+k] -= f * inv[c*n+k]
			}
		}
	}
	return inv
}

func TestBTStandardErrorsMatchInverseHessian(t *testing.T) {
	edges := []Edge{{0, 1, 3}, {1, 2, 2}, {2, 3, 1}, {0, 3, 4}, {3, 1, 1}}
	n := 4
	f := FitBT(n, edges)
	// Build the Hessian independently.
	H := make([]float64, n*n)
	for i := 0; i < n; i++ {
		s := sigmoid(f.Theta[i])
		H[i*n+i] = 2 * s * (1 - s)
	}
	for _, e := range edges {
		p := sigmoid(f.Theta[e.I] - f.Theta[e.J])
		w := e.W * p * (1 - p)
		H[e.I*n+e.I] += w
		H[e.J*n+e.J] += w
		H[e.I*n+e.J] -= w
		H[e.J*n+e.I] -= w
	}
	inv := invert(H, n)
	for i := 0; i < n; i++ {
		if want := math.Sqrt(inv[i*n+i]); math.Abs(want-f.SE[i]) > 1e-9 {
			t.Errorf("SE[%d] = %v, want %v", i, f.SE[i], want)
		}
		if f.SE[i] <= 1/math.Sqrt(H[i*n+i])-1e-12 {
			t.Errorf("SE[%d] must not be below the diagonal approximation", i)
		}
	}
	// Stationarity: the gradient vanishes at the optimum.
	for i := 0; i < n; i++ {
		g := 1 - 2*sigmoid(f.Theta[i])
		for _, e := range edges {
			p := sigmoid(f.Theta[e.I] - f.Theta[e.J])
			if e.I == i {
				g += e.W * (1 - p)
			}
			if e.J == i {
				g -= e.W * (1 - p)
			}
		}
		if math.Abs(g) > 1e-8 {
			t.Errorf("gradient[%d] = %v", i, g)
		}
	}
}

// --- service tests ---

func setup(t *testing.T) (*bag.Bag, *Service, map[string]int64) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "photos")
	if _, err := testimg.WriteTree(src, 0.1); err != nil {
		t.Fatal(err)
	}
	b, err := bag.Open(filepath.Join(dir, "s.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	ctx := context.Background()
	if _, err := importer.Run(ctx, b, src, importer.Options{Recursive: true, TagFolders: true}, nil); err != nil {
		t.Fatal(err)
	}
	ids, _ := library.ListIDs(ctx, b, query.ImageQuery{}, query.Sort{})
	ims, _ := library.GetImages(ctx, b, ids)
	byName := map[string]int64{}
	for _, im := range ims {
		byName[im.OriginalPath] = im.ID
	}
	return b, New(b), byName
}

// answerAll answers every remaining pair with pref(left, right) choosing.
func answerAll(t *testing.T, s *Service, runID int64, pref func(a, b int64) int64) int {
	ctx := context.Background()
	p, err := s.Next(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for !p.Done {
		if p, err = s.Answer(ctx, runID, p.Pos, pref(p.Left, p.Right)); err != nil {
			t.Fatal(err)
		}
		n++
	}
	return n
}

func TestRunLifecycleAndRankings(t *testing.T) {
	_, s, byName := setup(t)
	ctx := context.Background()
	m, err := s.EnsureMetric(ctx, "Composition", "")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := s.EnsureMetric(ctx, "composition", ""); again.ID != m.ID {
		t.Error("EnsureMetric should be case-insensitive")
	}
	party := []string{"2020/Party/party_01.jpg", "2020/Party/party_02.jpg", "2020/Party/party_03.jpg",
		"2020/Party/party_04.jpg", "2020/Party/party_05.jpg", "2020/Party/party_06.jpg"}
	var ids []int64
	rank := map[int64]int{}
	for i, p := range party {
		ids = append(ids, byName[p])
		rank[byName[p]] = i
	}
	run, err := s.CreateRun(ctx, m.ID, query.ImageQuery{IDs: ids}, 7)
	if err != nil {
		t.Fatal(err)
	}
	if run.TotalPairs != 15 || run.ImageCount != 6 {
		t.Fatalf("run %+v", run)
	}
	// Planted preference: lower party number is better.
	better := func(a, b int64) int64 {
		if rank[a] < rank[b] {
			return a
		}
		return b
	}

	// Answer, skip, undo, conflict.
	p, _ := s.Next(ctx, run.ID)
	p2, err := s.Answer(ctx, run.ID, p.Pos, 0) // skip
	if err != nil || p2.Pos != 1 {
		t.Fatalf("skip: %v %+v", err, p2)
	}
	if _, err := s.Answer(ctx, run.ID, 0, p.Left); !errors.Is(err, ErrConflict) {
		t.Errorf("stale answer: want ErrConflict, got %v", err)
	}
	u, err := s.Undo(ctx, run.ID)
	if err != nil || u.Pos != 0 || u.Left != p.Left {
		t.Fatalf("undo: %v %+v", err, u)
	}
	if _, err := s.Answer(ctx, run.ID, 0, 999999); err == nil {
		t.Error("winner outside the pair must be rejected")
	}
	if st, _ := s.SetStatus(ctx, run.ID, StatusStopped); st.Status != StatusStopped {
		t.Errorf("stop: %+v", st)
	}
	if n := answerAll(t, s, run.ID, better); n != 15 {
		t.Errorf("answered %d", n)
	}
	got, _ := s.GetRun(ctx, run.ID)
	if got.Status != StatusComplete || got.Compared != 15 {
		t.Errorf("run after completion %+v", got)
	}

	ranks, metric, err := s.Rankings(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if metric.Dirty || len(ranks) != 6 {
		t.Fatalf("rankings %d dirty=%v", len(ranks), metric.Dirty)
	}
	for i, r := range ranks {
		if rank[r.ImageID] != i {
			t.Errorf("rank %d is %s", i+1, party[rank[r.ImageID]])
		}
	}
	if ranks[0].Wins != 5 || ranks[0].Losses != 0 || ranks[5].Score >= ranks[0].Score {
		t.Errorf("top %+v bottom %+v", ranks[0], ranks[5])
	}

	// A second run with the opposite preference cancels every pair.
	run2, _ := s.CreateRun(ctx, m.ID, query.ImageQuery{IDs: ids}, 8)
	answerAll(t, s, run2.ID, func(a, b int64) int64 {
		if better(a, b) == a {
			return b
		}
		return a
	})
	ranks, metric, _ = s.Rankings(ctx, m.ID)
	if metric.CancelledPairs != 15 || metric.Comparisons != 30 {
		t.Errorf("cancelled=%d comparisons=%d", metric.CancelledPairs, metric.Comparisons)
	}
	for _, r := range ranks {
		if math.Abs(r.Score-1000) > 1e-6 || r.Wins != 0 || r.RawWins+r.RawLosses != 10 {
			t.Errorf("fully cancelled image should sit at 1000 with no net wins: %+v", r)
		}
	}
}

func TestMergedDuplicatesCountForKeeper(t *testing.T) {
	b, s, byName := setup(t)
	ctx := context.Background()
	m, _ := s.EnsureMetric(ctx, "Sharpness", "")
	edit, orig := byName["2020/Party/party_03_edit.jpg"], byName["2020/Party/party_03.jpg"]
	others := []int64{byName["2020/Party/party_01.jpg"], byName["2020/Party/party_02.jpg"]}
	run, _ := s.CreateRun(ctx, m.ID, query.ImageQuery{IDs: append([]int64{edit}, others...)}, 3)
	// The edit wins everything.
	answerAll(t, s, run.ID, func(a, c int64) int64 {
		if a == edit || c == edit {
			return edit
		}
		return min(a, c)
	})
	if _, err := dedup.Resolve(ctx, b, []dedup.Resolution{{Keep: orig, Delete: []int64{edit}}}); err != nil {
		t.Fatal(err)
	}
	ranks, _, err := s.Rankings(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranks) != 3 || ranks[0].ImageID != orig || ranks[0].Wins != 2 {
		t.Fatalf("keeper should inherit the duplicate's comparisons: %+v", ranks)
	}
	// The run itself is untouched: restoring gives the edit its record back.
	if _, err := library.Restore(ctx, b, []int64{edit}); err != nil {
		t.Fatal(err)
	}
	ranks, _, _ = s.Rankings(ctx, m.ID)
	ids := []int64{}
	for _, r := range ranks {
		ids = append(ids, r.ImageID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if ranks[0].ImageID != edit {
		t.Errorf("after restore the edit should lead again: %+v", ranks)
	}
}
