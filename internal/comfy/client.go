package comfy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Client talks to a ComfyUI server.
type Client struct {
	endpoint string
	http     *http.Client
}

// New returns a client for a ComfyUI address such as 127.0.0.1:8188.
func New(endpoint string) *Client {
	return &Client{endpoint: NormalizeEndpoint(endpoint), http: &http.Client{Timeout: 2 * time.Minute}}
}

// Endpoint is the server's base URL.
func (c *Client) Endpoint() string { return c.endpoint }

// NormalizeEndpoint adds http:// and drops a trailing slash or a copied
// page path.
func NormalizeEndpoint(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	s = strings.TrimRight(s, "/")
	for _, suffix := range []string{"/prompt", "/ws", "/system_stats", "/object_info", "/queue", "/history"} {
		s = strings.TrimSuffix(s, suffix)
	}
	return strings.TrimRight(s, "/")
}

// ValidateEndpoint checks an address.
func ValidateEndpoint(s string) error {
	s = NormalizeEndpoint(s)
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%q is not a ComfyUI address (host:port or http://host:port)", s)
	}
	return nil
}

// Error is a failure reported by ComfyUI.
type Error struct {
	Status  int
	Message string
	body    []byte
}

func (e *Error) Error() string { return e.Message }

func (c *Client) url(path string, q url.Values) string {
	u := c.endpoint + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any, out any) error {
	var rd io.Reader
	if body != nil {
		js, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(js)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path, q), rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ComfyUI at %s did not answer: %w", c.endpoint, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 256<<20))
	if err != nil {
		return fmt.Errorf("reading ComfyUI's answer: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return &Error{Status: res.StatusCode, Message: errorText(res.StatusCode, path, data), body: data}
	}
	if out == nil {
		return nil
	}
	if b, ok := out.(*[]byte); ok {
		*b = data
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("ComfyUI's answer to %s is not what was expected (is %s a ComfyUI server?): %v", path, c.endpoint, err)
	}
	return nil
}

func errorText(status int, path string, data []byte) string {
	var e struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error != nil {
		switch v := e.Error.(type) {
		case string:
			return v
		case map[string]any:
			if m, _ := v["message"].(string); m != "" {
				return m
			}
		}
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	if status == http.StatusNotFound {
		return fmt.Sprintf("%s was not found: is this a ComfyUI server?", path)
	}
	return fmt.Sprintf("HTTP %d from ComfyUI: %s", status, s)
}

// Info reports on the server.
func (c *Client) Info(ctx context.Context) (*ServerInfo, error) {
	var st struct {
		System struct {
			OS             string `json:"os"`
			ComfyUIVersion string `json:"comfyui_version"`
			PythonVersion  string `json:"python_version"`
			PytorchVersion string `json:"pytorch_version"`
		} `json:"system"`
		Devices []struct {
			Name      string `json:"name"`
			Type      string `json:"type"`
			VRAMTotal int64  `json:"vram_total"`
			VRAMFree  int64  `json:"vram_free"`
		} `json:"devices"`
	}
	if err := c.do(ctx, "GET", "/system_stats", nil, nil, &st); err != nil {
		return nil, err
	}
	if st.System.PythonVersion == "" && len(st.Devices) == 0 {
		return nil, fmt.Errorf("%s answered, but not like ComfyUI does", c.endpoint)
	}
	info := &ServerInfo{Version: st.System.ComfyUIVersion, OS: st.System.OS, Python: firstWord(st.System.PythonVersion),
		Pytorch: st.System.PytorchVersion, Devices: []Device{}}
	for _, d := range st.Devices {
		name := d.Name
		if i := strings.Index(name, " : "); i >= 0 {
			name = name[:i] // "cuda:0 NVIDIA GeForce RTX 4090 : cudaMallocAsync"
		}
		info.Devices = append(info.Devices, Device{Name: name, Type: d.Type, VRAMTotal: d.VRAMTotal, VRAMFree: d.VRAMFree})
	}
	if q, err := c.Queue(ctx); err == nil {
		info.Queue = len(q.Running) + len(q.Pending)
	}
	if _, err := c.NodeInfo(ctx, "ETN_SendImageWebSocket"); err == nil {
		info.WebsocketNode = true
	} else if _, err := c.NodeInfo(ctx, "SaveImageWebsocket"); err == nil {
		info.WebsocketNode = true
	}
	return info, nil
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return s
}

// ErrUnknownNode reports a node class the server does not have.
var ErrUnknownNode = errors.New("unknown node class")

// NodeInfo returns the definition of a node class.
func (c *Client) NodeInfo(ctx context.Context, class string) (*NodeInfo, error) {
	var raw map[string]struct {
		Input struct {
			Required map[string]json.RawMessage `json:"required"`
			Optional map[string]json.RawMessage `json:"optional"`
		} `json:"input"`
		InputOrder struct {
			Required []string `json:"required"`
			Optional []string `json:"optional"`
		} `json:"input_order"`
		DisplayName string `json:"display_name"`
		OutputNode  bool   `json:"output_node"`
	}
	if err := c.do(ctx, "GET", "/object_info/"+url.PathEscape(class), nil, nil, &raw); err != nil {
		return nil, err
	}
	def, ok := raw[class]
	if !ok {
		return nil, fmt.Errorf("%w %s", ErrUnknownNode, class)
	}
	ni := &NodeInfo{Class: class, DisplayName: def.DisplayName, OutputNode: def.OutputNode, Inputs: []InputSpec{}}
	add := func(specs map[string]json.RawMessage, order []string) {
		names := slices.Clone(order)
		for k := range specs {
			if !slices.Contains(names, k) {
				names = append(names, k)
			}
		}
		for _, name := range names {
			if raw, ok := specs[name]; ok {
				ni.Inputs = append(ni.Inputs, parseSpec(name, raw))
			}
		}
	}
	add(def.Input.Required, def.InputOrder.Required)
	add(def.Input.Optional, def.InputOrder.Optional)
	return ni, nil
}

// parseSpec reads ["INT", {...}] or [["a", "b"], {...}] (a legacy combo)
// or ["COMBO", {"options": [...]}].
func parseSpec(name string, raw json.RawMessage) InputSpec {
	s := InputSpec{Name: name}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || len(parts) == 0 {
		return s
	}
	var typ string
	if json.Unmarshal(parts[0], &typ) == nil {
		s.Type = typ
	} else {
		var opts []any
		if json.Unmarshal(parts[0], &opts) == nil {
			s.Type, s.Options = "COMBO", opts
		}
	}
	if len(parts) > 1 {
		var o struct {
			Default   any      `json:"default"`
			Min       *float64 `json:"min"`
			Max       *float64 `json:"max"`
			Step      *float64 `json:"step"`
			Multiline bool     `json:"multiline"`
			Options   []any    `json:"options"`
			Control   any      `json:"control_after_generate"`
			Tooltip   string   `json:"tooltip"`
		}
		if json.Unmarshal(parts[1], &o) == nil {
			s.Default, s.Min, s.Max, s.Step, s.Multiline, s.Tooltip = o.Default, o.Min, o.Max, o.Step, o.Multiline, o.Tooltip
			if len(o.Options) > 0 {
				s.Options = o.Options
			}
			switch v := o.Control.(type) {
			case bool:
				s.Seed = v
			case string:
				s.Seed = v == "randomize" || v == "increment"
			}
		}
	}
	if s.Type == "COMBO" && s.Options == nil {
		s.Options = []any{}
	}
	return s
}

// PromptError is a prompt ComfyUI refused, usually a validation failure
// such as a model name it does not have.
type PromptError struct {
	Message string
	Nodes   []string // "Title: problem" per failing node
}

func (e *PromptError) Error() string {
	if len(e.Nodes) == 0 {
		return e.Message
	}
	return e.Message + ": " + strings.Join(e.Nodes, "; ")
}

// Submit queues a prompt for the websocket client clientID.
func (c *Client) Submit(ctx context.Context, w *Workflow, clientID, promptID string) error {
	body := map[string]any{"prompt": json.RawMessage(w.Compact()), "client_id": clientID, "prompt_id": promptID}
	var res struct {
		PromptID string `json:"prompt_id"`
	}
	err := c.do(ctx, "POST", "/prompt", nil, body, &res)
	var ce *Error
	if errors.As(err, &ce) && ce.Status == http.StatusBadRequest {
		return parsePromptError(w, ce.body)
	}
	return err
}

func parsePromptError(w *Workflow, data []byte) error {
	var r struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Details string `json:"details"`
		} `json:"error"`
		NodeErrors map[string]struct {
			Errors []struct {
				Message string `json:"message"`
				Details string `json:"details"`
			} `json:"errors"`
			ClassType string `json:"class_type"`
		} `json:"node_errors"`
	}
	if json.Unmarshal(data, &r) != nil {
		return &PromptError{Message: "ComfyUI refused the prompt: " + truncate(string(data), 300)}
	}
	pe := &PromptError{Message: strings.TrimSuffix(cmpOr(r.Error.Message, "ComfyUI refused the prompt"), ".")}
	if len(r.NodeErrors) == 0 && r.Error.Details != "" {
		pe.Message += " (" + truncate(r.Error.Details, 300) + ")"
	}
	ids := make([]string, 0, len(r.NodeErrors))
	for id := range r.NodeErrors {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, compareIDs)
	for _, id := range ids {
		ne := r.NodeErrors[id]
		var msgs []string
		for _, e := range ne.Errors {
			m := e.Message
			if e.Details != "" {
				m += ": " + truncate(e.Details, 200)
			}
			msgs = append(msgs, m)
		}
		label := "#" + id
		if w != nil {
			label = w.NodeLabel(id)
		}
		pe.Nodes = append(pe.Nodes, label+": "+strings.Join(msgs, ", "))
	}
	return pe
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// QueueState lists the prompt ids ComfyUI is running and has waiting.
type QueueState struct {
	Running []string
	Pending []string
}

// Queue reads ComfyUI's queue.
func (c *Client) Queue(ctx context.Context) (*QueueState, error) {
	var raw struct {
		Running [][]json.RawMessage `json:"queue_running"`
		Pending [][]json.RawMessage `json:"queue_pending"`
	}
	if err := c.do(ctx, "GET", "/queue", nil, nil, &raw); err != nil {
		return nil, err
	}
	ids := func(items [][]json.RawMessage) []string {
		var out []string
		for _, it := range items {
			var id string
			if len(it) > 1 && json.Unmarshal(it[1], &id) == nil {
				out = append(out, id)
			}
		}
		return out
	}
	return &QueueState{Running: ids(raw.Running), Pending: ids(raw.Pending)}, nil
}

// Interrupt stops a running prompt (only that one: another client's
// prompt keeps running).
func (c *Client) Interrupt(ctx context.Context, promptID string) error {
	return c.do(ctx, "POST", "/interrupt", nil, map[string]string{"prompt_id": promptID}, nil)
}

// Dequeue removes waiting prompts.
func (c *Client) Dequeue(ctx context.Context, promptIDs []string) error {
	if len(promptIDs) == 0 {
		return nil
	}
	return c.do(ctx, "POST", "/queue", nil, map[string]any{"delete": promptIDs}, nil)
}

// FileRef names an image ComfyUI saved.
type FileRef struct {
	Filename  string `json:"filename"`
	Subfolder string `json:"subfolder"`
	Type      string `json:"type"`
}

// HistoryEntry is what ComfyUI remembers of a finished prompt.
type HistoryEntry struct {
	Done    bool
	Success bool
	Error   string
	// Files are the saved images per node id.
	Files map[string][]FileRef
}

// History returns a finished prompt, or nil while it is not finished.
func (c *Client) History(ctx context.Context, promptID string) (*HistoryEntry, error) {
	var raw map[string]struct {
		Outputs map[string]struct {
			Images []FileRef `json:"images"`
		} `json:"outputs"`
		Status struct {
			StatusStr string            `json:"status_str"`
			Completed bool              `json:"completed"`
			Messages  []json.RawMessage `json:"messages"`
		} `json:"status"`
	}
	if err := c.do(ctx, "GET", "/history/"+url.PathEscape(promptID), nil, nil, &raw); err != nil {
		return nil, err
	}
	h, ok := raw[promptID]
	if !ok {
		return nil, nil
	}
	e := &HistoryEntry{Done: true, Success: h.Status.StatusStr != "error", Files: map[string][]FileRef{}}
	for id, o := range h.Outputs {
		e.Files[id] = o.Images
	}
	if !e.Success {
		e.Error = "ComfyUI reported an error"
		for _, m := range h.Status.Messages {
			var pair []json.RawMessage
			if json.Unmarshal(m, &pair) != nil || len(pair) != 2 {
				continue
			}
			var kind string
			_ = json.Unmarshal(pair[0], &kind)
			if kind == "execution_error" {
				var d execError
				if json.Unmarshal(pair[1], &d) == nil {
					e.Error = d.text()
				}
			}
		}
	}
	return e, nil
}

// View downloads a saved image.
func (c *Client) View(ctx context.Context, f FileRef) ([]byte, error) {
	q := url.Values{"filename": {f.Filename}, "subfolder": {f.Subfolder}, "type": {f.Type}}
	var data []byte
	err := c.do(ctx, "GET", "/view", q, nil, &data)
	return data, err
}

type execError struct {
	PromptID         string `json:"prompt_id"`
	NodeID           string `json:"node_id"`
	NodeType         string `json:"node_type"`
	ExceptionMessage string `json:"exception_message"`
	ExceptionType    string `json:"exception_type"`
}

func (e execError) text() string {
	msg := strings.TrimSpace(e.ExceptionMessage)
	if msg == "" {
		msg = cmpOr(e.ExceptionType, "an error")
	}
	if e.NodeType != "" {
		return fmt.Sprintf("%s failed: %s", e.NodeType, truncate(msg, 400))
	}
	return truncate(msg, 400)
}
