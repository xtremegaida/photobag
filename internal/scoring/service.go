package scoring

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"

	"photobag/internal/bag"
	"photobag/internal/library"
	"photobag/internal/query"
)

// Errors.
var (
	ErrNotFound = errors.New("not found")
	// ErrConflict means the answer was for a position that is no longer
	// current (another tab answered first, or an undo happened).
	ErrConflict = errors.New("position is no longer current")
)

// Run statuses.
const (
	StatusActive   = "active"
	StatusStopped  = "stopped"
	StatusComplete = "complete"
)

// Service implements metrics, runs and scores on one bag.
type Service struct {
	b         *bag.Bag
	runImages sync.Map // run id -> []int64 (frozen at creation)
	recompute sync.Mutex
}

// New creates the service.
func New(b *bag.Bag) *Service { return &Service{b: b} }

// Metric is a named scoring criterion.
type Metric struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	CreatedAt      int64  `json:"createdAt"`
	Runs           int    `json:"runs"`
	Comparisons    int    `json:"comparisons"`
	Ranked         int    `json:"ranked"`
	Dirty          bool   `json:"dirty"`
	ScoredAt       int64  `json:"scoredAt,omitempty"`
	PairCount      int    `json:"pairCount"`
	CancelledPairs int    `json:"cancelledPairs"`
}

const metricCols = `m.id, m.name, m.description, m.created_at,
	(SELECT count(*) FROM score_runs r WHERE r.metric_id = m.id),
	(SELECT count(*) FROM comparisons c WHERE c.metric_id = m.id AND c.winner IS NOT NULL),
	(SELECT count(*) FROM scores s WHERE s.metric_id = m.id),
	m.scores_dirty > 0, COALESCE(m.scored_at, 0), m.pair_count, m.cancelled_pairs`

func scanMetric(sc interface{ Scan(...any) error }) (Metric, error) {
	var m Metric
	err := sc.Scan(&m.ID, &m.Name, &m.Description, &m.CreatedAt, &m.Runs, &m.Comparisons, &m.Ranked,
		&m.Dirty, &m.ScoredAt, &m.PairCount, &m.CancelledPairs)
	return m, err
}

// Metrics lists all metrics.
func (s *Service) Metrics(ctx context.Context) ([]Metric, error) {
	rows, err := s.b.R.QueryContext(ctx, "SELECT "+metricCols+" FROM metrics m ORDER BY m.name COLLATE NOCASE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Metric{}
	for rows.Next() {
		m, err := scanMetric(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMetric returns one metric.
func (s *Service) GetMetric(ctx context.Context, id int64) (*Metric, error) {
	m, err := scanMetric(s.b.R.QueryRowContext(ctx, "SELECT "+metricCols+" FROM metrics m WHERE m.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &m, err
}

// EnsureMetric returns the metric with this name, creating it if needed.
func (s *Service) EnsureMetric(ctx context.Context, name, description string) (*Metric, error) {
	if err := bag.ValidateLabel(name); err != nil {
		return nil, err
	}
	var id int64
	err := s.b.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO metrics(name, key, description, created_at, scores_dirty)
			VALUES (?, ?, ?, ?, 0) ON CONFLICT(key) DO NOTHING`,
			bag.CleanLabel(name), bag.LabelKey(name), description, bag.NowMillis()); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, "SELECT id FROM metrics WHERE key = ?", bag.LabelKey(name)).Scan(&id)
	})
	if err != nil {
		return nil, err
	}
	return s.GetMetric(ctx, id)
}

// UpdateMetric renames or re-describes a metric.
func (s *Service) UpdateMetric(ctx context.Context, id int64, name, description string) error {
	if err := bag.ValidateLabel(name); err != nil {
		return err
	}
	return s.b.Tx(ctx, func(tx *sql.Tx) error {
		var other int64
		err := tx.QueryRowContext(ctx, "SELECT id FROM metrics WHERE key = ?", bag.LabelKey(name)).Scan(&other)
		if err == nil && other != id {
			return fmt.Errorf("a metric named %q already exists", bag.CleanLabel(name))
		}
		res, err := tx.ExecContext(ctx, "UPDATE metrics SET name = ?, key = ?, description = ? WHERE id = ?",
			bag.CleanLabel(name), bag.LabelKey(name), description, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// DeleteMetric deletes a metric with all its runs, comparisons and scores.
func (s *Service) DeleteMetric(ctx context.Context, id int64) error {
	return s.b.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM metrics WHERE id = ?", id)
		return err
	})
}

// Run is a comparison run.
type Run struct {
	ID          int64            `json:"id"`
	MetricID    int64            `json:"metricId"`
	MetricName  string           `json:"metricName"`
	Seed        int64            `json:"seed"`
	Position    int64            `json:"position"`
	TotalPairs  int64            `json:"totalPairs"`
	ImageCount  int              `json:"imageCount"`
	Status      string           `json:"status"`
	Selection   query.ImageQuery `json:"selection"`
	CreatedAt   int64            `json:"createdAt"`
	UpdatedAt   int64            `json:"updatedAt"`
	FinishedAt  int64            `json:"finishedAt,omitempty"`
	Compared    int              `json:"compared"`
	Skipped     int              `json:"skipped"`
	Description string           `json:"description"`
}

const runCols = `r.id, r.metric_id, m.name, r.seed, r.position, r.total_pairs, r.image_count, r.status,
	r.selection_json, r.created_at, r.updated_at, COALESCE(r.finished_at, 0),
	(SELECT count(*) FROM comparisons c WHERE c.run_id = r.id AND c.winner IS NOT NULL),
	(SELECT count(*) FROM comparisons c WHERE c.run_id = r.id AND c.winner IS NULL)`

func scanRun(sc interface{ Scan(...any) error }) (Run, error) {
	var r Run
	var sel string
	err := sc.Scan(&r.ID, &r.MetricID, &r.MetricName, &r.Seed, &r.Position, &r.TotalPairs, &r.ImageCount, &r.Status,
		&sel, &r.CreatedAt, &r.UpdatedAt, &r.FinishedAt, &r.Compared, &r.Skipped)
	if err == nil {
		_ = json.Unmarshal([]byte(sel), &r.Selection)
		r.Description = r.Selection.Describe()
	}
	return r, err
}

// Runs lists runs, optionally for one metric, newest first.
func (s *Service) Runs(ctx context.Context, metricID int64) ([]Run, error) {
	q := "SELECT " + runCols + " FROM score_runs r JOIN metrics m ON m.id = r.metric_id"
	var args []any
	if metricID != 0 {
		q += " WHERE r.metric_id = ?"
		args = append(args, metricID)
	}
	rows, err := s.b.R.QueryContext(ctx, q+" ORDER BY r.id DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRun returns one run.
func (s *Service) GetRun(ctx context.Context, id int64) (*Run, error) {
	r, err := scanRun(s.b.R.QueryRowContext(ctx, "SELECT "+runCols+
		" FROM score_runs r JOIN metrics m ON m.id = r.metric_id WHERE r.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

// MaxRunImages bounds a run's input set.
const MaxRunImages = 100_000

// CreateRun starts a run over the images selected by q. seed 0 picks a
// random seed.
func (s *Service) CreateRun(ctx context.Context, metricID int64, q query.ImageQuery, seed int64) (*Run, error) {
	if _, err := s.GetMetric(ctx, metricID); err != nil {
		return nil, err
	}
	q.Scope = query.Active
	ids, err := library.ListIDs(ctx, s.b, q, query.Sort{Field: query.SortImported})
	if err != nil {
		return nil, err
	}
	if len(ids) < 2 {
		return nil, fmt.Errorf("a run needs at least two images (the selection has %d)", len(ids))
	}
	if len(ids) > MaxRunImages {
		return nil, fmt.Errorf("a run is limited to %d images (the selection has %d)", MaxRunImages, len(ids))
	}
	for seed == 0 {
		var b [8]byte
		rand.Read(b[:])
		seed = int64(binary.LittleEndian.Uint64(b[:]) >> 1)
	}
	sel, _ := json.Marshal(q)
	idsJSON, _ := json.Marshal(ids)
	now := bag.NowMillis()
	var runID int64
	err = s.b.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO score_runs(metric_id, seed, total_pairs, image_count, selection_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, metricID, seed, NumPairs(len(ids)), len(ids), string(sel), now, now)
		if err != nil {
			return err
		}
		runID, _ = res.LastInsertId()
		_, err = tx.ExecContext(ctx, `INSERT INTO score_run_images(run_id, ord, image_id)
			SELECT ?, key, value FROM json_each(?)`, runID, string(idsJSON))
		return err
	})
	if err != nil {
		return nil, err
	}
	s.runImages.Store(runID, ids)
	return s.GetRun(ctx, runID)
}

// DeleteRun removes a run and its comparisons.
func (s *Service) DeleteRun(ctx context.Context, id int64) error {
	err := s.b.Tx(ctx, func(tx *sql.Tx) error {
		var metric int64
		if err := tx.QueryRowContext(ctx, "SELECT metric_id FROM score_runs WHERE id = ?", id).Scan(&metric); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM score_runs WHERE id = ?", id); err != nil {
			return err
		}
		return markDirty(ctx, tx, metric)
	})
	s.runImages.Delete(id)
	return err
}

func markDirty(ctx context.Context, tx *sql.Tx, metricID int64) error {
	_, err := tx.ExecContext(ctx, "UPDATE metrics SET scores_dirty = scores_dirty + 1 WHERE id = ?", metricID)
	return err
}

func (s *Service) images(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, runID int64) ([]int64, error) {
	if v, ok := s.runImages.Load(runID); ok {
		return v.([]int64), nil
	}
	rows, err := q.QueryContext(ctx, "SELECT image_id FROM score_run_images WHERE run_id = ? ORDER BY ord", runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.runImages.Store(runID, ids)
	return ids, nil
}

// Pair is the comparison currently asked for in a run.
type Pair struct {
	Run   Run   `json:"run"`
	Done  bool  `json:"done"`
	Pos   int64 `json:"pos"`
	Left  int64 `json:"left,omitempty"`
	Right int64 `json:"right,omitempty"`
	// Upcoming are the next few pairs, for prefetching their images.
	Upcoming []IDPair `json:"upcoming"`
}

// IDPair is a left/right pair of image ids.
type IDPair struct {
	Left  int64 `json:"left"`
	Right int64 `json:"right"`
}

// Next returns the run's current pair. Pairs involving images that have
// left the library since the run started are skipped automatically.
func (s *Service) Next(ctx context.Context, runID int64) (*Pair, error) {
	var out *Pair
	err := s.b.Tx(ctx, func(tx *sql.Tx) error {
		var seed, pos, total int64
		var status string
		err := tx.QueryRowContext(ctx, "SELECT seed, position, total_pairs, status FROM score_runs WHERE id = ?", runID).
			Scan(&seed, &pos, &total, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		ids, err := s.images(ctx, tx, runID)
		if err != nil {
			return err
		}
		perm := NewPerm(uint64(total), uint64(seed))
		start := pos
		out = &Pair{}
		for pos < total {
			i, j := PairAt(perm.At(uint64(pos)))
			if Swap(seed, uint64(pos)) {
				i, j = j, i
			}
			var active int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM images WHERE id IN (?, ?)
				AND deleted_at IS NULL AND purged_at IS NULL`, ids[i], ids[j]).Scan(&active); err != nil {
				return err
			}
			if active == 2 {
				out.Pos, out.Left, out.Right = pos, ids[i], ids[j]
				break
			}
			pos++
		}
		out.Upcoming = []IDPair{}
		for k := pos + 1; k < total && k <= pos+3; k++ {
			l, r := PairAt(perm.At(uint64(k)))
			if Swap(seed, uint64(k)) {
				l, r = r, l
			}
			out.Upcoming = append(out.Upcoming, IDPair{Left: ids[l], Right: ids[r]})
		}
		if pos != start || (pos >= total && status != StatusComplete) {
			st := status
			fin := any(nil)
			if pos >= total {
				st, fin = StatusComplete, bag.NowMillis()
			}
			if _, err := tx.ExecContext(ctx, `UPDATE score_runs SET position = ?, status = ?, finished_at = COALESCE(?, finished_at),
				updated_at = ? WHERE id = ?`, pos, st, fin, bag.NowMillis(), runID); err != nil {
				return err
			}
		}
		out.Done = pos >= total
		return nil
	})
	if err != nil {
		return nil, err
	}
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	out.Run = *run
	return out, nil
}

// Answer records the result for the pair at pos: winner is the left or
// right image id, or 0 to skip the pair. Answering a stopped run resumes it.
func (s *Service) Answer(ctx context.Context, runID, pos, winner int64) (*Pair, error) {
	err := s.b.Tx(ctx, func(tx *sql.Tx) error {
		var seed, cur, total, metric int64
		var status string
		err := tx.QueryRowContext(ctx, "SELECT seed, position, total_pairs, status, metric_id FROM score_runs WHERE id = ?", runID).
			Scan(&seed, &cur, &total, &status, &metric)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if pos != cur || pos >= total {
			return ErrConflict
		}
		ids, err := s.images(ctx, tx, runID)
		if err != nil {
			return err
		}
		l, r := PairForPos(seed, len(ids), uint64(pos))
		left, right := ids[l], ids[r]
		var w any
		switch winner {
		case 0:
		case left, right:
			w = winner
		default:
			return fmt.Errorf("image %d is not part of pair %d", winner, pos)
		}
		now := bag.NowMillis()
		if _, err := tx.ExecContext(ctx, `INSERT INTO comparisons(run_id, pos, metric_id, left_id, right_id, winner, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, runID, pos, metric, left, right, w, now); err != nil {
			return err
		}
		st, fin := StatusActive, any(nil)
		if pos+1 >= total {
			st, fin = StatusComplete, now
		}
		if _, err := tx.ExecContext(ctx, `UPDATE score_runs SET position = ?, status = ?, finished_at = ?, updated_at = ?
			WHERE id = ?`, pos+1, st, fin, now, runID); err != nil {
			return err
		}
		if w != nil {
			return markDirty(ctx, tx, metric)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Next(ctx, runID)
}

// Undo removes the run's most recent answer and makes its pair current.
func (s *Service) Undo(ctx context.Context, runID int64) (*Pair, error) {
	err := s.b.Tx(ctx, func(tx *sql.Tx) error {
		var metric int64
		if err := tx.QueryRowContext(ctx, "SELECT metric_id FROM score_runs WHERE id = ?", runID).Scan(&metric); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var pos int64
		var winner sql.NullInt64
		err := tx.QueryRowContext(ctx, "SELECT pos, winner FROM comparisons WHERE run_id = ? ORDER BY pos DESC LIMIT 1", runID).
			Scan(&pos, &winner)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("nothing to undo")
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM comparisons WHERE run_id = ? AND pos = ?", runID, pos); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE score_runs SET position = ?, status = ?, finished_at = NULL, updated_at = ?
			WHERE id = ?`, pos, StatusActive, bag.NowMillis(), runID); err != nil {
			return err
		}
		if winner.Valid {
			return markDirty(ctx, tx, metric)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Next(ctx, runID)
}

// SetStatus stops or resumes a run.
func (s *Service) SetStatus(ctx context.Context, runID int64, status string) (*Run, error) {
	if status != StatusActive && status != StatusStopped {
		return nil, fmt.Errorf("invalid status %q", status)
	}
	err := s.b.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE score_runs SET status = ?, updated_at = ?
			WHERE id = ? AND status != 'complete'`, status, bag.NowMillis(), runID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var exists int
			tx.QueryRowContext(ctx, "SELECT count(*) FROM score_runs WHERE id = ?", runID).Scan(&exists)
			if exists == 0 {
				return ErrNotFound
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetRun(ctx, runID)
}

// Summary describes the last score calculation of a metric.
type Summary struct {
	MetricID       int64 `json:"metricId"`
	Ranked         int   `json:"ranked"`
	Comparisons    int   `json:"comparisons"`
	Pairs          int   `json:"pairs"`
	CancelledPairs int   `json:"cancelledPairs"`
	Iterations     int   `json:"iterations"`
	Converged      bool  `json:"converged"`
	Approx         bool  `json:"approx"`
	ComputedAt     int64 `json:"computedAt"`
}

// Recompute recalculates a metric's scores from all its runs.
//
// Every non-skipped comparison of the metric is mapped through dedup merges
// (merged_into) to the surviving image; self-pairs and pairs with inactive
// images are dropped. Results are then netted per unordered pair so that
// contradicting results cancel one-for-one: net = wins(i over j) −
// wins(j over i). A positive net becomes an edge of that weight; a zero net
// (fully contradicted) is dropped and counted as a cancelled pair.
func (s *Service) Recompute(ctx context.Context, metricID int64) (*Summary, error) {
	s.recompute.Lock()
	defer s.recompute.Unlock()
	var dirty int64
	if err := s.b.R.QueryRowContext(ctx, "SELECT scores_dirty FROM metrics WHERE id = ?", metricID).Scan(&dirty); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	merged, active, err := s.lifecycle(ctx)
	if err != nil {
		return nil, err
	}
	resolve := func(id int64) int64 {
		for hops := 0; hops < 64; hops++ {
			to, ok := merged[id]
			if !ok {
				return id
			}
			id = to
		}
		return id
	}

	rows, err := s.b.R.QueryContext(ctx, "SELECT left_id, right_id, winner FROM comparisons WHERE metric_id = ? AND winner IS NOT NULL", metricID)
	if err != nil {
		return nil, err
	}
	type tally struct{ lo, hi int } // wins of the lower id, wins of the higher id
	pairs := map[[2]int64]*tally{}
	rawW, rawL := map[int64]int{}, map[int64]int{}
	sum := Summary{MetricID: metricID}
	for rows.Next() {
		var l, r, w int64
		if err := rows.Scan(&l, &r, &w); err != nil {
			rows.Close()
			return nil, err
		}
		l, r, w = resolve(l), resolve(r), resolve(w)
		if l == r || !active[l] || !active[r] {
			continue
		}
		loser := l
		if w == l {
			loser = r
		}
		rawW[w]++
		rawL[loser]++
		sum.Comparisons++
		k := [2]int64{min(l, r), max(l, r)}
		t := pairs[k]
		if t == nil {
			t = &tally{}
			pairs[k] = t
		}
		if w == k[0] {
			t.lo++
		} else {
			t.hi++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	index := map[int64]int{}
	var ids []int64
	idx := func(id int64) int {
		if i, ok := index[id]; ok {
			return i
		}
		index[id] = len(ids)
		ids = append(ids, id)
		return len(ids) - 1
	}
	for id := range rawW {
		idx(id)
	}
	for id := range rawL {
		idx(id)
	}
	var edges []Edge
	effW, effL := map[int64]int{}, map[int64]int{}
	for k, t := range pairs {
		sum.Pairs++
		net := t.lo - t.hi
		switch {
		case net > 0:
			edges = append(edges, Edge{I: idx(k[0]), J: idx(k[1]), W: float64(net)})
			effW[k[0]] += net
			effL[k[1]] += net
		case net < 0:
			edges = append(edges, Edge{I: idx(k[1]), J: idx(k[0]), W: float64(-net)})
			effW[k[1]] += -net
			effL[k[0]] += -net
		default:
			sum.CancelledPairs++
		}
	}
	fit := FitBT(len(ids), edges)
	sum.Ranked, sum.Iterations, sum.Converged, sum.Approx = len(ids), fit.Iterations, fit.Converged, fit.Approx
	sum.ComputedAt = bag.NowMillis()

	err = s.b.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM scores WHERE metric_id = ?", metricID); err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO scores(metric_id, image_id, theta, score, stderr, stderr_approx,
			n, wins, losses, raw_wins, raw_losses, computed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for i, id := range ids {
			se := fit.SE[i] * 400 / math.Ln10
			if _, err := stmt.ExecContext(ctx, metricID, id, fit.Theta[i], DisplayScore(fit.Theta[i]), se, fit.Approx,
				effW[id]+effL[id], effW[id], effL[id], rawW[id], rawL[id], sum.ComputedAt); err != nil {
				return err
			}
		}
		// Only clear the dirty counter if nothing changed while computing.
		_, err = tx.ExecContext(ctx, `UPDATE metrics SET scores_dirty = CASE WHEN scores_dirty = ? THEN 0 ELSE scores_dirty END,
			scored_at = ?, comparison_count = ?, pair_count = ?, cancelled_pairs = ? WHERE id = ?`,
			dirty, sum.ComputedAt, sum.Comparisons, sum.Pairs, sum.CancelledPairs, metricID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &sum, nil
}

func (s *Service) lifecycle(ctx context.Context) (merged map[int64]int64, active map[int64]bool, err error) {
	merged, active = map[int64]int64{}, map[int64]bool{}
	rows, err := s.b.R.QueryContext(ctx, `SELECT id, COALESCE(merged_into, 0), deleted_at IS NULL AND purged_at IS NULL
		FROM images WHERE merged_into IS NOT NULL OR (deleted_at IS NULL AND purged_at IS NULL)`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, into int64
		var act bool
		if err := rows.Scan(&id, &into, &act); err != nil {
			return nil, nil, err
		}
		if into != 0 {
			merged[id] = into
		}
		if act {
			active[id] = true
		}
	}
	return merged, active, rows.Err()
}

// Ranking is one image's place on a metric.
type Ranking struct {
	ImageID   int64   `json:"imageId"`
	Rank      int     `json:"rank"`
	Score     float64 `json:"score"`
	Stderr    float64 `json:"stderr"`
	Approx    bool    `json:"approx"`
	N         int     `json:"n"`
	Wins      int     `json:"wins"`
	Losses    int     `json:"losses"`
	RawWins   int     `json:"rawWins"`
	RawLosses int     `json:"rawLosses"`
}

// Rankings returns the metric's ranked images (recomputing stale scores),
// best first.
func (s *Service) Rankings(ctx context.Context, metricID int64) ([]Ranking, *Metric, error) {
	m, err := s.GetMetric(ctx, metricID)
	if err != nil {
		return nil, nil, err
	}
	if m.Dirty {
		if _, err := s.Recompute(ctx, metricID); err != nil {
			return nil, nil, err
		}
		if m, err = s.GetMetric(ctx, metricID); err != nil {
			return nil, nil, err
		}
	}
	rows, err := s.b.R.QueryContext(ctx, `SELECT s.image_id, s.score, s.stderr, s.stderr_approx, s.n, s.wins, s.losses,
		s.raw_wins, s.raw_losses FROM scores s JOIN images i ON i.id = s.image_id
		WHERE s.metric_id = ? AND i.deleted_at IS NULL AND i.purged_at IS NULL
		ORDER BY s.score DESC, s.image_id`, metricID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := []Ranking{}
	for rows.Next() {
		var r Ranking
		if err := rows.Scan(&r.ImageID, &r.Score, &r.Stderr, &r.Approx, &r.N, &r.Wins, &r.Losses, &r.RawWins, &r.RawLosses); err != nil {
			return nil, nil, err
		}
		r.Rank = len(out) + 1
		out = append(out, r)
	}
	return out, m, rows.Err()
}

// RefreshDirty recomputes every stale metric (used before score sorts).
func (s *Service) RefreshDirty(ctx context.Context, metricID int64) error {
	m, err := s.GetMetric(ctx, metricID)
	if err != nil {
		return err
	}
	if m.Dirty {
		_, err = s.Recompute(ctx, metricID)
	}
	return err
}
