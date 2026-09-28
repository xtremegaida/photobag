package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeEndpoint(t *testing.T) {
	for in, want := range map[string]string{
		" http://127.0.0.1:1234/v1/ ":                "http://127.0.0.1:1234/v1",
		"https://api.openai.com/v1/chat/completions": "https://api.openai.com/v1",
		"http://x/v1/chat/completions/":              "http://x/v1",
	} {
		if got := NormalizeEndpoint(in); got != want {
			t.Errorf("NormalizeEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChatRequestAndFallbacks(t *testing.T) {
	var calls atomic.Int32
	temp := 0.2
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("request %s auth %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &body)
		switch n {
		case 1: // an OpenAI reasoning model
			if body["max_tokens"] == nil || body["temperature"] == nil || body["seed"] != float64(7) {
				t.Errorf("first body %v", body)
			}
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead."}}`))
		case 2:
			if body["max_completion_tokens"] != float64(100) || body["max_tokens"] != nil {
				t.Errorf("second body %v", body)
			}
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"message":"Unsupported value: 'temperature' does not support 0.2 with this model. Only the default (1) value is supported."}}`))
		case 3:
			if body["temperature"] != nil {
				t.Errorf("temperature still sent")
			}
			msgs := body["messages"].([]any)
			parts := msgs[1].(map[string]any)["content"].([]any)
			img := parts[0].(map[string]any)["image_url"].(map[string]any)["url"].(string)
			if !strings.HasPrefix(img, "data:image/jpeg;base64,") || parts[1].(map[string]any)["text"] != "What is this?" {
				t.Errorf("content parts %v", parts)
			}
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			w.Write([]byte(`{"error":"slow down"}`))
		default:
			w.Write([]byte(`{"model":"m1","choices":[{"finish_reason":"stop","message":{"content":[{"type":"text","text":"<think>hmm</think>  A cat. "}]}}],
				"usage":{"prompt_tokens":10,"completion_tokens":3}}`))
		}
	}))
	defer srv.Close()
	c := New(Config{Endpoint: srv.URL + "/v1/", APIKey: "k", Model: "m", MaxTokens: 100, Temperature: &temp,
		Extra: map[string]any{"seed": 7}, Retries: 2, RetryDelay: time.Millisecond})
	r, err := c.Chat(context.Background(), []Message{{Role: "system", Text: "be brief"}, {Role: "user", Text: "What is this?", Images: [][]byte{{0xff, 0xd8}}}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "A cat." || r.Model != "m1" || r.PromptTokens != 10 || calls.Load() != 4 {
		t.Errorf("reply %+v after %d calls", r, calls.Load())
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		status int
		body   string
		fatal  bool
		msg    string
	}{
		{401, `{"error":{"message":"Incorrect API key provided"}}`, true, "Incorrect API key"},
		{404, `<html>nope</html>`, true, "ends in /v1"},
		{400, `{"error":"model 'llava' not found, try pulling it first"}`, true, "not found"},
		{500, `{"error":{"message":"image input is not supported - hint: if this is unexpected, you may need to provide the mmproj"}}`, true, "does not accept images"},
		{400, `{"error":{"message":"Invalid image"}}`, false, "Invalid image"},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			w.Write([]byte(tc.body))
		}))
		c := New(Config{Endpoint: srv.URL, Model: "m", Retries: 0})
		_, err := c.Chat(context.Background(), []Message{{Role: "user", Text: "hi"}})
		srv.Close()
		if err == nil || IsFatal(err) != tc.fatal || !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%d %s: err %v fatal %v", tc.status, tc.body, err, IsFatal(err))
		}
	}
	// Unreachable servers are reported, not fatal (they may come back).
	c := New(Config{Endpoint: "http://127.0.0.1:1", Retries: 0})
	if _, err := c.Chat(context.Background(), nil); err == nil || IsFatal(err) || !strings.Contains(err.Error(), "cannot reach") {
		t.Errorf("unreachable: %v", err)
	}
}

func TestReplyShapes(t *testing.T) {
	for raw, want := range map[string]string{
		`{"choices":[{"message":{"content":"OK","reasoning_content":"thinking"}}]}`: "OK",
		`{"choices":[{"message":{"content":"</think>\n\nOK"}}]}`:                    "OK",
		`{"choices":[{"message":{"content":"Sure <think>never ends"}}]}`:            "Sure",
	} {
		r, err := parseReply([]byte(raw))
		if err != nil || r.Text != want {
			t.Errorf("%s: %+v %v", raw, r, err)
		}
	}
	_, err := parseReply([]byte(`{"choices":[{"finish_reason":"length","message":{"content":"","reasoning_content":"..."}}],"usage":{"completion_tokens":512}}`))
	if err == nil || !strings.Contains(err.Error(), "512 reply tokens") {
		t.Errorf("length without answer: %v", err)
	}
}

func TestModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"object":"list","data":[{"id":"b"},{"id":"a"}],"models":[{"name":"ignored"}]}`))
	}))
	defer srv.Close()
	got, err := New(Config{Endpoint: srv.URL}).Models(context.Background())
	if err != nil || strings.Join(got, ",") != "a,b" {
		t.Errorf("models %v %v", got, err)
	}
}
