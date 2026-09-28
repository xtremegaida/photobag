// Package similar finds visually similar images from stored thumbprints:
// exact k-nearest-neighbour search and similarity-based orderings.
package similar

import (
	"context"
	"encoding/json"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"

	"photobag/internal/bag"
	"photobag/internal/imaging"
)

// Item is an image with its thumbprint.
type Item struct {
	ID int64
	FP imaging.Fingerprint
}

// Neighbor is a nearby item (index into the item slice).
type Neighbor struct {
	Index int
	Sim   float64
}

// Load reads thumbprints for the given image ids, preserving their order.
// Images without a thumbprint are returned in missing.
func Load(ctx context.Context, b *bag.Bag, ids []int64) (items []Item, missing []int64, err error) {
	js, _ := json.Marshal(ids)
	rows, err := b.R.QueryContext(ctx, `SELECT i.id, f.phash, f.color, f.aspect, f.ac_energy, f.version
		FROM images i JOIN fingerprints f ON f.blob_id = i.blob_id
		WHERE i.id IN (SELECT value FROM json_each(?))`, string(js))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	byID := make(map[int64]imaging.Fingerprint, len(ids))
	for rows.Next() {
		var id, ph int64
		var color []byte
		var fp imaging.Fingerprint
		if err := rows.Scan(&id, &ph, &color, &fp.Aspect, &fp.ACEnergy, &fp.Version); err != nil {
			return nil, nil, err
		}
		fp.PHash = uint64(ph)
		copy(fp.Color[:], color)
		byID[id] = fp
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	items = make([]Item, 0, len(byID))
	for _, id := range ids {
		if fp, ok := byID[id]; ok {
			items = append(items, Item{ID: id, FP: fp})
		} else {
			missing = append(missing, id)
		}
	}
	return items, missing, nil
}

// KNN returns, for every item, its k most similar other items (by the full
// thumbprint similarity) in descending similarity. Candidates are the
// 4k nearest by pHash Hamming distance, found by exact brute force in
// parallel. progress (optional) receives the number of items finished.
func KNN(ctx context.Context, items []Item, k int, progress func(done int)) ([][]Neighbor, error) {
	n := len(items)
	out := make([][]Neighbor, n)
	if n < 2 || k <= 0 {
		return out, nil
	}
	k = min(k, n-1)
	pool := min(n-1, max(4*k, 16))
	hashes := make([]uint64, n)
	for i := range items {
		hashes[i] = items[i].FP.PHash
	}
	var next, done atomic.Int64
	var wg sync.WaitGroup
	workers := runtime.GOMAXPROCS(0)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cand := make([]cand, 0, pool+1)
			for {
				i := int(next.Add(1) - 1)
				if i >= n || ctx.Err() != nil {
					return
				}
				cand = nearestHamming(hashes, i, pool, cand[:0])
				nb := make([]Neighbor, len(cand))
				for c, cd := range cand {
					nb[c] = Neighbor{Index: cd.j, Sim: imaging.Similarity(&items[i].FP, &items[cd.j].FP)}
				}
				sort.Slice(nb, func(a, b int) bool {
					if nb[a].Sim != nb[b].Sim {
						return nb[a].Sim > nb[b].Sim
					}
					return nb[a].Index < nb[b].Index
				})
				out[i] = nb[:k]
				if d := done.Add(1); progress != nil && d%256 == 0 {
					progress(int(d))
				}
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if progress != nil {
		progress(n)
	}
	return out, nil
}

type cand struct {
	ham int
	j   int
}

// nearestHamming keeps the m smallest Hamming distances to hashes[i] in a
// sorted slice (m is small, so insertion is cheap and rare after warm-up).
func nearestHamming(hashes []uint64, i, m int, buf []cand) []cand {
	h := hashes[i]
	worst := 65
	for j, hj := range hashes {
		if j == i {
			continue
		}
		d := imaging.Hamming(h, hj)
		if len(buf) == m && d >= worst {
			continue
		}
		pos := sort.Search(len(buf), func(p int) bool { return buf[p].ham > d })
		if len(buf) < m {
			buf = append(buf, cand{})
		}
		copy(buf[pos+1:], buf[pos:len(buf)-1])
		buf[pos] = cand{d, j}
		if len(buf) == m {
			worst = buf[m-1].ham
		}
	}
	return buf
}

// Chain orders items so that visually similar images sit next to each
// other: starting from the first item, repeatedly step to the most similar
// unvisited neighbour; when a chain ends, continue from the next unvisited
// item in input order. It returns indexes into items.
func Chain(knn [][]Neighbor) []int {
	n := len(knn)
	visited := make([]bool, n)
	order := make([]int, 0, n)
	for start := 0; start < n; start++ {
		cur := start
		for cur >= 0 && !visited[cur] {
			visited[cur] = true
			order = append(order, cur)
			next := -1
			for _, nb := range knn[cur] {
				if !visited[nb.Index] {
					next = nb.Index
					break
				}
			}
			cur = next
		}
	}
	return order
}

// ByTarget orders items by descending similarity to target (stable).
func ByTarget(items []Item, target imaging.Fingerprint) []int {
	sims := make([]float64, len(items))
	idx := make([]int, len(items))
	for i := range items {
		idx[i] = i
		sims[i] = imaging.Similarity(&target, &items[i].FP)
	}
	sort.SliceStable(idx, func(a, b int) bool { return sims[idx[a]] > sims[idx[b]] })
	return idx
}

// Order reorders ids by visual similarity: chained (target == 0) or by
// similarity to the target image. Ids without thumbprints go last.
func Order(ctx context.Context, b *bag.Bag, ids []int64, target int64) ([]int64, error) {
	items, missing, err := Load(ctx, b, ids)
	if err != nil {
		return nil, err
	}
	var idx []int
	if target != 0 {
		t, _, err := Load(ctx, b, []int64{target})
		if err != nil {
			return nil, err
		}
		if len(t) == 0 {
			return ids, nil
		}
		idx = ByTarget(items, t[0].FP)
	} else {
		knn, err := KNN(ctx, items, 8, nil)
		if err != nil {
			return nil, err
		}
		idx = Chain(knn)
	}
	out := make([]int64, 0, len(ids))
	for _, i := range idx {
		out = append(out, items[i].ID)
	}
	return append(out, missing...), nil
}
