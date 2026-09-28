package server

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"sync"

	"photobag/internal/exporter"
	"photobag/internal/imaging"
	"photobag/internal/library"
	"photobag/internal/query"
	"photobag/internal/similar"
)

func (s *Server) routes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/events", s.sse)
	s.handle(mux, "GET /api/stats", s.getStats)

	s.handle(mux, "POST /api/images/ids", s.listIDs)
	s.handle(mux, "POST /api/images/count", s.countImages)
	s.handle(mux, "POST /api/images/batch", s.batchImages)
	s.handle(mux, "GET /api/images/{id}", s.getImage)
	s.handle(mux, "PATCH /api/images/{id}", s.patchImage)
	s.handle(mux, "GET /api/images/{id}/thumb", s.thumbByID)
	s.handle(mux, "GET /api/images/{id}/preview", s.imagePreview)
	s.handle(mux, "GET /api/images/{id}/original", s.imageOriginal)
	s.handle(mux, "GET /api/thumbs/{sha}", s.thumb)
	s.handle(mux, "POST /api/images/tags", s.bulkTags)
	s.handle(mux, "POST /api/images/trash", s.trashImages)
	s.handle(mux, "POST /api/images/restore", s.restoreImages)

	s.handle(mux, "GET /api/tags", s.listTags)
	s.handle(mux, "POST /api/tags", s.createTag)
	s.handle(mux, "PATCH /api/tags/{id}", s.renameTag)
	s.handle(mux, "DELETE /api/tags/{id}", s.deleteTag)

	s.handle(mux, "GET /api/fs/list", s.fsList)

	s.handle(mux, "GET /api/jobs", s.listJobs)
	s.handle(mux, "GET /api/jobs/{id}", s.getJob)
	s.handle(mux, "POST /api/jobs/{id}/cancel", s.cancelJob)
	s.handle(mux, "POST /api/jobs/import", s.startImport)
	s.handle(mux, "POST /api/jobs/export", s.startExport)
	s.handle(mux, "POST /api/jobs/backup", s.startBackup)
	s.handle(mux, "POST /api/jobs/compact", s.startCompact)
	s.handle(mux, "POST /api/jobs/empty-trash", s.startEmptyTrash)
	s.handle(mux, "GET /api/backups/{token}", s.downloadBackup)

	s.handle(mux, "POST /api/dedup/scans", s.startScan)
	s.handle(mux, "GET /api/dedup/scans", s.listScans)
	s.handle(mux, "GET /api/dedup/scans/{id}", s.getScan)
	s.handle(mux, "POST /api/dedup/resolve", s.resolveDups)

	s.handle(mux, "GET /api/metrics", s.listMetrics)
	s.handle(mux, "POST /api/metrics", s.createMetric)
	s.handle(mux, "PATCH /api/metrics/{id}", s.updateMetric)
	s.handle(mux, "DELETE /api/metrics/{id}", s.deleteMetric)
	s.handle(mux, "GET /api/metrics/{id}/rankings", s.rankings)
	s.handle(mux, "POST /api/metrics/{id}/recalc", s.recalc)

	s.handle(mux, "GET /api/runs", s.listRuns)
	s.handle(mux, "POST /api/runs", s.createRun)
	s.handle(mux, "GET /api/runs/{id}", s.getRun)
	s.handle(mux, "DELETE /api/runs/{id}", s.deleteRun)
	s.handle(mux, "GET /api/runs/{id}/next", s.nextPair)
	s.handle(mux, "POST /api/runs/{id}/answer", s.answer)
	s.handle(mux, "POST /api/runs/{id}/undo", s.undo)
	s.handle(mux, "POST /api/runs/{id}/status", s.runStatus)
}

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) error {
	st, err := library.GetStats(r.Context(), s.b)
	if err != nil {
		return err
	}
	return ok(w, st)
}

type listRequest struct {
	Query query.ImageQuery `json:"query"`
	Sort  query.Sort       `json:"sort"`
}

func (s *Server) listIDs(w http.ResponseWriter, r *http.Request) error {
	var req listRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	if req.Sort.Field == query.SortScore {
		if err := s.scoring.RefreshDirty(ctx, req.Sort.MetricID); err != nil {
			return err
		}
	}
	ids, err := library.ListIDs(ctx, s.b, req.Query, req.Sort)
	if err != nil {
		return badRequest(err)
	}
	if req.Sort.Field == query.SortSimilar {
		if ids, err = s.similarOrder(r, ids, req.Sort.SimilarTo); err != nil {
			return err
		}
		if req.Sort.Desc {
			for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	return ok(w, map[string]any{"ids": ids, "total": len(ids)})
}

func (s *Server) similarOrder(r *http.Request, ids []int64, target int64) ([]int64, error) {
	h := sha256.New()
	binary.Write(h, binary.LittleEndian, target)
	binary.Write(h, binary.LittleEndian, ids)
	key := string(h.Sum(nil))
	if v, hit := s.simSort.get(key); hit {
		return append([]int64{}, v...), nil
	}
	out, err := similar.Order(r.Context(), s.b, ids, target)
	if err != nil {
		return nil, err
	}
	s.simSort.put(key, out)
	return append([]int64{}, out...), nil
}

func (s *Server) countImages(w http.ResponseWriter, r *http.Request) error {
	var req listRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	n, err := library.Count(r.Context(), s.b, req.Query)
	if err != nil {
		return badRequest(err)
	}
	pairs := int64(n) * int64(n-1) / 2
	if n < 2 {
		pairs = 0
	}
	return ok(w, map[string]any{"count": n, "pairs": pairs, "description": req.Query.Describe()})
}

func (s *Server) batchImages(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if len(req.IDs) > 5000 {
		return apiErr(http.StatusBadRequest, "at most 5000 ids per batch")
	}
	images, err := library.GetImages(r.Context(), s.b, req.IDs)
	if err != nil {
		return err
	}
	return ok(w, images)
}

func (s *Server) getImage(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	im, err := library.GetImage(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	scores, err := library.Scores(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"image": im, "scores": scores})
}

func (s *Server) patchImage(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Name *string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if req.Name != nil {
		if _, err := library.Rename(r.Context(), s.b, id, *req.Name); err != nil {
			if err == library.ErrNotFound {
				return err
			}
			return badRequest(err)
		}
		s.events.Changed("images")
	}
	im, err := library.GetImage(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	return ok(w, im)
}

func (s *Server) thumb(w http.ResponseWriter, r *http.Request) error {
	sha := r.PathValue("sha")
	data, err := library.Thumb(r.Context(), s.b, sha)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, err = w.Write(data)
	return nil
}

func (s *Server) thumbByID(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	data, err := library.ThumbByID(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	// An image's content never changes, so its thumbnail is immutable.
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
	return nil
}

// previewSizes are the display sizes rendered and cached.
var previewSizes = []int{800, 1600, 2560}

var previewSlots = make(chan struct{}, 3) // concurrent full decodes

func (s *Server) imagePreview(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	want, _ := strconv.Atoi(r.URL.Query().Get("size"))
	size := previewSizes[len(previewSizes)-1]
	for _, ps := range previewSizes {
		if want <= ps {
			size = ps
			break
		}
	}
	ctx := r.Context()
	blobID, format, err := library.BlobInfo(ctx, s.b, id)
	if err != nil {
		return err
	}
	etag := fmt.Sprintf(`"%d-%d"`, blobID, size)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}
	key := fmt.Sprintf("%d/%d", blobID, size)
	data, err := s.preview.get(key, func() ([]byte, error) {
		select {
		case previewSlots <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-previewSlots }()
		raw, err := library.BlobData(ctx, s.b, blobID)
		if err != nil {
			return nil, err
		}
		return imaging.Preview(imaging.Format(format), raw, size)
	})
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
	return nil
}

// originalSlots bounds concurrent whole-blob reads (originals are loaded
// into memory in full).
var originalSlots = make(chan struct{}, 4)

func (s *Server) imageOriginal(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	select {
	case originalSlots <- struct{}{}:
	case <-r.Context().Done():
		return r.Context().Err()
	}
	defer func() { <-originalSlots }()
	blob, err := library.Original(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	f := imaging.Format(blob.Image.Format)
	disp := "inline"
	if r.URL.Query().Get("download") != "" {
		disp = "attachment"
	}
	w.Header().Set("Content-Type", f.MIME())
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": exporter.SafeName(blob.Image.Name, f)}))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(blob.Data)))
	w.Write(blob.Data)
	return nil
}

type idsRequest struct {
	IDs []int64 `json:"ids"`
}

func (s *Server) bulkTags(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		IDs    []int64  `json:"ids"`
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	if len(req.Add) > 0 {
		if err := library.AddTags(ctx, s.b, req.IDs, req.Add); err != nil {
			return badRequest(err)
		}
	}
	if len(req.Remove) > 0 {
		if err := library.RemoveTags(ctx, s.b, req.IDs, req.Remove); err != nil {
			return err
		}
	}
	s.events.Changed("images", "tags")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) trashImages(w http.ResponseWriter, r *http.Request) error {
	var req idsRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	n, err := library.Trash(r.Context(), s.b, req.IDs)
	if err != nil {
		return err
	}
	s.events.Changed("images", "trash", "tags", "metrics")
	return ok(w, map[string]int{"trashed": n})
}

func (s *Server) restoreImages(w http.ResponseWriter, r *http.Request) error {
	var req idsRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	n, err := library.Restore(r.Context(), s.b, req.IDs)
	if err != nil {
		return err
	}
	for _, sc := range s.scans.All() {
		sc.Restored(req.IDs)
	}
	s.events.Changed("images", "trash", "tags", "metrics", "dedup")
	return ok(w, map[string]int{"restored": n})
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) error {
	tags, err := library.ListTags(r.Context(), s.b)
	if err != nil {
		return err
	}
	return ok(w, tags)
}

func (s *Server) createTag(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	id, err := library.CreateTag(r.Context(), s.b, req.Name)
	if err != nil {
		return badRequest(err)
	}
	s.events.Changed("tags")
	return ok(w, map[string]int64{"id": id})
}

func (s *Server) renameTag(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	into, err := library.RenameTag(r.Context(), s.b, id, req.Name)
	if err != nil {
		if err == library.ErrNotFound {
			return err
		}
		return badRequest(err)
	}
	s.events.Changed("tags", "images")
	return ok(w, map[string]any{"id": into, "merged": into != id})
}

func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := library.DeleteTag(r.Context(), s.b, id); err != nil {
		return err
	}
	s.events.Changed("tags", "images")
	return ok(w, map[string]bool{"ok": true})
}

// orderCache memoises similarity orders (they depend only on the ids,
// since image content never changes).
type orderCache struct {
	mu    sync.Mutex
	max   int
	keys  []string
	items map[string][]int64
}

func newOrderCache(n int) *orderCache { return &orderCache{max: n, items: map[string][]int64{}} }

func (c *orderCache) get(k string) ([]int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[k]
	return v, ok
}

func (c *orderCache) put(k string, v []int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[k]; !ok {
		c.keys = append(c.keys, k)
	}
	c.items[k] = v
	for len(c.keys) > c.max {
		delete(c.items, c.keys[0])
		c.keys = c.keys[1:]
	}
}
