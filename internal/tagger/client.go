// Package tagger talks to a WD tagger server (python/tagger_server.py:
// SmilingWolf's WD v3 ONNX models behind a small HTTP API), and installs
// and runs that server locally.
package tagger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Tag is a tag and the model's confidence in it (0–1).
type Tag struct {
	Tag   string  `json:"tag"`
	Score float64 `json:"score"`
}

// Result is what the tagger found in an image, most confident first.
type Result struct {
	General    []Tag `json:"general"`
	Characters []Tag `json:"characters"`
	// Rating is the most likely of general, sensitive, questionable and
	// explicit.
	Rating *Tag `json:"rating"`
	// Raw is the server's reply.
	Raw string `json:"-"`
}

// Options choose the tags returned.
type Options struct {
	// GeneralThreshold and CharacterThreshold are the minimum confidence
	// for general and character tags.
	GeneralThreshold   float64
	CharacterThreshold float64
	Characters         bool
}

// Health describes a running tagger server.
type Health struct {
	Status string `json:"status"`
	// Name is the model's name when the server knows it.
	Name      string   `json:"name,omitempty"`
	Model     string   `json:"model"`
	Providers []string `json:"providers"`
	TagCount  int      `json:"tagCount"`
}

// OnGPU reports whether the model runs on the GPU.
func (h *Health) OnGPU() bool {
	for _, p := range h.Providers {
		if p != "CPUExecutionProvider" {
			return true
		}
	}
	return false
}

// Tagger tags images: a remote server (Client) or the local installation
// (Local).
type Tagger interface {
	Tag(ctx context.Context, img []byte, o Options) (*Result, error)
	Health(ctx context.Context) (*Health, error)
	// Name names the model, for results and reports.
	Name() string
}

// Error is a failed request.
type Error struct {
	// Status is the HTTP status, 0 when the server could not be reached.
	Status  int
	Message string
	// Fatal errors (wrong URL, a server that is not a tagger) will fail
	// every request, so callers should stop.
	Fatal bool
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// IsFatal reports whether err will recur on every request.
func IsFatal(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Fatal
}

// NormalizeEndpoint tidies a tagger address: "host:port" gets http://,
// and a trailing slash or API path is removed.
func NormalizeEndpoint(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	s = strings.TrimRight(s, "/")
	for _, suffix := range []string{"/tag/details", "/tag", "/health", "/docs"} {
		s = strings.TrimSuffix(s, suffix)
	}
	return strings.TrimRight(s, "/")
}

// ValidateEndpoint checks a normalized address.
func ValidateEndpoint(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("the tagger address must be host:port or an http:// URL, such as http://192.168.1.20:8000")
	}
	return nil
}

// Client calls a tagger server.
type Client struct {
	base  string
	http  *http.Client
	delay time.Duration // before the first retry; later ones wait longer

	mu   sync.Mutex
	name string
}

// New returns a client for the server at endpoint (see NormalizeEndpoint).
func New(endpoint string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	return &Client{base: NormalizeEndpoint(endpoint), http: &http.Client{Timeout: timeout}, delay: time.Second}
}

// Endpoint is the server's base URL.
func (c *Client) Endpoint() string { return c.base }

// Name is the model name the server reported, or "WD tagger".
func (c *Client) Name() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.name != "" {
		return c.name
	}
	return "WD tagger"
}

// Health asks the server for its status.
func (c *Client) Health(ctx context.Context) (*Health, error) { return c.health(ctx, 3) }

// healthOnce asks once, without retrying.
func (c *Client) healthOnce(ctx context.Context) (*Health, error) { return c.health(ctx, 1) }

func (c *Client) health(ctx context.Context, attempts int) (*Health, error) {
	var raw struct {
		Health
		TagCount int `json:"tag_count"`
	}
	body, err := c.do(ctx, attempts, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/health", nil)
	})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &raw); err != nil || raw.Status == "" || raw.TagCount == 0 {
		return nil, &Error{Message: fmt.Sprintf("%s did not answer like a WD tagger (unexpected reply %q)", c.base, clip(string(body), 120)), Fatal: true}
	}
	h := raw.Health
	h.TagCount = raw.TagCount
	if h.Name != "" {
		c.mu.Lock()
		c.name = h.Name
		c.mu.Unlock()
	}
	return &h, nil
}

// Tag sends a JPEG to the server.
func (c *Client) Tag(ctx context.Context, img []byte, o Options) (*Result, error) {
	q := url.Values{}
	q.Set("general_threshold", strconv.FormatFloat(clamp01(o.GeneralThreshold), 'f', -1, 64))
	q.Set("character_threshold", strconv.FormatFloat(clamp01(o.CharacterThreshold), 'f', -1, 64))
	q.Set("include_characters", strconv.FormatBool(o.Characters))
	body, err := c.do(ctx, 3, func() (*http.Request, error) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="file"; filename="image.jpg"`)
		h.Set("Content-Type", "image/jpeg")
		part, err := mw.CreatePart(h)
		if err != nil {
			return nil, err
		}
		part.Write(img)
		mw.Close()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/tag/details?"+q.Encode(), &buf)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	var r Result
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, &Error{Message: fmt.Sprintf("unexpected reply from the tagger: %q", clip(string(body), 120)), Fatal: true}
	}
	r.Raw = string(body)
	return &r, nil
}

// do sends a request, retrying when the server is unreachable, busy or
// starting.
func (c *Client) do(ctx context.Context, attempts int, build func() (*http.Request, error)) ([]byte, error) {
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			wait := c.delay * time.Duration(1<<(2*(attempt-1))) // 1×, 4×
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
		req, err := build()
		if err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = &Error{Message: fmt.Sprintf("the tagger at %s could not be reached: %v", c.base, unwrapURLError(err))}
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if err != nil {
			last = &Error{Message: fmt.Sprintf("reading the tagger's reply: %v", err)}
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return body, nil
		}
		e := &Error{Status: resp.StatusCode, Message: "the tagger refused the request: " + detail(body)}
		switch {
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed:
			e.Message = fmt.Sprintf("%s is not a WD tagger server (no %s)", c.base, req.URL.Path)
			e.Fatal = true
			return nil, e
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden ||
			resp.StatusCode == http.StatusUnprocessableEntity:
			e.Fatal = true
			return nil, e
		case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
			last = e
			continue
		default:
			return nil, e
		}
	}
	return nil, last
}

// detail extracts FastAPI's {"detail": ...} message.
func detail(body []byte) string {
	var d struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(body, &d) == nil && len(d.Detail) > 0 {
		var s string
		if json.Unmarshal(d.Detail, &s) == nil {
			return s
		}
		var list []struct {
			Msg string `json:"msg"`
			Loc []any  `json:"loc"`
		}
		if json.Unmarshal(d.Detail, &list) == nil && len(list) > 0 {
			var parts []string
			for _, it := range list {
				parts = append(parts, fmt.Sprintf("%v: %s", it.Loc, it.Msg))
			}
			return strings.Join(parts, "; ")
		}
		return clip(string(d.Detail), 200)
	}
	return clip(strings.TrimSpace(string(body)), 200)
}

func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func clamp01(v float64) float64 { return max(0, min(1, v)) }

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
