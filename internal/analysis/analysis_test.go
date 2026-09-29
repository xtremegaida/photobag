package analysis

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"photobag/internal/bag"
	"photobag/internal/importer"
	"photobag/internal/library"
	"photobag/internal/llm"
	"photobag/internal/llm/llmtest"
	"photobag/internal/query"
	"photobag/internal/tagger/taggertest"
	"photobag/internal/testimg"
)

func TestParseOCR(t *testing.T) {
	for in, want := range map[string]string{
		"NO_TEXT":  "",
		"no_text.": "",
		"There is no visible text in this image.": "",
		"None":                        "",
		"```\nSTOP\nAll way  \n```":   "STOP\nAll way",
		"\"EXIT\"":                    "EXIT",
		"No parking\n\nTow away zone": "No parking\n\nTow away zone",
	} {
		if got := parseOCR(in); got != want {
			t.Errorf("parseOCR(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseCaption(t *testing.T) {
	got, err := parseCaption("Alt text: \"A dog  runs\n on a beach.\"")
	if err != nil || got != "A dog runs on a beach." {
		t.Errorf("caption %q %v", got, err)
	}
	if _, err := parseCaption("  "); err == nil {
		t.Error("empty caption should fail")
	}
}

func TestParseDanbooru(t *testing.T) {
	got, err := parseDanbooru("Tags: 1girl, Long Hair, (smile:1.2), `outdoors`\n- blue sky.\n1girl, rating:general, http://x.y, 2. solo", 10)
	want := "1girl long_hair smile outdoors blue_sky solo"
	if err != nil || strings.Join(got, " ") != want {
		t.Errorf("tags %v %v", got, err)
	}
	got, _ = parseDanbooru(`["a", "b c", "d"]`, 2)
	if strings.Join(got, " ") != "a b_c" {
		t.Errorf("json tags %v", got)
	}
	var pe *ParseError
	if _, err := parseDanbooru("I cannot tag this image because it is too dark to see anything at all", 5); !errors.As(err, &pe) {
		t.Errorf("sentence should not be a tag: %v", err)
	}
	s := Defaults()
	s.Danbooru.Prefix = "db:"
	if n := s.danbooruNames(library.AnalysisData{Tags: []string{"long_hair", "1girl"}}); strings.Join(n, "|") != "db:long hair|db:1girl" {
		t.Errorf("tag names %v", n)
	}
}

func TestParseCategory(t *testing.T) {
	cats := ParseCategories("Animals: Dogs, Cats\n# comment\n- People\nTravel / Beaches\nanimals: Birds")
	if len(cats) != 3 || strings.Join(cats[0].Subs, ",") != "Dogs,Cats,Birds" || cats[2].Name != "Travel" {
		t.Fatalf("categories %+v", cats)
	}
	cases := []struct {
		reply     string
		levels    int
		main, sub string
		fail      bool
	}{
		{"animals", 1, "Animals", "", false},
		{"Category: People.", 1, "People", "", false},
		{"The best fit is Travel", 1, "Travel", "", false},
		{"Animals / Dogs", 1, "Animals", "", false},
		{"Animals > dog", 2, "Animals", "Dogs", false},
		{"Main category: Animals\nSubcategory: cats", 2, "Animals", "Cats", false},
		{`{"main": "Travel", "sub": "beaches"}`, 2, "Travel", "Beaches", false},
		{"Birds", 2, "Animals", "Birds", false},
		{"People / Crowds", 2, "People", "", false},
		{"Vehicles", 1, "", "", true},
		{"Animals / Fish", 2, "", "", true},
	}
	for _, c := range cases {
		m, s, err := parseCategory(c.reply, c.levels, cats, nil)
		if (err != nil) != c.fail || m != c.main || s != c.sub {
			t.Errorf("parseCategory(%q, %d) = %q %q %v", c.reply, c.levels, m, s, err)
		}
	}
	// Free-form: tidy, and reuse spellings already in use.
	m, s, err := parseCategory("landscape photography / mountains", 2, nil, []string{"Landscape Photography / Mountain"})
	if err != nil || m != "Landscape Photography" || s != "Mountain" {
		t.Errorf("free-form %q %q %v", m, s, err)
	}
	m, _, _ = parseCategory("street food", 1, nil, nil)
	if m != "Street Food" {
		t.Errorf("title case %q", m)
	}
	if _, _, err := parseCategory("a photo of a very long description that goes on and on", 1, nil, nil); err == nil {
		t.Error("long free-form category should fail")
	}
}

func TestPrompts(t *testing.T) {
	s := Defaults()
	if p := s.Prompt(library.PipelineDanbooru, nil); !strings.Contains(p, "at most 30 tags") {
		t.Errorf("danbooru prompt %q", p)
	}
	if p := s.Prompt(library.PipelineCategory, []string{"Food", "Pets"}); !strings.Contains(p, "already in use when it fits: Food, Pets.") {
		t.Errorf("free category prompt %q", p)
	}
	s.Category.Categories = "Animals: Dogs\nPeople"
	s.Category.Levels = 2
	p := s.Prompt(library.PipelineCategory, []string{"ignored"})
	if !strings.Contains(p, "- Animals: Dogs\n- People") || strings.Contains(p, "ignored") {
		t.Errorf("list prompt %q", p)
	}
	s.Category.Prompt = "Pick one."
	if p := s.Prompt(library.PipelineCategory, nil); !strings.HasPrefix(p, "Pick one.\n\nCategories:\n- Animals") {
		t.Errorf("custom prompt gets the list appended: %q", p)
	}
	a, b := s.ConfigHash(library.PipelineCategory), s.ConfigHash(library.PipelineCaption)
	s.Category.Prompt = "Pick one, please."
	if s.ConfigHash(library.PipelineCategory) == a || s.ConfigHash(library.PipelineCaption) != b {
		t.Error("config hash should follow the pipeline's own prompt only")
	}
}

func setup(t *testing.T) (*bag.Bag, []int64) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "photos")
	if _, err := testimg.WriteTree(filepath.Join(src), 0.1); err != nil {
		t.Fatal(err)
	}
	b, err := bag.Open(filepath.Join(dir, "a.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	if _, err := importer.Run(context.Background(), b, src, importer.Options{Recursive: true}, nil); err != nil {
		t.Fatal(err)
	}
	ids, _ := library.ListIDs(context.Background(), b, query.ImageQuery{}, query.Sort{})
	return b, ids
}

func tagCounts(t *testing.T, b *bag.Bag) map[string]library.Tag {
	tags, err := library.ListTags(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]library.Tag{}
	for _, tg := range tags {
		out[tg.Name] = tg
	}
	return out
}

func imageTags(t *testing.T, b *bag.Bag, id int64) string {
	im, err := library.GetImage(context.Background(), b, id)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(im.Tags, "|")
}

func TestRunStoresResultsAndTags(t *testing.T) {
	b, ids := setup(t)
	ctx := context.Background()
	n := len(ids)
	category := "Animals > dogs"
	fake := llmtest.New(func(r llmtest.Request) (string, int) {
		switch {
		case r.Images != 1:
			return "no image", 400
		case strings.Contains(r.Prompt, "Transcribe"):
			return "NO_TEXT", 0
		case strings.Contains(r.Prompt, "alt text"):
			return "A colourful test scene.", 0
		case strings.Contains(r.Prompt, "Danbooru"):
			return "Tags: 1girl, Long Hair, (smile:1.2), outdoors, 1girl, rating:general", 0
		case strings.Contains(r.Prompt, "not one of the categories"):
			return category, 0 // the hint turn
		case strings.Contains(r.Prompt, "Classify"):
			return "Vehicles", 0
		}
		return "?", 0
	})
	defer fake.Close()

	s := Defaults()
	s.Endpoint, s.Model, s.Concurrency = fake.URL, "fake-vision", 3
	s.Danbooru.AddTags, s.Danbooru.Prefix = true, "db:"
	s.Category.Levels, s.Category.Categories = 2, "Animals: Dogs, Cats\nPeople"
	client, err := s.Client("")
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Pipelines: library.Pipelines}
	plan, err := MakePlan(ctx, b, s, opts)
	if err != nil || plan.Images != n || plan.Requests != 4*n {
		t.Fatalf("plan %+v %v", plan, err)
	}
	var last Progress
	rep, err := Run(ctx, b, s, Clients{Model: client}, plan, opts, func(p Progress) { last = p })
	if err != nil || rep.Stored != 4*n || rep.Failed != 0 || last.Done != 4*n {
		t.Fatalf("run: %v %+v last %+v", err, rep, last)
	}
	if got := len(fake.Requests()); got != 5*n { // each category needed a hint turn
		t.Errorf("%d requests, want %d", got, 5*n)
	}

	tags := tagCounts(t, b)
	for _, name := range []string{"Animals", "Animals / Dogs", "db:1girl", "db:long hair", "db:smile", "db:outdoors"} {
		if tags[name].Count != n || tags[name].Auto != n {
			t.Errorf("tag %q = %+v, want %d auto", name, tags[name], n)
		}
	}
	if _, ok := tags["db:rating:general"]; ok || len(tags) != 6 {
		t.Errorf("tags %v", tags)
	}
	id := ids[0]
	as, _ := library.Analyses(ctx, b, id)
	if len(as) != 4 || as[0].Pipeline != "caption" || as[1].Text != "" || as[3].Main != "Animals" || as[3].Sub != "Dogs" ||
		as[2].Text != "1girl, long_hair, smile, outdoors" || as[0].Model != "fake-vision" {
		t.Errorf("analyses %+v", as)
	}
	im, _ := library.GetImage(ctx, b, id)
	if im.Caption != "A colourful test scene." {
		t.Errorf("caption %q", im.Caption)
	}
	count := func(text string) int {
		c, err := library.Count(ctx, b, query.ImageQuery{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if count("COLOURFUL") != n || count("long hair") != n || count(`"test scene"`) != n || count("scene zebra") != 0 {
		t.Error("text search over analyses")
	}

	// Nothing is missing any more; edited results are kept by "all".
	if p, _ := MakePlan(ctx, b, s, opts); p.Requests != 0 {
		t.Errorf("missing plan %+v", p)
	}
	if err := library.EditAnalysis(ctx, b, id, library.PipelineCaption, "My own words"); err != nil {
		t.Fatal(err)
	}
	if p, _ := MakePlan(ctx, b, s, Options{Pipelines: []string{"caption"}, Mode: ModeAll}); p.Requests != n-1 || p.Edited != 1 {
		t.Errorf("all plan %+v", p)
	}
	s.Danbooru.Prompt = "Tag it."
	if p, _ := MakePlan(ctx, b, s, Options{Pipelines: []string{"caption", "danbooru"}, Mode: ModeChanged}); p.Requests != n || p.ByPipeline["danbooru"] != n {
		t.Errorf("changed plan %+v", p)
	}

	// A job keeps the correction; an explicit re-run of the image replaces it.
	allCaptions := Options{Pipelines: []string{"caption"}, Mode: ModeAll}
	plan, _ = MakePlan(ctx, b, s, allCaptions)
	if rep, err := Run(ctx, b, s, Clients{Model: client}, plan, allCaptions, nil); err != nil || rep.Stored != n-1 {
		t.Fatalf("caption job %v %+v", err, rep)
	}
	if im, _ := library.GetImage(ctx, b, id); im.Caption != "My own words" {
		t.Errorf("a job replaced an edited caption: %q", im.Caption)
	}
	// A new category replaces the old tags; a person's tag survives.
	category = "People"
	out, err := RunOne(ctx, b, s, Clients{Model: client}, id, []string{"category", "caption"}, true)
	if err != nil || len(out) != 2 || !out[0].Stored || !out[1].Stored || out[1].Error != "" {
		t.Fatalf("run one: %v %+v", err, out)
	}
	if got := imageTags(t, b, id); strings.Contains(got, "Animals") || !strings.Contains(got, "People") {
		t.Errorf("tags after new category: %s", got)
	}
	if as, _ := library.Analyses(ctx, b, id); as[0].Text != "A colourful test scene." || as[0].Edited {
		t.Errorf("explicit re-run should replace the edit: %+v", as[0])
	}
	if err := library.AddTags(ctx, b, []int64{id}, []string{"people"}); err != nil {
		t.Fatal(err)
	}
	if err := library.DeleteAnalysis(ctx, b, id, library.PipelineCategory); err != nil {
		t.Fatal(err)
	}
	if got := imageTags(t, b, id); !strings.Contains(got, "People") {
		t.Errorf("a person's tag was removed with the result: %s", got)
	}
	// Removing the Danbooru tags deletes tags left on no image.
	if n2, err := library.RemoveSourceTags(ctx, b, library.PipelineDanbooru); err != nil || n2 != 4*n {
		t.Errorf("remove source tags %d %v", n2, err)
	}
	if tags := tagCounts(t, b); len(tags) != 3 || tags["Animals"].Count != n-1 {
		t.Errorf("tags after removal %v", tags)
	}
	// Purging drops analyses.
	library.Trash(ctx, b, []int64{id})
	library.EmptyTrash(ctx, b, nil)
	if as, _ := library.Analyses(ctx, b, id); len(as) != 0 {
		t.Errorf("purged image keeps analyses %+v", as)
	}
}

func TestRunStopsOnPersistentErrors(t *testing.T) {
	b, ids := setup(t)
	ctx := context.Background()
	for _, tc := range []struct {
		status int
		want   string
	}{
		{401, "Invalid API key"},
		{500, "the first 5 requests failed"},
	} {
		fake := llmtest.New(func(r llmtest.Request) (string, int) { return "Invalid API key", tc.status })
		s := Defaults()
		s.Endpoint, s.Concurrency = fake.URL, 2
		client := llm.New(llm.Config{Endpoint: fake.URL, Retries: 1, RetryDelay: time.Millisecond})
		opts := Options{Pipelines: []string{"caption"}}
		plan, _ := MakePlan(ctx, b, s, opts)
		rep, err := Run(ctx, b, s, Clients{Model: client}, plan, opts, nil)
		fake.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) || rep.Stopped == "" || rep.Stored != 0 {
			t.Errorf("%d: err %v report %+v", tc.status, err, rep)
		}
		if reqs := len(fake.Requests()); reqs >= len(ids) {
			t.Errorf("%d: %d requests, should stop early", tc.status, reqs)
		}
	}
}

func TestCheck(t *testing.T) {
	var code string
	fake := llmtest.New(func(r llmtest.Request) (string, int) { return code, 0 })
	defer fake.Close()
	s := Defaults()
	s.Endpoint = fake.URL
	client, _ := s.Client("")
	res := Check(context.Background(), s, client)
	if res.OK || len(res.Models) != 1 || !strings.Contains(res.Message, "may not accept images") {
		t.Errorf("wrong answer: %+v", res)
	}
	img, err := CheckImage("1234")
	if err != nil || len(img) < 1000 {
		t.Fatalf("check image: %v", err)
	}
}

func TestRetag(t *testing.T) {
	b, ids := setup(t)
	ctx := context.Background()
	fake := llmtest.New(func(r llmtest.Request) (string, int) {
		if strings.Contains(r.Prompt, "Danbooru") {
			return "long_hair, outdoors", 0
		}
		return "Animals / Dogs", 0
	})
	defer fake.Close()
	s := Defaults()
	s.Endpoint = fake.URL
	s.Category.Levels = 2
	client, _ := s.Client("")
	opts := Options{Pipelines: []string{"danbooru", "category"}}
	plan, _ := MakePlan(ctx, b, s, opts)
	if _, err := Run(ctx, b, s, Clients{Model: client}, plan, opts, nil); err != nil {
		t.Fatal(err)
	}
	if tags := tagCounts(t, b); len(tags) != 2 || tags["Animals / Dogs"].Count != len(ids) {
		t.Fatalf("tags after run %v", tags)
	}
	requests := len(fake.Requests())

	s.Danbooru.AddTags, s.Danbooru.Prefix, s.Category.Prefix = true, "db:", "cat:"
	if n, err := Retag(ctx, b, s, "danbooru", nil); err != nil || n != len(ids) {
		t.Fatalf("retag danbooru %d %v", n, err)
	}
	if n, err := Retag(ctx, b, s, "category", nil); err != nil || n != len(ids) {
		t.Fatalf("retag category %d %v", n, err)
	}
	tags := tagCounts(t, b)
	if len(tags) != 4 || tags["db:long hair"].Count != len(ids) || tags["cat:Animals / Dogs"].Count != len(ids) {
		t.Errorf("tags after retag %v", tags)
	}
	s.Danbooru.AddTags = false
	Retag(ctx, b, s, "danbooru", nil)
	if tags := tagCounts(t, b); len(tags) != 2 {
		t.Errorf("tags after turning Danbooru tags off %v", tags)
	}
	if len(fake.Requests()) != requests {
		t.Error("retag must not call the model")
	}
}

func TestTaggerPipeline(t *testing.T) {
	b, ids := setup(t)
	ctx := context.Background()
	fakeTagger := taggertest.New(func(r taggertest.Request) (taggertest.Reply, int) {
		return taggertest.Reply{
			General:    map[string]float64{"long_hair": 0.9, "outdoors": 0.7, "^_^": 0.6, "sky": 0.4, "cloud": 0.2},
			Characters: map[string]float64{"hatsune_miku": 0.95, "kagamine_rin": 0.5},
			Rating:     "sensitive", Score: 0.8,
		}, 0
	})
	defer fakeTagger.Close()
	fakeModel := llmtest.New(func(r llmtest.Request) (string, int) { return "A test image.", 0 })
	defer fakeModel.Close()

	s := Defaults()
	s.Endpoint = fakeModel.URL
	s.Danbooru.Source = DanbooruFromTagger
	s.Danbooru.Tagger.Endpoint = fakeTagger.URL
	s.Danbooru.AddTags, s.Danbooru.Prefix, s.Danbooru.MaxTags = true, "db:", 3
	s.Danbooru.Tagger.CharacterPrefix = "character:"
	s = s.Normalized()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	opts := Options{Pipelines: []string{"caption", "danbooru"}}
	clients, err := s.Clients("", nil, opts.Pipelines)
	if err != nil || clients.Model == nil || clients.Tagger == nil {
		t.Fatalf("clients %+v %v", clients, err)
	}
	plan, _ := MakePlan(ctx, b, s, opts)
	rep, err := Run(ctx, b, s, clients, plan, opts, nil)
	if err != nil || rep.Stored != 2*len(ids) || rep.Model != "default model + fake-tagger" {
		t.Fatalf("report %+v %v", rep, err)
	}
	for _, r := range fakeModel.Requests() {
		if strings.Contains(r.Prompt, "Danbooru") {
			t.Fatal("Danbooru tags were asked of the model")
		}
	}
	if q := fakeTagger.Requests()[0].Query; q.Get("general_threshold") != "0.35" || q.Get("character_threshold") != "0.85" || q.Get("include_characters") != "true" {
		t.Fatalf("tagger query %v", q)
	}
	as, _ := library.Analyses(ctx, b, ids[0])
	var db library.Analysis
	for _, a := range as {
		if a.Pipeline == "danbooru" {
			db = a
		}
	}
	// Character tags first, then at most 3 general tags; the rating apart.
	if strings.Join(db.Tags, " ") != "hatsune_miku long_hair outdoors ^_^" || strings.Join(db.Characters, " ") != "hatsune_miku" ||
		db.Rating != "sensitive" || db.Scores["outdoors"] != 0.7 || db.Model != "fake-tagger" || db.Source != "tagger" {
		t.Fatalf("stored %+v", db)
	}
	if db.Text != "hatsune_miku, long_hair, outdoors, ^_^, rating:sensitive" {
		t.Fatalf("text %q", db.Text)
	}
	if got := imageTags(t, b, ids[0]); got != "character:hatsune miku|db:^_^|db:long hair|db:outdoors|rating:sensitive" {
		t.Fatalf("image tags %q", got)
	}

	// Tightening the filters retags without asking the tagger again.
	sent := len(fakeTagger.Requests())
	s.Danbooru.Tagger.General.Threshold = 0.8
	s.Danbooru.Tagger.Rating.Include = false
	s.Danbooru.Tagger.CharacterPrefix = ""
	if n, err := Retag(ctx, b, s, "danbooru", nil); err != nil || n != len(ids) {
		t.Fatalf("retag %d %v", n, err)
	}
	if got := imageTags(t, b, ids[0]); got != "db:long hair|hatsune miku" {
		t.Fatalf("image tags after retag %q", got)
	}
	if len(fakeTagger.Requests()) != sent {
		t.Fatal("retag called the tagger")
	}
	// The changed filters make the results outdated; the model's are not.
	plan, _ = MakePlan(ctx, b, s, Options{Pipelines: []string{"caption", "danbooru"}, Mode: ModeChanged})
	if plan.ByPipeline["danbooru"] != len(ids) || plan.ByPipeline["caption"] != 0 {
		t.Fatalf("changed plan %+v", plan.ByPipeline)
	}

	// Without a model endpoint, tagger-only jobs still run.
	s.Endpoint = ""
	if _, err := s.Clients("", nil, []string{"danbooru"}); err != nil {
		t.Fatalf("tagger only: %v", err)
	}
	if _, err := s.Clients("", nil, []string{"caption", "danbooru"}); err == nil {
		t.Fatal("caption without an endpoint accepted")
	}
	out, err := RunOne(ctx, b, s, Clients{Tagger: clients.Tagger}, ids[1], []string{"danbooru"}, false)
	if err != nil || len(out) != 1 || out[0].Rating != "" || len(out[0].TagNames) != 2 || out[0].Reply == "" {
		t.Fatalf("try %+v %v", out, err)
	}
}

func TestTaggerUnavailable(t *testing.T) {
	b, ids := setup(t)
	ctx := context.Background()
	fakeTagger := taggertest.New(func(r taggertest.Request) (taggertest.Reply, int) { return taggertest.Reply{}, 400 })
	s := Defaults()
	s.Danbooru.Source = DanbooruFromTagger
	s.Danbooru.Tagger.Endpoint = fakeTagger.URL
	opts := Options{Pipelines: []string{"danbooru"}}
	clients, _ := s.Clients("", nil, opts.Pipelines)
	plan, _ := MakePlan(ctx, b, s, opts)
	// Every request fails: the job stops after the first few.
	rep, err := Run(ctx, b, s, clients, plan, opts, nil)
	if err == nil || !strings.Contains(rep.Stopped, "the first 5 tagger requests failed") || rep.Failed >= len(ids)+taggerWorkers {
		t.Fatalf("report %+v %v", rep, err)
	}
	if check := CheckTagger(ctx, clients.Tagger); check.OK || !strings.Contains(check.Message, "fake failure") || check.TagCount != 10861 {
		t.Fatalf("check %+v", check)
	}
	// An unreachable tagger stops the job before it starts.
	fakeTagger.Close()
	rep, err = Run(ctx, b, s, clients, plan, opts, nil)
	if err == nil || !strings.Contains(rep.Stopped, "the tagger is not available") || rep.Failed != 0 {
		t.Fatalf("report %+v %v", rep, err)
	}
}
