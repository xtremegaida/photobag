// Package llmtest provides a fake OpenAI-compatible chat server for tests.
package llmtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Request is what the fake server saw.
type Request struct {
	System string
	// Prompt is the text of the last user message.
	Prompt string
	Images int
	// Turns is the number of messages.
	Turns int
	// Auth is the Authorization header.
	Auth string
	Body map[string]any
}

// Server is a fake chat completions endpoint at URL (ending in /v1).
type Server struct {
	*httptest.Server
	URL string

	mu       sync.Mutex
	requests []Request
}

// Answer returns the reply text for a request, or an HTTP status and
// error body when status is not 0.
type Answer func(r Request) (reply string, status int)

// New starts a fake server.
func New(answer Answer) *Server {
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"object":"list","data":[{"id":"fake-vision"}]}`))
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		req := Request{Body: body, Auth: r.Header.Get("Authorization")}
		msgs, _ := body["messages"].([]any)
		req.Turns = len(msgs)
		for _, m := range msgs {
			mm, _ := m.(map[string]any)
			role, _ := mm["role"].(string)
			var text strings.Builder
			switch c := mm["content"].(type) {
			case string:
				text.WriteString(c)
			case []any:
				for _, p := range c {
					pm, _ := p.(map[string]any)
					if pm["type"] == "image_url" {
						req.Images++
					}
					if t, ok := pm["text"].(string); ok {
						text.WriteString(t)
					}
				}
			}
			switch role {
			case "system":
				req.System = text.String()
			case "user":
				req.Prompt = text.String()
			}
		}
		s.mu.Lock()
		s.requests = append(s.requests, req)
		s.mu.Unlock()
		reply, status := answer(req)
		if status != 0 {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": reply}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"model": "fake-vision",
			"choices": []any{map[string]any{"finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": reply}}},
			"usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 10},
		})
	})
	s.Server = httptest.NewServer(mux)
	s.URL = s.Server.URL + "/v1"
	return s
}

// Requests returns the requests received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}
