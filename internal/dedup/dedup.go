// Package dedup finds duplicate images (bit-identical or visually similar),
// groups them into clusters with a proposed keeper, and resolves clusters by
// trashing duplicates into their keeper.
package dedup

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"photobag/internal/bag"
	"photobag/internal/imaging"
	"photobag/internal/library"
	"photobag/internal/query"
	"photobag/internal/similar"
)

// Modes.
const (
	ModeExact   = "exact"
	ModeSimilar = "similar"
)

// Defaults for similar scans.
const (
	DefaultNeighbors = 8
	DefaultFloor     = 0.5
	DefaultThreshold = 0.9
)

// Params configure a scan.
type Params struct {
	Mode      string           `json:"mode"`
	Neighbors int              `json:"neighbors,omitempty"`
	Floor     float64          `json:"floor,omitempty"`
	Scope     query.ImageQuery `json:"scope"`
}

// Pair is a candidate duplicate pair (A < B).
type Pair struct {
	A   int64   `json:"a"`
	B   int64   `json:"b"`
	Sim float64 `json:"sim"`
}

// member holds the keeper-ranking facts about an image.
type member struct {
	pixels      int64
	size        int64
	tags        int
	comparisons int
	importedAt  int64
}

// Scan is the result of a dedup scan. Clusters are derived from it for any
// threshold, and it tracks resolutions so resolved groups disappear.
type Scan struct {
	ID        string    `json:"id"`
	Params    Params    `json:"params"`
	CreatedAt time.Time `json:"createdAt"`
	Scanned   int       `json:"scanned"`
	PairCount int       `json:"pairCount"`
	Millis    int64     `json:"millis"`

	pairs   []Pair
	members map[int64]*member
	fps     map[int64]imaging.Fingerprint

	mu        sync.Mutex
	removed   map[int64]bool
	dismissed map[[2]int64]bool
}

// Cluster is a group of probable duplicates.
type Cluster struct {
	// Members lists image ids, keeper first, then by similarity to it.
	Members []int64 `json:"members"`
	Keeper  int64   `json:"keeper"`
	// KeeperSim is each member's similarity to the keeper (aligned with Members).
	KeeperSim []float64 `json:"keeperSim"`
	// Proposed are members pre-marked for deletion: similar enough to the
	// keeper itself, not merely chained through other members.
	Proposed []int64 `json:"proposed"`
	MaxSim   float64 `json:"maxSim"`
	Pairs    []Pair  `json:"pairs"`
}

// NewScan runs a scan. progress (optional) receives (done, total).
func NewScan(ctx context.Context, b *bag.Bag, p Params, progress func(done, total int)) (*Scan, error) {
	start := time.Now()
	if progress == nil {
		progress = func(int, int) {}
	}
	if p.Mode == "" {
		p.Mode = ModeSimilar
	}
	if p.Neighbors <= 0 {
		p.Neighbors = DefaultNeighbors
	}
	if p.Floor <= 0 {
		p.Floor = DefaultFloor
	}
	p.Scope.Scope = query.Active
	ids, err := library.ListIDs(ctx, b, p.Scope, query.Sort{Field: query.SortImported})
	if err != nil {
		return nil, err
	}
	s := &Scan{
		ID: newID(), Params: p, CreatedAt: time.Now(), Scanned: len(ids),
		fps: map[int64]imaging.Fingerprint{}, removed: map[int64]bool{}, dismissed: map[[2]int64]bool{},
	}
	switch p.Mode {
	case ModeExact:
		if err := s.scanExact(ctx, b, ids); err != nil {
			return nil, err
		}
	case ModeSimilar:
		if err := s.scanSimilar(ctx, b, ids, progress); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown dedup mode %q", p.Mode)
	}
	if err := s.loadMembers(ctx, b); err != nil {
		return nil, err
	}
	s.PairCount = len(s.pairs)
	s.Millis = time.Since(start).Milliseconds()
	progress(len(ids), len(ids))
	return s, nil
}

func newID() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Scan) scanExact(ctx context.Context, b *bag.Bag, ids []int64) error {
	js, _ := json.Marshal(ids)
	rows, err := b.R.QueryContext(ctx, `SELECT id, sha256 FROM images
		WHERE id IN (SELECT value FROM json_each(?)) ORDER BY id`, string(js))
	if err != nil {
		return err
	}
	groups := map[string][]int64{}
	for rows.Next() {
		var id int64
		var sha []byte
		if err := rows.Scan(&id, &sha); err != nil {
			rows.Close()
			return err
		}
		groups[string(sha)] = append(groups[string(sha)], id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, g := range groups {
		for i := 1; i < len(g); i++ {
			s.pairs = append(s.pairs, Pair{A: g[0], B: g[i], Sim: 1})
		}
	}
	return nil
}

func (s *Scan) scanSimilar(ctx context.Context, b *bag.Bag, ids []int64, progress func(int, int)) error {
	items, _, err := similar.Load(ctx, b, ids)
	if err != nil {
		return err
	}
	for _, it := range items {
		s.fps[it.ID] = it.FP
	}
	knn, err := similar.KNN(ctx, items, s.Params.Neighbors, func(d int) { progress(d, len(items)) })
	if err != nil {
		return err
	}
	seen := map[[2]int64]bool{}
	for i, nbs := range knn {
		for _, nb := range nbs {
			if nb.Sim < s.Params.Floor {
				continue
			}
			a, c := items[i].ID, items[nb.Index].ID
			if a > c {
				a, c = c, a
			}
			k := [2]int64{a, c}
			if !seen[k] {
				seen[k] = true
				s.pairs = append(s.pairs, Pair{A: a, B: c, Sim: nb.Sim})
			}
		}
	}
	// Drop pairs the user already declared distinct.
	nd, err := loadNotDuplicates(ctx, b)
	if err != nil {
		return err
	}
	kept := s.pairs[:0]
	for _, p := range s.pairs {
		if !nd[[2]int64{p.A, p.B}] {
			kept = append(kept, p)
		}
	}
	s.pairs = kept
	return nil
}

func loadNotDuplicates(ctx context.Context, b *bag.Bag) (map[[2]int64]bool, error) {
	rows, err := b.R.QueryContext(ctx, "SELECT a, b FROM not_duplicates")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[[2]int64]bool{}
	for rows.Next() {
		var a, c int64
		if err := rows.Scan(&a, &c); err != nil {
			return nil, err
		}
		m[[2]int64{a, c}] = true
	}
	return m, rows.Err()
}

func (s *Scan) loadMembers(ctx context.Context, b *bag.Bag) error {
	s.members = map[int64]*member{}
	var ids []int64
	for _, p := range s.pairs {
		for _, id := range []int64{p.A, p.B} {
			if s.members[id] == nil {
				s.members[id] = &member{}
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	js, _ := json.Marshal(ids)
	rows, err := b.R.QueryContext(ctx, `SELECT i.id, i.width * i.height, i.size, i.imported_at,
		(SELECT count(*) FROM image_tags it WHERE it.image_id = i.id),
		(SELECT count(*) FROM comparisons c WHERE c.left_id = i.id OR c.right_id = i.id)
		FROM images i WHERE i.id IN (SELECT value FROM json_each(?))`, string(js))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var m member
		if err := rows.Scan(&id, &m.pixels, &m.size, &m.importedAt, &m.tags, &m.comparisons); err != nil {
			return err
		}
		*s.members[id] = m
	}
	return rows.Err()
}

// better reports whether a should be kept over c: more pixels, then larger
// file, then more tags and comparisons, then earlier import.
func (s *Scan) better(a, c int64) bool {
	ma, mc := s.members[a], s.members[c]
	if ma == nil || mc == nil {
		return a < c
	}
	switch {
	case ma.pixels != mc.pixels:
		return ma.pixels > mc.pixels
	case ma.size != mc.size:
		return ma.size > mc.size
	case ma.tags+ma.comparisons != mc.tags+mc.comparisons:
		return ma.tags+ma.comparisons > mc.tags+mc.comparisons
	case ma.importedAt != mc.importedAt:
		return ma.importedAt < mc.importedAt
	}
	return a < c
}

func (s *Scan) sim(a, c int64) float64 {
	if s.Params.Mode == ModeExact {
		return 1
	}
	fa, oka := s.fps[a]
	fc, okc := s.fps[c]
	if !oka || !okc {
		return 0
	}
	return imaging.Similarity(&fa, &fc)
}

// Clusters groups the scan's pairs at the given similarity threshold,
// excluding resolved images and dismissed pairs.
func (s *Scan) Clusters(threshold float64) []Cluster {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent := map[int64]int64{}
	var find func(int64) int64
	find = func(x int64) int64 {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	var live []Pair
	for _, p := range s.pairs {
		if p.Sim < threshold || s.removed[p.A] || s.removed[p.B] || s.dismissed[[2]int64{p.A, p.B}] {
			continue
		}
		live = append(live, p)
		for _, id := range []int64{p.A, p.B} {
			if _, ok := parent[id]; !ok {
				parent[id] = id
			}
		}
		ra, rb := find(p.A), find(p.B)
		if ra != rb {
			parent[ra] = rb
		}
	}
	groups := map[int64][]int64{}
	for id := range parent {
		r := find(id)
		groups[r] = append(groups[r], id)
	}
	pairsBy := map[int64][]Pair{}
	for _, p := range live {
		r := find(p.A)
		pairsBy[r] = append(pairsBy[r], p)
	}
	out := make([]Cluster, 0, len(groups))
	for r, ids := range groups {
		keeper := ids[0]
		for _, id := range ids[1:] {
			if s.better(id, keeper) {
				keeper = id
			}
		}
		c := Cluster{Keeper: keeper, Pairs: pairsBy[r], Proposed: []int64{}}
		for _, p := range c.Pairs {
			c.MaxSim = max(c.MaxSim, p.Sim)
		}
		type ms struct {
			id  int64
			sim float64
		}
		var rest []ms
		for _, id := range ids {
			if id != keeper {
				rest = append(rest, ms{id, s.sim(keeper, id)})
			}
		}
		sort.Slice(rest, func(i, j int) bool {
			if rest[i].sim != rest[j].sim {
				return rest[i].sim > rest[j].sim
			}
			return rest[i].id < rest[j].id
		})
		c.Members = append(c.Members, keeper)
		c.KeeperSim = append(c.KeeperSim, 1)
		for _, m := range rest {
			c.Members = append(c.Members, m.id)
			c.KeeperSim = append(c.KeeperSim, m.sim)
			if m.sim >= threshold {
				c.Proposed = append(c.Proposed, m.id)
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].MaxSim != out[j].MaxSim {
			return out[i].MaxSim > out[j].MaxSim
		}
		if len(out[i].Members) != len(out[j].Members) {
			return len(out[i].Members) > len(out[j].Members)
		}
		return out[i].Keeper < out[j].Keeper
	})
	return out
}

// Resolution is the user's decision for one cluster.
type Resolution struct {
	// Keep is the image that survives; deleted images merge into it.
	Keep int64 `json:"keep"`
	// KeepAlso are members kept as distinct images: every pair among
	// Keep and KeepAlso is recorded as "not duplicates".
	KeepAlso []int64 `json:"keepAlso,omitempty"`
	// Delete are moved to the trash and merged into Keep (their tags are
	// copied to Keep; their comparisons count for Keep in scoring).
	Delete []int64 `json:"delete,omitempty"`
}

// Resolve applies resolutions and returns how many images were trashed.
func Resolve(ctx context.Context, b *bag.Bag, res []Resolution) (int, error) {
	trashed := 0
	now := bag.NowMillis()
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		for _, r := range res {
			var active int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM images WHERE id = ? AND deleted_at IS NULL AND purged_at IS NULL",
				r.Keep).Scan(&active); err != nil {
				return err
			}
			if active == 0 {
				return fmt.Errorf("image %d to keep is not active", r.Keep)
			}
			for _, d := range r.Delete {
				if d == r.Keep {
					return fmt.Errorf("image %d cannot be both kept and deleted", d)
				}
				resu, err := tx.ExecContext(ctx, `UPDATE images SET deleted_at = ?, merged_into = ?
					WHERE id = ? AND deleted_at IS NULL AND purged_at IS NULL`, now, r.Keep, d)
				if err != nil {
					return err
				}
				n, _ := resu.RowsAffected()
				trashed += int(n)
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO image_tags(image_id, tag_id)
					SELECT ?, tag_id FROM image_tags WHERE image_id = ?`, r.Keep, d); err != nil {
					return err
				}
			}
			kept := append([]int64{r.Keep}, r.KeepAlso...)
			for i := 0; i < len(kept); i++ {
				for j := i + 1; j < len(kept); j++ {
					a, c := min(kept[i], kept[j]), max(kept[i], kept[j])
					if a == c {
						continue
					}
					if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO not_duplicates(a, b, created_at) VALUES (?, ?, ?)",
						a, c, now); err != nil {
						return err
					}
				}
			}
		}
		return library.MarkAllMetricsDirty(ctx, tx)
	})
	return trashed, err
}

// MarkResolved updates the scan after Resolve succeeded.
func (s *Scan) MarkResolved(res []Resolution) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range res {
		for _, d := range r.Delete {
			s.removed[d] = true
		}
		kept := append([]int64{r.Keep}, r.KeepAlso...)
		for i := 0; i < len(kept); i++ {
			for j := i + 1; j < len(kept); j++ {
				s.dismissed[[2]int64{min(kept[i], kept[j]), max(kept[i], kept[j])}] = true
			}
		}
	}
}

// Restored tells the scan that images came back from the trash.
func (s *Scan) Restored(ids []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		delete(s.removed, id)
	}
}

// Proposals turns clusters into their default resolutions (keep the
// keeper, delete the proposed members).
func Proposals(clusters []Cluster) []Resolution {
	var out []Resolution
	for _, c := range clusters {
		if len(c.Proposed) > 0 {
			out = append(out, Resolution{Keep: c.Keeper, Delete: c.Proposed})
		}
	}
	return out
}

// Store keeps recent scans in memory for the UI.
type Store struct {
	mu    sync.Mutex
	scans map[string]*Scan
	order []string
}

// NewStore creates an empty store.
func NewStore() *Store { return &Store{scans: map[string]*Scan{}} }

// Put adds a scan, evicting the oldest beyond four.
func (st *Store) Put(s *Scan) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.scans[s.ID] = s
	st.order = append(st.order, s.ID)
	for len(st.order) > 4 {
		delete(st.scans, st.order[0])
		st.order = st.order[1:]
	}
}

// Get returns a scan by id.
func (st *Store) Get(id string) *Scan {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.scans[id]
}

// All returns the stored scans, newest first.
func (st *Store) All() []*Scan {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]*Scan, 0, len(st.order))
	for i := len(st.order) - 1; i >= 0; i-- {
		out = append(out, st.scans[st.order[i]])
	}
	return out
}
