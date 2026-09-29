package comfy

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
)

// Item is one prompt of a plan.
type Item struct {
	// Combo indexes the combination of swept values (0 without a sweep).
	Combo int
	// Repeat is the image's number within its combination.
	Repeat int
	// Seed is the seed the policy chose, if it chose one.
	Seed *int64
	// Applied is everything set on the workflow for this prompt.
	Applied []Applied
}

// Plan is the expansion of overrides into prompts.
type Plan struct {
	Items  []Item
	Dims   []Dim
	Combos int
	Count  int
	// SeedInputs are the inputs the seed policy sets ("node › input").
	SeedInputs []string
	Warnings   []string
}

// Expand turns overrides, a count per combination and a seed policy into
// the prompts to run. Swept overrides multiply: every combination of
// their values is made count times. Sweeps over model files vary slowest,
// so each model is loaded once.
func Expand(w *Workflow, overrides []Override, count int, seedMode string, rng *rand.Rand) (*Plan, error) {
	if count < 1 {
		count = 1
	}
	if count > MaxCount {
		return nil, fmt.Errorf("at most %d images per combination", MaxCount)
	}
	switch seedMode {
	case "":
		seedMode = SeedRandom
	case SeedRandom, SeedFixed, SeedIncrement:
	default:
		return nil, fmt.Errorf("unknown seed policy %q", seedMode)
	}
	if rng == nil {
		rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	p := &Plan{Count: count}
	var fixed []Applied
	var dims []Dim
	seen := map[string]bool{}
	for _, o := range overrides {
		id, cur, err := w.literal(o.Node, o.Input)
		if err != nil {
			return nil, err
		}
		key := id + "\x00" + o.Input
		if seen[key] {
			return nil, fmt.Errorf("%s › %s is overridden twice", w.nodes[id].title, o.Input)
		}
		seen[key] = true
		if o.Sweep == nil {
			if _, err := convert(cur, o.Value); err != nil {
				return nil, fmt.Errorf("%s › %s: %v", w.nodes[id].title, o.Input, err)
			}
			fixed = append(fixed, Applied{Node: o.Node, Input: o.Input, Value: o.Value})
			continue
		}
		vals, err := o.Sweep.values()
		if err != nil {
			return nil, fmt.Errorf("%s › %s: %v", w.nodes[id].title, o.Input, err)
		}
		for _, v := range vals {
			if _, err := convert(cur, v); err != nil {
				return nil, fmt.Errorf("%s › %s: %v", w.nodes[id].title, o.Input, err)
			}
		}
		dims = append(dims, Dim{Node: o.Node, Input: o.Input, Values: vals})
	}

	// Model files first: switching models is the slow part of a sweep.
	var models, others []Dim
	for _, d := range dims {
		if isModelSweep(d) {
			models = append(models, d)
		} else {
			others = append(others, d)
		}
	}
	p.Dims = append(models, others...)
	nModel, nOther := product(models), product(others)
	p.Combos = nModel * nOther
	total := p.Combos * count
	if p.Combos > MaxPrompts || total > MaxPrompts {
		return nil, fmt.Errorf("that makes %d images; at most %d can be made at once", p.Combos*count, MaxPrompts)
	}

	// Seed inputs the overrides do not set.
	type seedIn struct {
		ref, input string
		base       int64
	}
	var seeds []seedIn
	for _, nd := range w.Nodes() {
		for _, in := range nd.Inputs {
			if !in.Seed || seen[nd.ID+"\x00"+in.Name] {
				continue
			}
			base, _ := strconv.ParseInt(fmt.Sprint(in.Value), 10, 64)
			seeds = append(seeds, seedIn{ref: nd.Ref, input: in.Name, base: base})
			p.SeedInputs = append(p.SeedInputs, nd.Title+" › "+in.Name)
		}
	}
	if seedMode == SeedFixed {
		p.SeedInputs = nil
	}
	randoms := make([]int64, count)
	for i := range randoms {
		randoms[i] = rng.Int64N(MaxRandomSeed)
	}
	switch {
	case count > 1 && len(seeds) == 0 && !seedSwept(p.Dims, w):
		p.Warnings = append(p.Warnings, "No seed input was found, so the repeats may all be the same image.")
	case count > 1 && seedMode == SeedFixed:
		p.Warnings = append(p.Warnings, "With fixed seeds the repeats are the same image, and ComfyUI skips prompts it has just run.")
	}

	for m := 0; m < nModel; m++ {
		for r := 0; r < count; r++ {
			for o := 0; o < nOther; o++ {
				combo := m*nOther + o
				it := Item{Combo: combo, Repeat: r}
				it.Applied = append(it.Applied, fixed...)
				for _, a := range pick(p.Dims, combo) {
					it.Applied = append(it.Applied, a)
				}
				if seedMode != SeedFixed && len(seeds) > 0 {
					var first int64
					for i, s := range seeds {
						v := randoms[r]
						if seedMode == SeedIncrement {
							v = s.base + int64(r)
						}
						if i == 0 {
							first = v
						}
						it.Applied = append(it.Applied, Applied{Node: s.ref, Input: s.input, Value: v, Kind: KindSeed})
					}
					it.Seed = &first
				}
				if it.Seed == nil {
					it.Seed = overriddenSeed(w, it.Applied)
				}
				p.Items = append(p.Items, it)
			}
		}
	}
	return p, nil
}

// overriddenSeed returns the value an override gives a seed input, so an
// image records its seed however it was set.
func overriddenSeed(w *Workflow, applied []Applied) *int64 {
	for _, a := range applied {
		_, cur, err := w.literal(a.Node, a.Input)
		if err != nil || !IsSeedInput(a.Input, cur) {
			continue
		}
		var s string
		switch v := a.Value.(type) {
		case float64:
			if v != math.Trunc(v) {
				continue
			}
			s = strconv.FormatFloat(v, 'f', 0, 64)
		default:
			s = fmt.Sprint(v)
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return &n
		}
	}
	return nil
}
