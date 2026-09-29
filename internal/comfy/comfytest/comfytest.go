// Package comfytest is a fake ComfyUI server for tests: it validates
// prompts a little, "runs" them one at a time, and sends messages and
// images over the websocket like ComfyUI does.
package comfytest

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Models are the checkpoints the fake server has.
var Models = []string{"sdxl_base.safetensors", "anime.safetensors"}

// Server is a fake ComfyUI.
type Server struct {
	*httptest.Server
	// StepDelay is how long each sampler step takes.
	StepDelay time.Duration
	// Fail makes nodes of this class fail when run.
	Fail string

	mu       sync.Mutex
	clients  map[string]*client
	queue    []*job
	running  *job
	history  map[string]*hist
	files    map[string][]byte
	last     string
	received []Received
	wake     chan struct{}
	ctx      context.Context
	stop     context.CancelFunc
}

// Received is a prompt the server accepted.
type Received struct {
	ID       string
	ClientID string
	Prompt   map[string]Node
}

// Node is a node of a received prompt.
type Node struct {
	ClassType string         `json:"class_type"`
	Inputs    map[string]any `json:"inputs"`
}

type client struct {
	ws       *websocket.Conn
	metadata bool
}

type job struct {
	id          string
	clientID    string
	prompt      map[string]Node
	compact     string
	interrupted bool
}

type hist struct {
	outputs map[string][]map[string]string
	status  string
	message string
}

// New starts a fake server.
func New() *Server {
	ctx, stop := context.WithCancel(context.Background())
	s := &Server{clients: map[string]*client{}, history: map[string]*hist{}, files: map[string][]byte{},
		wake: make(chan struct{}, 1), ctx: ctx, stop: stop}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /system_stats", s.systemStats)
	mux.HandleFunc("GET /object_info/{class}", s.objectInfo)
	mux.HandleFunc("GET /queue", s.getQueue)
	mux.HandleFunc("POST /queue", s.postQueue)
	mux.HandleFunc("POST /prompt", s.postPrompt)
	mux.HandleFunc("POST /interrupt", s.interrupt)
	mux.HandleFunc("GET /history/{id}", s.getHistory)
	mux.HandleFunc("GET /view", s.view)
	mux.HandleFunc("GET /ws", s.websocket)
	s.Server = httptest.NewServer(mux)
	go s.worker()
	return s
}

// Close stops the server.
func (s *Server) Close() {
	s.stop()
	s.mu.Lock()
	for _, c := range s.clients {
		c.ws.CloseNow()
	}
	s.mu.Unlock()
	s.Server.Close()
}

// Received returns the prompts accepted so far.
func (s *Server) Received() []Received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.received)
}

// DropConnections closes every websocket (to test reconnecting).
func (s *Server) DropConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.clients {
		c.ws.CloseNow()
		delete(s.clients, id)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) systemStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"system":  map[string]any{"os": "linux", "comfyui_version": "0.99.0-fake", "python_version": "3.12.3 (main)", "pytorch_version": "2.9.0"},
		"devices": []any{map[string]any{"name": "cuda:0 Fake GPU : cudaMallocAsync", "type": "cuda", "vram_total": 16 << 30, "vram_free": 12 << 30}},
	})
}

var nodeDefs = map[string]any{
	"KSampler": map[string]any{"input": map[string]any{"required": map[string]any{
		"model":        []any{"MODEL"},
		"seed":         []any{"INT", map[string]any{"default": 0, "min": 0, "max": 18446744073709551615.0, "control_after_generate": true}},
		"steps":        []any{"INT", map[string]any{"default": 20, "min": 1, "max": 10000}},
		"cfg":          []any{"FLOAT", map[string]any{"default": 8.0, "min": 0.0, "max": 100.0, "step": 0.1}},
		"sampler_name": []any{[]any{"euler", "euler_ancestral", "dpmpp_2m"}},
		"scheduler":    []any{[]any{"normal", "karras"}},
		"positive":     []any{"CONDITIONING"},
		"negative":     []any{"CONDITIONING"},
		"latent_image": []any{"LATENT"},
		"denoise":      []any{"FLOAT", map[string]any{"default": 1.0, "min": 0.0, "max": 1.0, "step": 0.01}},
	}}, "input_order": map[string]any{"required": []any{"model", "seed", "steps", "cfg", "sampler_name", "scheduler", "positive", "negative", "latent_image", "denoise"}},
		"display_name": "KSampler", "output_node": false},
	"CheckpointLoaderSimple": map[string]any{"input": map[string]any{"required": map[string]any{
		"ckpt_name": []any{"COMBO", map[string]any{"options": Models}}}}, "display_name": "Load Checkpoint"},
	"CLIPTextEncode": map[string]any{"input": map[string]any{"required": map[string]any{
		"text": []any{"STRING", map[string]any{"multiline": true}}, "clip": []any{"CLIP"}}}, "display_name": "CLIP Text Encode"},
	"PrimitiveInt": map[string]any{"input": map[string]any{"required": map[string]any{
		"value": []any{"INT", map[string]any{"min": -9223372036854775807, "max": 9223372036854775807, "control_after_generate": "fixed"}}}}, "display_name": "Int"},
	"EmptyLatentImage": map[string]any{"input": map[string]any{"required": map[string]any{
		"width": []any{"INT"}, "height": []any{"INT"}, "batch_size": []any{"INT"}}}, "display_name": "Empty Latent Image"},
	"VAEDecode":              map[string]any{"input": map[string]any{"required": map[string]any{"samples": []any{"LATENT"}, "vae": []any{"VAE"}}}},
	"CLIPSetLastLayer":       map[string]any{"input": map[string]any{"required": map[string]any{"stop_at_clip_layer": []any{"INT"}, "clip": []any{"CLIP"}}}},
	"ETN_SendImageWebSocket": map[string]any{"input": map[string]any{"required": map[string]any{"images": []any{"IMAGE"}, "format": []any{"COMBO", map[string]any{"options": []any{"PNG", "JPEG"}}}}}, "display_name": "Send Image (WebSocket)", "output_node": true},
	"SaveImage":              map[string]any{"input": map[string]any{"required": map[string]any{"images": []any{"IMAGE"}, "filename_prefix": []any{"STRING"}}}, "display_name": "Save Image", "output_node": true},
}

func (s *Server) objectInfo(w http.ResponseWriter, r *http.Request) {
	class := r.PathValue("class")
	def, ok := nodeDefs[class]
	if !ok {
		writeJSON(w, 200, map[string]any{})
		return
	}
	writeJSON(w, 200, map[string]any{class: def})
}

func (s *Server) getQueue(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := func(j *job) []any { return []any{0, j.id, map[string]any{}, map[string]any{}, []any{}} }
	running, pending := []any{}, []any{}
	if s.running != nil {
		running = append(running, entry(s.running))
	}
	for _, j := range s.queue {
		pending = append(pending, entry(j))
	}
	writeJSON(w, 200, map[string]any{"queue_running": running, "queue_pending": pending})
}

func (s *Server) postQueue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Delete []string `json:"delete"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	s.mu.Lock()
	s.queue = slices.DeleteFunc(s.queue, func(j *job) bool { return slices.Contains(req.Delete, j.id) })
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{})
}

func (s *Server) interrupt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PromptID string `json:"prompt_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	s.mu.Lock()
	if s.running != nil && (req.PromptID == "" || req.PromptID == s.running.id) {
		s.running.interrupted = true
	}
	s.mu.Unlock()
	writeJSON(w, 200, map[string]any{})
}

func (s *Server) postPrompt(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt   map[string]Node `json:"prompt"`
		ClientID string          `json:"client_id"`
		PromptID string          `json:"prompt_id"`
	}
	raw := new(bytes.Buffer)
	raw.ReadFrom(r.Body)
	if err := json.Unmarshal(raw.Bytes(), &req); err != nil || req.Prompt == nil {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"type": "no_prompt", "message": "No prompt provided"}, "node_errors": map[string]any{}})
		return
	}
	nodeErrors := map[string]any{}
	for id, n := range req.Prompt {
		if _, ok := nodeDefs[n.ClassType]; !ok {
			writeJSON(w, 400, map[string]any{"error": map[string]any{"type": "invalid_prompt",
				"message": fmt.Sprintf("Cannot execute because node %s does not exist.", n.ClassType), "details": "Node ID '#" + id + "'"},
				"node_errors": map[string]any{}})
			return
		}
		if n.ClassType == "CheckpointLoaderSimple" {
			name, _ := n.Inputs["ckpt_name"].(string)
			if !slices.Contains(Models, name) {
				nodeErrors[id] = map[string]any{"errors": []any{map[string]any{"type": "value_not_in_list", "message": "Value not in list",
					"details": fmt.Sprintf("ckpt_name: '%s' not in %v", name, Models)}}, "class_type": n.ClassType}
			}
		}
	}
	if len(nodeErrors) > 0 {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"type": "prompt_outputs_failed_validation",
			"message": "Prompt outputs failed validation", "details": ""}, "node_errors": nodeErrors})
		return
	}
	id := req.PromptID
	if id == "" {
		id = fmt.Sprintf("p%d", time.Now().UnixNano())
	}
	var compact bytes.Buffer
	js, _ := json.Marshal(req.Prompt)
	compact.Write(js)
	s.mu.Lock()
	s.queue = append(s.queue, &job{id: id, clientID: req.ClientID, prompt: req.Prompt, compact: compact.String()})
	s.received = append(s.received, Received{ID: id, ClientID: req.ClientID, Prompt: req.Prompt})
	n := len(s.received)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	writeJSON(w, 200, map[string]any{"prompt_id": id, "number": n, "node_errors": map[string]any{}})
}

func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	h, ok := s.history[id]
	s.mu.Unlock()
	if !ok {
		writeJSON(w, 200, map[string]any{})
		return
	}
	outputs := map[string]any{}
	for node, imgs := range h.outputs {
		outputs[node] = map[string]any{"images": imgs}
	}
	msgs := []any{}
	if h.status == "error" {
		msgs = append(msgs, []any{"execution_error", map[string]any{"prompt_id": id, "node_type": "KSampler", "exception_message": h.message}})
	}
	writeJSON(w, 200, map[string]any{id: map[string]any{"outputs": outputs,
		"status": map[string]any{"status_str": h.status, "completed": h.status == "success", "messages": msgs}}})
}

func (s *Server) view(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	data, ok := s.files[r.URL.Query().Get("filename")]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(data)
}

func (s *Server) websocket(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	id := r.URL.Query().Get("clientId")
	c := &client{ws: ws}
	s.mu.Lock()
	s.clients[id] = c
	s.mu.Unlock()
	ctx := r.Context()
	s.send(id, "status", map[string]any{"status": map[string]any{"exec_info": map[string]any{"queue_remaining": 0}}, "sid": id})
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			break
		}
		var m struct {
			Type string `json:"type"`
			Data struct {
				Metadata bool `json:"supports_preview_metadata"`
			} `json:"data"`
		}
		if typ == websocket.MessageText && json.Unmarshal(data, &m) == nil && m.Type == "feature_flags" {
			s.mu.Lock()
			c.metadata = m.Data.Metadata
			s.mu.Unlock()
		}
	}
	s.mu.Lock()
	if s.clients[id] == c {
		delete(s.clients, id)
	}
	s.mu.Unlock()
}

func (s *Server) send(clientID, typ string, data any) {
	s.mu.Lock()
	c := s.clients[clientID]
	s.mu.Unlock()
	if c == nil {
		return
	}
	js, _ := json.Marshal(map[string]any{"type": typ, "data": data})
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	c.ws.Write(ctx, websocket.MessageText, js)
}

func (s *Server) sendBinary(clientID string, event uint32, payload []byte) {
	s.mu.Lock()
	c := s.clients[clientID]
	s.mu.Unlock()
	if c == nil {
		return
	}
	msg := binary.BigEndian.AppendUint32(nil, event)
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	c.ws.Write(ctx, websocket.MessageBinary, append(msg, payload...))
}

func (s *Server) worker() {
	for {
		s.mu.Lock()
		var j *job
		if len(s.queue) > 0 {
			j, s.queue = s.queue[0], s.queue[1:]
			s.running = j
		}
		s.mu.Unlock()
		if j == nil {
			select {
			case <-s.wake:
				continue
			case <-s.ctx.Done():
				return
			case <-time.After(20 * time.Millisecond):
				continue
			}
		}
		s.execute(j)
		s.mu.Lock()
		s.running = nil
		s.mu.Unlock()
	}
}

// resolve follows links to a literal value.
func resolve(p map[string]Node, v any) any {
	for range 10 {
		link, ok := v.([]any)
		if !ok || len(link) != 2 {
			return v
		}
		id, _ := link[0].(string)
		n, ok := p[id]
		if !ok {
			return nil
		}
		v = n.Inputs["value"]
	}
	return v
}

func num(v any, def float64) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err == nil {
			return f
		}
	}
	return def
}

func (s *Server) execute(j *job) {
	pid := map[string]any{"prompt_id": j.id}
	s.send(j.clientID, "execution_start", map[string]any{"prompt_id": j.id, "timestamp": time.Now().UnixMilli()})
	ids := make([]string, 0, len(j.prompt))
	for id := range j.prompt {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int { x, _ := strconv.Atoi(a); y, _ := strconv.Atoi(b); return x - y })
	s.mu.Lock()
	cached := s.last == j.compact
	s.last = j.compact
	s.mu.Unlock()
	if cached {
		s.send(j.clientID, "execution_cached", map[string]any{"nodes": ids, "prompt_id": j.id})
		s.send(j.clientID, "execution_success", pid)
		s.send(j.clientID, "executing", map[string]any{"node": nil, "prompt_id": j.id})
		s.record(j.id, &hist{status: "success", outputs: map[string][]map[string]string{}})
		return
	}
	s.send(j.clientID, "execution_cached", map[string]any{"nodes": []string{}, "prompt_id": j.id})

	// The picture depends on the sampler settings and the model.
	var w, h, batch = 64, 64, 1
	h32 := fnv.New32a()
	for _, n := range j.prompt {
		switch n.ClassType {
		case "EmptyLatentImage":
			w = int(num(resolve(j.prompt, n.Inputs["width"]), 64))
			h = int(num(resolve(j.prompt, n.Inputs["height"]), 64))
			batch = max(1, int(num(resolve(j.prompt, n.Inputs["batch_size"]), 1)))
		}
	}
	for _, id := range ids {
		n := j.prompt[id]
		if n.ClassType == "KSampler" || n.ClassType == "CheckpointLoaderSimple" || n.ClassType == "CLIPTextEncode" {
			js, _ := json.Marshal(n.Inputs)
			h32.Write(js)
		}
	}
	sum := h32.Sum32()
	outputs := map[string][]map[string]string{}
	for _, id := range ids {
		n := j.prompt[id]
		s.send(j.clientID, "executing", map[string]any{"node": id, "display_node": id, "prompt_id": j.id})
		if n.ClassType == s.Fail {
			s.send(j.clientID, "execution_error", map[string]any{"prompt_id": j.id, "node_id": id, "node_type": n.ClassType,
				"exception_message": "CUDA out of memory (fake)", "exception_type": "torch.OutOfMemoryError"})
			s.send(j.clientID, "executing", map[string]any{"node": nil, "prompt_id": j.id})
			s.record(j.id, &hist{status: "error", message: "CUDA out of memory (fake)"})
			return
		}
		switch n.ClassType {
		case "KSampler":
			steps := int(num(resolve(j.prompt, n.Inputs["steps"]), 20))
			for i := 1; i <= steps; i++ {
				time.Sleep(s.StepDelay)
				s.mu.Lock()
				stop := j.interrupted
				meta := s.clients[j.clientID] != nil && s.clients[j.clientID].metadata
				s.mu.Unlock()
				if stop {
					s.send(j.clientID, "execution_interrupted", map[string]any{"prompt_id": j.id, "node_id": id, "node_type": n.ClassType})
					s.send(j.clientID, "executing", map[string]any{"node": nil, "prompt_id": j.id})
					s.record(j.id, &hist{status: "error", message: "interrupted"})
					return
				}
				s.send(j.clientID, "progress", map[string]any{"value": i, "max": steps, "prompt_id": j.id, "node": id})
				if i%5 == 0 {
					prev := encode(16, 16, sum^uint32(i))
					if meta {
						md, _ := json.Marshal(map[string]any{"node_id": id, "prompt_id": j.id, "image_type": "image/png"})
						s.sendBinary(j.clientID, 4, append(binary.BigEndian.AppendUint32(nil, uint32(len(md))), append(md, prev...)...))
					} else {
						s.sendBinary(j.clientID, 1, append(binary.BigEndian.AppendUint32(nil, 2), prev...))
					}
				}
			}
		case "ETN_SendImageWebSocket":
			for b := range batch {
				img := encode(w, h, sum+uint32(b))
				s.sendBinary(j.clientID, 1, append(binary.BigEndian.AppendUint32(nil, 2), img...))
			}
		case "SaveImage":
			for b := range batch {
				name := fmt.Sprintf("ComfyUI_%s_%d.png", j.id, b)
				s.mu.Lock()
				s.files[name] = encode(w, h, sum+uint32(b))
				s.mu.Unlock()
				outputs[id] = append(outputs[id], map[string]string{"filename": name, "subfolder": "", "type": "output"})
			}
			s.send(j.clientID, "executed", map[string]any{"node": id, "display_node": id, "prompt_id": j.id,
				"output": map[string]any{"images": outputs[id]}})
		}
	}
	s.send(j.clientID, "execution_success", pid)
	s.send(j.clientID, "executing", map[string]any{"node": nil, "prompt_id": j.id})
	s.record(j.id, &hist{status: "success", outputs: outputs})
}

func (s *Server) record(id string, h *hist) {
	s.mu.Lock()
	s.history[id] = h
	s.mu.Unlock()
}

// encode draws a w×h PNG whose colours depend on seed.
func encode(w, h int, seed uint32) []byte {
	w, h = max(1, min(w, 512)), max(1, min(h, 512))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r, g, b := uint8(seed), uint8(seed>>8), uint8(seed>>16)
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{r + uint8(x*255/w), g + uint8(y*255/h), b, 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// Workflow is a small API-format text-to-image workflow for the fake
// server, shaped like ComfyUI's own export.
const Workflow = `{
  "3": {"inputs": {"seed": 1234, "steps": 10, "cfg": 5, "sampler_name": "euler", "scheduler": "karras", "denoise": 1,
        "model": ["4", 0], "positive": ["6", 0], "negative": ["7", 0], "latent_image": ["30", 0]},
        "class_type": "KSampler", "_meta": {"title": "KSampler"}},
  "4": {"inputs": {"ckpt_name": "sdxl_base.safetensors"}, "class_type": "CheckpointLoaderSimple", "_meta": {"title": "Checkpoint"}},
  "6": {"inputs": {"text": "a cat", "clip": ["4", 1]}, "class_type": "CLIPTextEncode", "_meta": {"title": "Positive"}},
  "7": {"inputs": {"text": "blurry", "clip": ["4", 1]}, "class_type": "CLIPTextEncode", "_meta": {"title": "Negative"}},
  "8": {"inputs": {"samples": ["3", 0], "vae": ["4", 2]}, "class_type": "VAEDecode", "_meta": {"title": "VAE Decode"}},
  "27": {"inputs": {"value": 64}, "class_type": "PrimitiveInt", "_meta": {"title": "Width"}},
  "28": {"inputs": {"value": 48}, "class_type": "PrimitiveInt", "_meta": {"title": "Height"}},
  "29": {"inputs": {"format": "PNG", "images": ["8", 0]}, "class_type": "ETN_SendImageWebSocket", "_meta": {"title": "Send Image (WebSocket)"}},
  "30": {"inputs": {"width": ["27", 0], "height": ["28", 0], "batch_size": ["31", 0]}, "class_type": "EmptyLatentImage", "_meta": {"title": "Empty Latent Image"}},
  "31": {"inputs": {"value": 1}, "class_type": "PrimitiveInt", "_meta": {"title": "Batches"}}
}`

// SaveImageWorkflow is Workflow with a Save Image node instead of the
// websocket one.
var SaveImageWorkflow = strings.Replace(Workflow,
	`"29": {"inputs": {"format": "PNG", "images": ["8", 0]}, "class_type": "ETN_SendImageWebSocket", "_meta": {"title": "Send Image (WebSocket)"}}`,
	`"29": {"inputs": {"filename_prefix": "ComfyUI", "images": ["8", 0]}, "class_type": "SaveImage", "_meta": {"title": "Save Image"}}`, 1)
