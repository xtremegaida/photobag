package comfy

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Override sets a node input: to Value for every image, or, in a sweep, to
// each value of Sweep in turn.
type Override struct {
	// Node is the node's title, or "#id".
	Node  string `json:"node"`
	Input string `json:"input"`
	Value any    `json:"value,omitempty"`
	Sweep *Sweep `json:"sweep,omitempty"`
}

// Sweep lists the values an override takes: Values, or the numbers From
// to To (inclusive) in steps of Step.
type Sweep struct {
	Values []any  `json:"values,omitempty"`
	Range  *Range `json:"range,omitempty"`
}

// Range is an arithmetic series of numbers.
type Range struct {
	From float64 `json:"from"`
	To   float64 `json:"to"`
	Step float64 `json:"step"`
}

// Seed policies for seed inputs the overrides leave alone.
const (
	// SeedRandom gives each image a new random seed. In a sweep, the n-th
	// image of every combination shares a seed, so the images differ
	// only in the swept values.
	SeedRandom = "random"
	// SeedFixed keeps the workflow's seeds.
	SeedFixed = "fixed"
	// SeedIncrement counts up from the workflow's seed.
	SeedIncrement = "increment"
)

// Limits.
const (
	MaxValues  = 500  // per swept override
	MaxPrompts = 2000 // per generation
	MaxCount   = 500  // images per combination
)

// Applied kinds.
const (
	KindSweep = "sweep" // a swept value
	KindSeed  = "seed"  // a seed chosen by the seed policy
)

// Applied is a value set on a workflow input.
type Applied struct {
	Node  string `json:"node"`
	Input string `json:"input"`
	Value any    `json:"value"`
	// Kind is "sweep" for swept values, "seed" for seeds the seed policy
	// chose, and empty for fixed overrides.
	Kind string `json:"kind,omitempty"`
}

// Dim is a swept override.
type Dim struct {
	Node   string `json:"node"`
	Input  string `json:"input"`
	Values []any  `json:"values"`
}

// MaxRandomSeed bounds random seeds, keeping them exact in JavaScript
// (ComfyUI's own editor stays below 2^50 too).
const MaxRandomSeed = 1 << 50

func seedSwept(dims []Dim, w *Workflow) bool {
	for _, d := range dims {
		if _, v, err := w.literal(d.Node, d.Input); err == nil && IsSeedInput(d.Input, v) {
			return true
		}
	}
	return false
}

func product(dims []Dim) int {
	n := 1
	for _, d := range dims {
		n *= len(d.Values)
		if n > MaxPrompts*10 {
			return n
		}
	}
	return n
}

// pick returns the swept values of a combination; the first dimension
// varies slowest.
func pick(dims []Dim, combo int) []Applied {
	out := make([]Applied, len(dims))
	for i := len(dims) - 1; i >= 0; i-- {
		n := len(dims[i].Values)
		out[i] = Applied{Node: dims[i].Node, Input: dims[i].Input, Value: dims[i].Values[combo%n], Kind: KindSweep}
		combo /= n
	}
	return out
}

var modelExts = []string{".safetensors", ".ckpt", ".pt", ".pth", ".bin", ".gguf", ".sft", ".onnx"}

func isModelSweep(d Dim) bool {
	for _, v := range d.Values {
		s, ok := v.(string)
		if !ok {
			return false
		}
		s = strings.ToLower(s)
		found := false
		for _, e := range modelExts {
			if strings.HasSuffix(s, e) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return len(d.Values) > 0
}

func (s *Sweep) values() ([]any, error) {
	if s.Range != nil {
		return s.Range.values()
	}
	if len(s.Values) == 0 {
		return nil, errors.New("the sweep has no values")
	}
	if len(s.Values) > MaxValues {
		return nil, fmt.Errorf("a sweep can have at most %d values", MaxValues)
	}
	return s.Values, nil
}

// values expands the range. Whether the input takes whole numbers cannot be
// told from the workflow (cfg is often exported as 5), so that is left to
// the caller and ComfyUI.
func (r Range) values() ([]any, error) {
	if !(r.Step > 0) || math.IsInf(r.Step, 0) {
		return nil, errors.New("the step must be above 0")
	}
	if math.IsNaN(r.From) || math.IsNaN(r.To) || math.IsInf(r.From, 0) || math.IsInf(r.To, 0) {
		return nil, errors.New("the range needs a start and an end")
	}
	span := math.Abs(r.To - r.From)
	n := int(math.Floor(span/r.Step+1e-9)) + 1
	if n > MaxValues {
		return nil, fmt.Errorf("that range has %d values; a sweep can have at most %d", n, MaxValues)
	}
	dir := 1.0
	if r.To < r.From {
		dir = -1
	}
	dec := max(decimals(r.From), decimals(r.Step))
	scale := math.Pow(10, float64(dec))
	out := make([]any, 0, n)
	for i := range n {
		out = append(out, math.Round((r.From+dir*float64(i)*r.Step)*scale)/scale)
	}
	return out, nil
}

// decimals counts the digits after the point, up to 6.
func decimals(x float64) int {
	s := strconv.FormatFloat(x, 'f', -1, 64)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return min(len(s)-i-1, 6)
	}
	return 0
}

// Apply returns a copy of the workflow with the values set.
func (w *Workflow) Apply(values []Applied) (*Workflow, error) {
	c := w.Clone()
	for _, a := range values {
		if err := c.Set(a.Node, a.Input, a.Value); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Describe renders applied values for labels: "Steps › value = 28".
func Describe(values []Applied) string {
	parts := make([]string, 0, len(values))
	for _, a := range values {
		parts = append(parts, fmt.Sprintf("%s › %s = %s", a.Node, a.Input, ValueText(a.Value)))
	}
	return strings.Join(parts, ", ")
}

// ValueText renders a value briefly.
func ValueText(v any) string {
	switch x := v.(type) {
	case string:
		if len([]rune(x)) > 40 {
			x = string([]rune(x)[:39]) + "…"
		}
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return "null"
	}
	js, _ := json.Marshal(v)
	return string(js)
}
