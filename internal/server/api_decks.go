package server

import (
	"context"
	"net/http"

	"photobag/internal/decks"
	"photobag/internal/query"
)

func (s *Server) deckRoutes(mux *http.ServeMux) {
	s.handle(mux, "GET /api/decks", s.listDecks)
	s.handle(mux, "POST /api/decks", s.createDeck)
	s.handle(mux, "GET /api/decks/{id}", s.getDeck)
	s.handle(mux, "PATCH /api/decks/{id}", s.updateDeck)
	s.handle(mux, "DELETE /api/decks/{id}", s.deleteDeck)
	s.handle(mux, "POST /api/decks/{id}/add", s.addToDeck)
	s.handle(mux, "POST /api/decks/{id}/remove", s.removeFromDeck)
	s.handle(mux, "POST /api/decks/{id}/move", s.moveInDeck)
	s.handle(mux, "POST /api/decks/{id}/sort", s.sortDeck)
}

// deckImages names images to add to a deck: ids, or the images matching
// a query in the order of a sort (as the gallery shows them).
type deckImages struct {
	IDs   []int64           `json:"ids"`
	Query *query.ImageQuery `json:"query"`
	Sort  query.Sort        `json:"sort"`
}

func (s *Server) resolveDeckImages(ctx context.Context, req deckImages) ([]int64, error) {
	if req.Query == nil {
		return req.IDs, nil
	}
	return s.orderedIDs(ctx, *req.Query, req.Sort)
}

func (s *Server) listDecks(w http.ResponseWriter, r *http.Request) error {
	list, err := decks.List(r.Context(), s.b)
	if err != nil {
		return err
	}
	return ok(w, list)
}

func (s *Server) createDeck(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name     string          `json:"name"`
		Notes    string          `json:"notes"`
		Settings *decks.Settings `json:"settings"`
		deckImages
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	ids, err := s.resolveDeckImages(ctx, req.deckImages)
	if err != nil {
		return err
	}
	d, err := decks.Create(ctx, s.b, req.Name, req.Notes, req.Settings)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		if _, err := decks.Add(ctx, s.b, d.ID, ids); err != nil {
			decks.Delete(ctx, s.b, d.ID)
			return err
		}
		if d, err = decks.Get(ctx, s.b, d.ID); err != nil {
			return err
		}
	}
	s.events.Changed("decks")
	return ok(w, d)
}

func (s *Server) getDeck(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := decks.Get(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	return ok(w, d)
}

func (s *Server) updateDeck(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var c decks.Change
	if err := readJSON(r, &c); err != nil {
		return err
	}
	d, err := decks.Update(r.Context(), s.b, id, c)
	if err != nil {
		return err
	}
	s.events.Changed("decks")
	return ok(w, d)
}

func (s *Server) deleteDeck(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := decks.Delete(r.Context(), s.b, id); err != nil {
		return err
	}
	s.events.Changed("decks")
	return ok(w, map[string]bool{"ok": true})
}

func (s *Server) addToDeck(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req deckImages
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ids, err := s.resolveDeckImages(r.Context(), req)
	if err != nil {
		return err
	}
	res, err := decks.Add(r.Context(), s.b, id, ids)
	if err != nil {
		return err
	}
	s.events.Changed("decks")
	return ok(w, res)
}

func (s *Server) removeFromDeck(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req idsRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	n, err := decks.Remove(r.Context(), s.b, id, req.IDs)
	if err != nil {
		return err
	}
	s.events.Changed("decks")
	return ok(w, map[string]int{"removed": n})
}

func (s *Server) moveInDeck(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		IDs []int64 `json:"ids"`
		// Before is the image to move them in front of; 0 moves them to
		// the end.
		Before int64 `json:"before"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	return s.afterReorder(w, r, id, decks.Move(r.Context(), s.b, id, req.IDs, req.Before))
}

// sortDeck puts a deck in the order of a gallery sort, or in the order of
// ids (the rest of the deck following).
func (s *Server) sortDeck(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Sort *query.Sort `json:"sort"`
		IDs  []int64     `json:"ids"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ctx := r.Context()
	if req.Sort == nil {
		return s.afterReorder(w, r, id, decks.SetOrder(ctx, s.b, id, req.IDs))
	}
	ids, err := decks.IDs(ctx, s.b, id)
	if err != nil {
		return err
	}
	if len(ids) == 0 { // (an empty id list would select every image)
		return s.afterReorder(w, r, id, nil)
	}
	sorted, err := s.orderedIDs(ctx, query.ImageQuery{IDs: ids}, *req.Sort)
	if err != nil {
		return err
	}
	return s.afterReorder(w, r, id, decks.SetOrder(ctx, s.b, id, sorted))
}

func (s *Server) afterReorder(w http.ResponseWriter, r *http.Request, id int64, err error) error {
	if err != nil {
		return err
	}
	s.events.Changed("decks")
	d, err := decks.Get(r.Context(), s.b, id)
	if err != nil {
		return err
	}
	return ok(w, d)
}
