// Package comfy runs ComfyUI workflows: it reads workflows in ComfyUI's API
// format, applies input overrides and sweeps, queues prompts over HTTP and
// receives the images over the websocket.
package comfy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Workflow is a ComfyUI workflow in the API format (ComfyUI's "Export
// (API)"): node id → {inputs, class_type, _meta: {title}}. An input is a
// literal value or a link to another node's output ([node id, index]).
// Key order is kept, so a saved workflow reads like ComfyUI's export.
type Workflow struct {
	ids   []string // numeric order
	nodes map[string]*node
}

type node struct {
	id     string
	class  string
	title  string // _meta.title, or the class when untitled
	fields *object
	inputs *object
}

// object is a JSON object that keeps its key order.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func (o *object) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) clone() *object {
	c := &object{keys: slices.Clone(o.keys), vals: make(map[string]json.RawMessage, len(o.vals))}
	for k, v := range o.vals {
		c.vals[k] = v
	}
	return c
}

func parseObject(data []byte) (*object, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errNotObject
	}
	o := &object{vals: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o.set(key, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("unexpected data after the JSON object")
	}
	return o, nil
}

var errNotObject = errors.New("not a JSON object")

// ErrEditorFormat reports a workflow saved in the editor format rather
// than the API format.
var ErrEditorFormat = errors.New(`this is a workflow in ComfyUI's editor format; export it in the API format instead ` +
	`(in ComfyUI: Workflow → Export (API))`)

// Parse reads an API-format workflow. A /prompt request body
// ({"prompt": {...}}) is accepted too.
func Parse(data []byte) (*Workflow, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errors.New("the workflow is empty")
	}
	top, err := parseObject(data)
	if err != nil {
		if errors.Is(err, errNotObject) {
			return nil, errors.New("the workflow must be a JSON object of nodes")
		}
		return nil, fmt.Errorf("the workflow is not valid JSON: %v", err)
	}
	if _, ok := top.vals["nodes"]; ok {
		if _, ok := top.vals["links"]; ok {
			return nil, ErrEditorFormat
		}
	}
	if p, ok := top.vals["prompt"]; ok && len(top.keys) <= 4 {
		if inner, err := parseObject(p); err == nil {
			top = inner
		}
	}
	w := &Workflow{nodes: map[string]*node{}}
	for _, id := range top.keys {
		n, err := parseNode(id, top.vals[id])
		if err != nil {
			return nil, err
		}
		w.nodes[id] = n
		w.ids = append(w.ids, id)
	}
	if len(w.ids) == 0 {
		return nil, errors.New("the workflow has no nodes")
	}
	slices.SortStableFunc(w.ids, compareIDs)
	return w, nil
}

func parseNode(id string, raw json.RawMessage) (*node, error) {
	fields, err := parseObject(raw)
	if err != nil {
		return nil, fmt.Errorf("node %s is not an object: this is not an API-format workflow", id)
	}
	n := &node{id: id, fields: fields}
	if err := json.Unmarshal(fields.vals["class_type"], &n.class); err != nil || n.class == "" {
		return nil, fmt.Errorf("node %s has no class_type: this is not an API-format workflow", id)
	}
	if in, ok := fields.vals["inputs"]; ok {
		if n.inputs, err = parseObject(in); err != nil {
			return nil, fmt.Errorf("node %s: its inputs are not an object", id)
		}
	} else {
		n.inputs = &object{vals: map[string]json.RawMessage{}}
		fields.set("inputs", json.RawMessage("{}"))
	}
	var meta struct {
		Title string `json:"title"`
	}
	if m, ok := fields.vals["_meta"]; ok {
		_ = json.Unmarshal(m, &meta)
	}
	n.title = strings.TrimSpace(meta.Title)
	if n.title == "" {
		n.title = n.class
	}
	return n, nil
}

// compareIDs orders node ids numerically ("3" < "10"), others after.
func compareIDs(a, b string) int {
	na, ea := strconv.ParseFloat(strings.ReplaceAll(a, ":", "."), 64)
	nb, eb := strconv.ParseFloat(strings.ReplaceAll(b, ":", "."), 64)
	switch {
	case ea == nil && eb == nil && na != nb:
		if na < nb {
			return -1
		}
		return 1
	case ea == nil && eb != nil:
		return -1
	case ea != nil && eb == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// Clone returns an independent copy.
func (w *Workflow) Clone() *Workflow {
	c := &Workflow{ids: slices.Clone(w.ids), nodes: make(map[string]*node, len(w.nodes))}
	for id, n := range w.nodes {
		cn := *n
		cn.fields = n.fields.clone()
		cn.inputs = n.inputs.clone()
		c.nodes[id] = &cn
	}
	return c
}

// JSON renders the workflow indented, like ComfyUI's export. The result is
// canonical: the same workflow always renders the same bytes.
func (w *Workflow) JSON() []byte {
	var out bytes.Buffer
	_ = json.Indent(&out, w.Compact(), "", "  ")
	return out.Bytes()
}

// Compact renders the workflow without whitespace (for queueing).
func (w *Workflow) Compact() []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, id := range w.ids {
		if i > 0 {
			b.WriteByte(',')
		}
		writeString(&b, id)
		b.WriteByte(':')
		n := w.nodes[id]
		b.WriteByte('{')
		for j, k := range n.fields.keys {
			if j > 0 {
				b.WriteByte(',')
			}
			writeString(&b, k)
			b.WriteByte(':')
			if k == "inputs" {
				writeObject(&b, n.inputs)
			} else {
				writeCompact(&b, n.fields.vals[k])
			}
		}
		b.WriteByte('}')
	}
	b.WriteByte('}')
	return b.Bytes()
}

func writeObject(b *bytes.Buffer, o *object) {
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		writeString(b, k)
		b.WriteByte(':')
		writeCompact(b, o.vals[k])
	}
	b.WriteByte('}')
}

func writeCompact(b *bytes.Buffer, v json.RawMessage) {
	if err := json.Compact(b, v); err != nil {
		b.WriteString("null")
	}
}

func writeString(b *bytes.Buffer, s string) {
	js, _ := marshal(s)
	b.Write(js)
}

// marshal encodes v without escaping <, > and & (prompts often hold them).
func marshal(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// isLink reports whether an input value connects to another node's
// output: ["node id", output index].
func isLink(v json.RawMessage) bool {
	v = bytes.TrimSpace(v)
	if len(v) == 0 || v[0] != '[' {
		return false
	}
	var arr []any
	if json.Unmarshal(v, &arr) != nil || len(arr) != 2 {
		return false
	}
	_, s := arr[0].(string)
	_, n := arr[1].(float64)
	return s && n
}

// decode turns a literal into a Go value, keeping numbers exact.
func decode(v json.RawMessage) any {
	dec := json.NewDecoder(bytes.NewReader(v))
	dec.UseNumber()
	var out any
	if dec.Decode(&out) != nil {
		return nil
	}
	return out
}

// isInteger reports whether a literal is a whole number.
func isInteger(v json.RawMessage) bool {
	s := string(bytes.TrimSpace(v))
	if s == "" {
		return false
	}
	_, err := strconv.ParseInt(s, 10, 64)
	if err == nil {
		return true
	}
	_, err = strconv.ParseUint(s, 10, 64)
	return err == nil
}

// IsSeedInput reports whether an input looks like a sampler seed.
func IsSeedInput(name string, v json.RawMessage) bool {
	n := strings.ToLower(name)
	return (n == "seed" || n == "noise_seed" || strings.HasSuffix(n, "_seed")) && isInteger(v)
}

// Output kinds: how a node hands its images back.
const (
	OutputWebsocket = "websocket" // sent over the websocket (Send Image (WebSocket))
	OutputFile      = "file"      // saved by ComfyUI and downloaded (Save Image)
)

// outputKind classifies a node class.
func outputKind(class string) string {
	c := strings.ToLower(class)
	switch {
	case strings.Contains(c, "websocket") && (strings.Contains(c, "send") || strings.Contains(c, "save")):
		return OutputWebsocket
	case c == "saveimage" || strings.Contains(c, "saveimage") || strings.Contains(c, "image save"):
		return OutputFile
	}
	return ""
}

// Outputs reports how the workflow returns images: websocket nodes win
// (images saved to disk too are then ignored), then Save Image nodes;
// "" when it has neither.
func (w *Workflow) Outputs() (kind string, ids []string) {
	for _, want := range []string{OutputWebsocket, OutputFile} {
		for _, id := range w.ids {
			if outputKind(w.nodes[id].class) == want {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			return want, ids
		}
	}
	return "", nil
}

// Node describes a workflow node for choosing overrides.
type Node struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ClassType string `json:"classType"`
	// Ref names the node in overrides: its title, or "#id" when another
	// node has the same title.
	Ref string `json:"ref"`
	// Inputs are the node's literal inputs (links to other nodes are
	// not listed: they cannot be overridden).
	Inputs []Input `json:"inputs"`
	// Output is how the node returns images, if it does ("websocket" or
	// "file").
	Output string `json:"output,omitempty"`
}

// Input is a literal node input and its value in the workflow.
type Input struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
	Seed  bool   `json:"seed,omitempty"`
}

// Nodes lists the nodes in id order.
func (w *Workflow) Nodes() []Node {
	count := map[string]int{}
	for _, n := range w.nodes {
		count[n.title]++
	}
	out := make([]Node, 0, len(w.ids))
	for _, id := range w.ids {
		n := w.nodes[id]
		nd := Node{ID: id, Title: n.title, ClassType: n.class, Ref: n.title, Inputs: []Input{}, Output: outputKind(n.class)}
		if count[n.title] > 1 {
			nd.Ref = "#" + id
		}
		for _, k := range n.inputs.keys {
			v := n.inputs.vals[k]
			if isLink(v) {
				continue
			}
			nd.Inputs = append(nd.Inputs, Input{Name: k, Value: decode(v), Seed: IsSeedInput(k, v)})
		}
		out = append(out, nd)
	}
	return out
}

// Problems lists what may stop the workflow from working here: duplicate
// titles, and a missing image output.
func (w *Workflow) Problems() []string {
	var out []string
	if kind, _ := w.Outputs(); kind == "" {
		out = append(out, "The workflow has no node that returns images: add a “Send Image (WebSocket)” node "+
			"(from comfyui-tooling-nodes) or a “Save Image” node.")
	}
	byTitle := map[string][]string{}
	for _, id := range w.ids {
		t := w.nodes[id].title
		byTitle[t] = append(byTitle[t], id)
	}
	var dups []string
	for _, id := range w.ids {
		t := w.nodes[id].title
		if ids := byTitle[t]; len(ids) > 1 && ids[0] == id {
			dups = append(dups, fmt.Sprintf("“%s” (#%s)", t, strings.Join(ids, ", #")))
		}
	}
	if len(dups) > 0 {
		out = append(out, "Several nodes share a title, so overrides name them by id, which may change when the workflow is "+
			"exported again. Give them distinct titles in ComfyUI: "+strings.Join(dups, "; ")+".")
	}
	return out
}

// Find resolves a node reference: "#id", or a title (exact, else ignoring
// case when that is unambiguous).
func (w *Workflow) Find(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("no node given")
	}
	if id, ok := strings.CutPrefix(ref, "#"); ok {
		if _, ok := w.nodes[id]; ok {
			return id, nil
		}
		return "", fmt.Errorf("the workflow has no node #%s", id)
	}
	var exact, folded []string
	for _, id := range w.ids {
		t := w.nodes[id].title
		if t == ref {
			exact = append(exact, id)
		} else if strings.EqualFold(t, ref) {
			folded = append(folded, id)
		}
	}
	switch {
	case len(exact) == 1:
		return exact[0], nil
	case len(exact) > 1:
		return "", fmt.Errorf("several nodes are titled “%s” (#%s): give them distinct titles in ComfyUI, or name one by id",
			ref, strings.Join(exact, ", #"))
	case len(folded) == 1:
		return folded[0], nil
	}
	return "", fmt.Errorf("the workflow has no node titled “%s”", ref)
}

// NodeLabel is a node's title for messages.
func (w *Workflow) NodeLabel(id string) string {
	if n, ok := w.nodes[id]; ok {
		return n.title
	}
	return "#" + id
}

// ClassOf returns a node's class.
func (w *Workflow) ClassOf(id string) string {
	if n, ok := w.nodes[id]; ok {
		return n.class
	}
	return ""
}

// Classes lists the node classes used, sorted.
func (w *Workflow) Classes() []string {
	var out []string
	for _, n := range w.nodes {
		if !slices.Contains(out, n.class) {
			out = append(out, n.class)
		}
	}
	slices.Sort(out)
	return out
}

// literal returns a node input's current value, checking it can be
// overridden.
func (w *Workflow) literal(ref, input string) (id string, v json.RawMessage, err error) {
	id, err = w.Find(ref)
	if err != nil {
		return "", nil, err
	}
	n := w.nodes[id]
	v, ok := n.inputs.vals[input]
	if !ok {
		return "", nil, fmt.Errorf("node “%s” has no input “%s”", n.title, input)
	}
	if isLink(v) {
		return "", nil, fmt.Errorf("input “%s” of node “%s” is connected to another node, so it has no value to override", input, n.title)
	}
	return id, v, nil
}

// Value returns the value of a node input.
func (w *Workflow) Value(ref, input string) (any, error) {
	_, v, err := w.literal(ref, input)
	if err != nil {
		return nil, err
	}
	return decode(v), nil
}

// Set changes a literal input, converting the value to the type of the
// current one where that is unambiguous (a number typed as text, say).
func (w *Workflow) Set(ref, input string, value any) error {
	id, cur, err := w.literal(ref, input)
	if err != nil {
		return err
	}
	v, err := convert(cur, value)
	if err != nil {
		return fmt.Errorf("%s › %s: %v", w.nodes[id].title, input, err)
	}
	raw, err := marshal(v)
	if err != nil {
		return err
	}
	w.nodes[id].inputs.set(input, raw)
	return nil
}

func convert(cur json.RawMessage, value any) (any, error) {
	c := bytes.TrimSpace(cur)
	if len(c) == 0 {
		return value, nil
	}
	switch c[0] {
	case '"':
		switch v := value.(type) {
		case string:
			return v, nil
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64), nil
		case json.Number:
			return v.String(), nil
		case bool:
			return strconv.FormatBool(v), nil
		}
	case 't', 'f':
		switch v := value.(type) {
		case bool:
			return v, nil
		case string:
			b, err := strconv.ParseBool(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("“%s” is not true or false", v)
			}
			return b, nil
		}
		return nil, errors.New("the value must be true or false")
	default:
		if c[0] == '-' || (c[0] >= '0' && c[0] <= '9') {
			switch v := value.(type) {
			case float64:
				return v, nil
			case json.Number:
				return v, nil
			case string:
				s := strings.TrimSpace(v)
				if _, err := strconv.ParseFloat(s, 64); err != nil {
					return nil, fmt.Errorf("“%s” is not a number", v)
				}
				return json.Number(s), nil
			case bool:
				return nil, errors.New("the value must be a number")
			}
		}
	}
	if value == nil {
		return nil, errors.New("no value given")
	}
	return value, nil
}
