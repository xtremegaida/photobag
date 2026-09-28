// Package llm is a small client for OpenAI-compatible chat completion APIs
// with image input: OpenAI, llama.cpp server, LM Studio, Ollama, vLLM,
// OpenRouter and similar.
package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config configures a client.
type Config struct {
	// Endpoint is the API base URL, e.g. http://127.0.0.1:1234/v1 or
	// https://api.openai.com/v1.
	Endpoint string
	APIKey   string
	Model    string
	// MaxTokens caps the reply length (including any reasoning).
	MaxTokens int
	// Temperature is sent when set; nil leaves the server default.
	Temperature *float64
	// Extra is merged into every request body, for server-specific
	// options such as {"chat_template_kwargs": {"enable_thinking": false}}.
	Extra map[string]any
	// Timeout bounds each attempt.
	Timeout time.Duration
	// Retries is how often a request is retried after rate limiting or a
	// server error, waiting RetryDelay (default 1s), then 4×, then 16×.
	Retries    int
	RetryDelay time.Duration
}

// Client sends chat completion requests.
type Client struct {
	cfg  Config
	http *http.Client

	mu              sync.Mutex
	maxTokensField  string
	dropTemperature bool
}

// New creates a client.
func New(cfg Config) *Client {
	cfg.Endpoint = NormalizeEndpoint(cfg.Endpoint)
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}
	if cfg.Retries < 0 {
		cfg.Retries = 0
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 16
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = time.Second
	}
	return &Client{cfg: cfg, http: &http.Client{Transport: tr}, maxTokensField: "max_tokens"}
}

// Model is the configured model name.
func (c *Client) Model() string { return c.cfg.Model }

// NormalizeEndpoint trims whitespace, trailing slashes and a pasted
// /chat/completions suffix from a base URL.
func NormalizeEndpoint(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, "/chat/completions")
	return strings.TrimRight(s, "/")
}

// Message is one chat message. Images are JPEG bytes, placed before the text.
type Message struct {
	Role   string
	Text   string
	Images [][]byte
}

// Reply is a model's answer.
type Reply struct {
	Text             string
	Reasoning        string
	FinishReason     string
	Model            string
	PromptTokens     int
	CompletionTokens int
	Duration         time.Duration
}

// Error is an API or connection failure.
type Error struct {
	// Status is the HTTP status, 0 when the server could not be reached.
	Status  int
	Message string
	// Fatal errors (bad key, wrong URL or model, no image support) will
	// fail every request, so callers should stop.
	Fatal bool
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// IsFatal reports whether err means further requests are pointless.
func IsFatal(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Fatal
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

func (c *Client) body(msgs []Message) map[string]any {
	body := map[string]any{}
	for k, v := range c.cfg.Extra {
		body[k] = v
	}
	var out []map[string]any
	for _, m := range msgs {
		if len(m.Images) == 0 {
			out = append(out, map[string]any{"role": m.Role, "content": m.Text})
			continue
		}
		var parts []contentPart
		for _, img := range m.Images {
			parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURL{
				URL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(img)}})
		}
		if m.Text != "" {
			parts = append(parts, contentPart{Type: "text", Text: m.Text})
		}
		out = append(out, map[string]any{"role": m.Role, "content": parts})
	}
	body["messages"] = out
	if c.cfg.Model != "" {
		body["model"] = c.cfg.Model
	}
	body["stream"] = false
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfg.MaxTokens > 0 {
		delete(body, "max_tokens")
		body[c.maxTokensField] = c.cfg.MaxTokens
	}
	if c.cfg.Temperature != nil && !c.dropTemperature {
		body["temperature"] = *c.cfg.Temperature
	}
	return body
}

// Chat sends a conversation and returns the reply, retrying rate limits
// and server errors.
func (c *Client) Chat(ctx context.Context, msgs []Message) (*Reply, error) {
	start := time.Now()
	adjusted := 0
	for attempt := 0; ; attempt++ {
		data, err := json.Marshal(c.body(msgs))
		if err != nil {
			return nil, err
		}
		status, raw, hdr, err := c.post(ctx, "/chat/completions", data)
		if err == nil && status == http.StatusOK {
			r, err := parseReply(raw)
			if err != nil {
				return nil, err
			}
			r.Duration = time.Since(start)
			return r, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var apiErr *Error
		if err != nil {
			if !errors.As(err, &apiErr) {
				return nil, err
			}
		} else {
			apiErr = statusError(status, raw)
			if status == http.StatusBadRequest && adjusted < 2 && c.adjust(apiErr.Message) {
				adjusted++
				attempt--
				continue
			}
		}
		if apiErr.Fatal || !retryable(apiErr.Status) || attempt >= c.cfg.Retries {
			return nil, apiErr
		}
		wait := c.cfg.RetryDelay * time.Duration(1<<(2*attempt)) // 1s, 4s, 16s
		if s, err := strconv.Atoi(hdr.Get("Retry-After")); err == nil && s > 0 {
			wait = time.Duration(s) * time.Second
		}
		wait = min(wait, 60*time.Second)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// adjust reacts to servers rejecting a parameter (newer OpenAI models want
// max_completion_tokens and only the default temperature). It reports
// whether the request should be sent again.
func (c *Client) adjust(msg string) bool {
	m := strings.ToLower(msg)
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.Contains(m, "max_tokens") && c.maxTokensField == "max_tokens" &&
		(strings.Contains(m, "max_completion_tokens") || strings.Contains(m, "unsupported") || strings.Contains(m, "not supported")) {
		c.maxTokensField = "max_completion_tokens"
		return true
	}
	if strings.Contains(m, "temperature") && !c.dropTemperature && c.cfg.Temperature != nil &&
		(strings.Contains(m, "unsupported") || strings.Contains(m, "not support") || strings.Contains(m, "default")) {
		c.dropTemperature = true
		return true
	}
	return false
}

func retryable(status int) bool {
	switch status {
	case 0, http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout, 529:
		return true
	}
	return false
}

func (c *Client) post(ctx context.Context, path string, data []byte) (int, []byte, http.Header, error) {
	return c.do(ctx, http.MethodPost, path, data)
}

func (c *Client) do(ctx context.Context, method, path string, data []byte) (int, []byte, http.Header, error) {
	if c.cfg.Endpoint == "" {
		return 0, nil, nil, &Error{Message: "no API endpoint is configured", Fatal: true}
	}
	actx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	var rd io.Reader
	if data != nil {
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(actx, method, c.cfg.Endpoint+path, rd)
	if err != nil {
		return 0, nil, nil, &Error{Message: fmt.Sprintf("invalid endpoint %q: %v", c.cfg.Endpoint, err), Fatal: true}
	}
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, ctx.Err()
		}
		if errors.Is(actx.Err(), context.DeadlineExceeded) {
			// Retrying would repeat the same slow work; fail this request.
			return 0, nil, nil, &Error{Status: http.StatusGatewayTimeout,
				Message: fmt.Sprintf("no answer within %s (raise the request timeout)", c.cfg.Timeout)}
		}
		return 0, nil, nil, &Error{Message: fmt.Sprintf("cannot reach %s: %v", c.cfg.Endpoint, unwrapURLError(err))}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, ctx.Err()
		}
		return 0, nil, nil, &Error{Message: "reading the reply: " + err.Error()}
	}
	return res.StatusCode, raw, res.Header, nil
}

func unwrapURLError(err error) error {
	for {
		u := errors.Unwrap(err)
		if u == nil {
			return err
		}
		err = u
	}
}

// statusError builds an Error from a non-200 response.
func statusError(status int, raw []byte) *Error {
	msg := errorMessage(raw)
	lower := strings.ToLower(msg)
	e := &Error{Status: status, Message: msg}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.Fatal = true
		if msg == "" {
			e.Message = "the endpoint rejected the API key"
		}
	case status == http.StatusNotFound:
		e.Fatal = true
		if msg == "" || strings.Contains(lower, "not found") && !strings.Contains(lower, "model") {
			e.Message = "not found: check the endpoint URL (it usually ends in /v1)" + suffix(msg)
		}
	case strings.Contains(lower, "model") && (strings.Contains(lower, "not found") || strings.Contains(lower, "does not exist") || strings.Contains(lower, "unknown model")):
		e.Fatal = true
	case strings.Contains(lower, "image") && (strings.Contains(lower, "not support") || strings.Contains(lower, "multimodal") ||
		strings.Contains(lower, "vision") || strings.Contains(lower, "mmproj")):
		e.Fatal = true
		e.Message = "the model does not accept images: " + msg
	}
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	return e
}

func suffix(msg string) string {
	if msg == "" {
		return ""
	}
	return ": " + msg
}

// errorMessage extracts the message from common error body shapes.
func errorMessage(raw []byte) string {
	var v struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  any             `json:"detail"`
	}
	if json.Unmarshal(raw, &v) == nil {
		if len(v.Error) > 0 {
			var s string
			if json.Unmarshal(v.Error, &s) == nil && s != "" {
				return s
			}
			var o struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(v.Error, &o) == nil && o.Message != "" {
				return o.Message
			}
		}
		if v.Message != "" {
			return v.Message
		}
		if v.Detail != nil {
			return fmt.Sprint(v.Detail)
		}
	}
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, "<") {
		return "" // an HTML error page
	}
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + "…"
	}
	return s
}

var thinkRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

func parseReply(raw []byte) (*Reply, error) {
	var v struct {
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content          json.RawMessage `json:"content"`
				ReasoningContent string          `json:"reasoning_content"`
				Reasoning        string          `json:"reasoning"`
				Refusal          string          `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, &Error{Status: http.StatusOK, Message: "the reply is not a chat completion: " + errorMessage(raw)}
	}
	if len(v.Choices) == 0 {
		if msg := errorMessage(raw); msg != "" && !strings.HasPrefix(msg, "{") {
			return nil, &Error{Status: http.StatusOK, Message: msg}
		}
		return nil, &Error{Status: http.StatusOK, Message: "the reply has no choices"}
	}
	ch := v.Choices[0]
	r := &Reply{
		Model: v.Model, FinishReason: ch.FinishReason,
		PromptTokens: v.Usage.PromptTokens, CompletionTokens: v.Usage.CompletionTokens,
		Reasoning: ch.Message.ReasoningContent,
	}
	if r.Reasoning == "" {
		r.Reasoning = ch.Message.Reasoning
	}
	r.Text = contentText(ch.Message.Content)
	// Reasoning inline in the content.
	if i := strings.Index(r.Text, "<think>"); i >= 0 {
		if strings.Contains(r.Text, "</think>") {
			r.Text = thinkRe.ReplaceAllString(r.Text, "")
		} else {
			r.Text = r.Text[:i] // never finished thinking
		}
	} else if j := strings.Index(r.Text, "</think>"); j >= 0 {
		r.Text = r.Text[j+len("</think>"):] // the template opened the block
	}
	r.Text = strings.TrimSpace(r.Text)
	if r.Text == "" {
		switch {
		case ch.Message.Refusal != "":
			return nil, &Error{Status: http.StatusOK, Message: "the model refused: " + ch.Message.Refusal}
		case ch.FinishReason == "length":
			return nil, &Error{Status: http.StatusOK, Message: fmt.Sprintf(
				"the model used all %d reply tokens without answering (probably thinking); raise Max tokens or turn thinking off", r.CompletionTokens)}
		case ch.FinishReason == "content_filter":
			return nil, &Error{Status: http.StatusOK, Message: "the reply was blocked by the provider's content filter"}
		}
	}
	return r, nil
}

func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" || p.Type == "output_text" || p.Type == "" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

// Models lists the model ids the endpoint offers.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	status, raw, _, err := c.do(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, statusError(status, raw)
	}
	var v struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, &Error{Status: status, Message: "the model list is not JSON; check the endpoint URL"}
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, m := range v.Data {
		add(m.ID)
	}
	if len(out) == 0 {
		for _, m := range v.Models {
			add(m.Model)
			if m.Model == "" {
				add(m.Name)
			}
		}
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out, nil
}
