// Package taggertest is a fake WD tagger server for tests.
package taggertest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
)

// Request is a tagging request the fake received.
type Request struct {
	Image []byte
	Query url.Values
}

// Server is a fake tagger. Tags answers each request: the result, or an
// HTTP status (non-zero) to fail with.
type Server struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []Request
}

// Reply is what the fake answers.
type Reply struct {
	General    map[string]float64
	Characters map[string]float64
	Rating     string
	Score      float64
}

type tag struct {
	Tag   string  `json:"tag"`
	Score float64 `json:"score"`
}

// New starts a fake tagger; answer decides each reply.
func New(answer func(Request) (Reply, int)) *Server {
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"status": "ok", "name": "fake-tagger", "model": "model.onnx", "device": 0,
			"providers": []string{"CPUExecutionProvider"}, "tag_count": 10861,
		})
	})
	mux.HandleFunc("POST /tag/details", func(w http.ResponseWriter, r *http.Request) {
		f, _, err := r.FormFile("file")
		if err != nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(map[string]any{"detail": []map[string]any{{"loc": []string{"body", "file"}, "msg": "Field required"}}})
			return
		}
		img, _ := io.ReadAll(f)
		req := Request{Image: img, Query: r.URL.Query()}
		s.mu.Lock()
		s.reqs = append(s.reqs, req)
		s.mu.Unlock()
		rep, status := answer(req)
		if status != 0 {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]string{"detail": "fake failure"})
			return
		}
		gt, _ := strconv.ParseFloat(req.Query.Get("general_threshold"), 64)
		ct, _ := strconv.ParseFloat(req.Query.Get("character_threshold"), 64)
		out := map[string]any{"general": pick(rep.General, gt), "characters": []tag{}, "rating": nil}
		if req.Query.Get("include_characters") != "false" {
			out["characters"] = pick(rep.Characters, ct)
		}
		if rep.Rating != "" {
			out["rating"] = tag{rep.Rating, rep.Score}
		}
		json.NewEncoder(w).Encode(out)
	})
	s.Server = httptest.NewServer(mux)
	return s
}

// pick keeps tags above the threshold, most confident first, as the real
// server does.
func pick(m map[string]float64, threshold float64) []tag {
	out := []tag{}
	for name, score := range m {
		if score > threshold {
			out = append(out, tag{name, score})
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && (out[j].Score > out[j-1].Score || out[j].Score == out[j-1].Score && out[j].Tag < out[j-1].Tag); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Requests returns the tagging requests received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}
