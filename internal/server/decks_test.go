package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"photobag/internal/bag"
	"photobag/internal/decks"
	"photobag/internal/importer"
	"photobag/internal/library"
	"photobag/internal/query"
	"photobag/internal/testimg"
)

func TestDecksAPI(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photos")
	if _, err := testimg.WriteTree(src, 0.1); err != nil {
		t.Fatal(err)
	}
	b, err := bag.Open(filepath.Join(dir, "d.photobag"), bag.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := importer.Run(context.Background(), b, src, importer.Options{Recursive: true}, nil); err != nil {
		t.Fatal(err)
	}
	byName, _ := library.ListIDs(context.Background(), b, query.ImageQuery{}, query.Sort{Field: query.SortName})

	srv := New(b, Config{Addr: "127.0.0.1:0", NoToken: true, KeyStore: filepath.Join(dir, "credentials.json"), TaggerDir: filepath.Join(dir, "tagger")})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx) }()
	defer func() {
		cancel()
		<-served
	}()
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: strings.TrimSuffix(srv.URL(), "/"), http: &http.Client{Jar: jar}}

	// A deck made from a query holds its images in the sort order given.
	var d decks.Detail
	if st := c.do("POST", "/api/decks", map[string]any{"name": "All", "query": map[string]any{}, "sort": map[string]any{"field": "name"}}, &d); st != 200 {
		t.Fatalf("create: %d", st)
	}
	if !slices.Equal(d.IDs, byName) || d.Count != len(byName) || d.Settings != decks.DefaultSettings() {
		t.Fatalf("created %+v, want ids %v", d, byName)
	}
	if st := c.do("POST", "/api/decks", map[string]any{"name": "all"}, nil); st != http.StatusConflict {
		t.Errorf("duplicate name: %d", st)
	}
	path := "/api/decks/" + strconv.FormatInt(d.ID, 10)

	c.do("POST", path+"/sort", map[string]any{"sort": map[string]any{"field": "name", "desc": true}}, &d)
	want := slices.Clone(byName)
	slices.Reverse(want)
	if !slices.Equal(d.IDs, want) {
		t.Fatalf("sorted %v, want %v", d.IDs, want)
	}
	c.do("POST", path+"/sort", map[string]any{"ids": byName[:2]}, &d)
	if !slices.Equal(d.IDs[:2], byName[:2]) || len(d.IDs) != len(byName) {
		t.Fatalf("explicit order %v", d.IDs)
	}
	first := d.IDs[0]
	c.do("POST", path+"/move", map[string]any{"ids": []int64{first}, "before": 0}, &d)
	if d.IDs[len(d.IDs)-1] != first {
		t.Fatalf("moved to the end: %v", d.IDs)
	}
	var removed struct{ Removed int }
	c.do("POST", path+"/remove", map[string]any{"ids": []int64{first}}, &removed)
	var added decks.Added
	c.do("POST", path+"/add", map[string]any{"ids": []int64{first, d.IDs[0]}}, &added)
	if removed.Removed != 1 || added.Added != 1 || added.Present != 1 {
		t.Errorf("removed %+v, added %+v", removed, added)
	}

	var detail struct {
		Decks []decks.Ref `json:"decks"`
	}
	c.do("GET", "/api/images/"+strconv.FormatInt(first, 10), nil, &detail)
	if len(detail.Decks) != 1 || detail.Decks[0].Name != "All" {
		t.Errorf("image decks %+v", detail.Decks)
	}

	if st := c.do("PATCH", path, map[string]any{"settings": map[string]any{"interval": 0.2}}, nil); st != http.StatusBadRequest {
		t.Errorf("bad interval: %d", st)
	}
	c.do("PATCH", path, map[string]any{"name": "Everything", "settings": map[string]any{"advance": "manual", "fit": "center", "background": "#123"}}, &d)
	if d.Name != "Everything" || d.Settings.Advance != decks.AdvanceManual || d.Settings.Background != "#112233" {
		t.Errorf("patched %+v", d)
	}
	var list []decks.Deck
	c.do("GET", "/api/decks", nil, &list)
	if len(list) != 1 || list[0].Count != len(byName) || len(list[0].Covers) != 4 {
		t.Errorf("list %+v", list)
	}
	if st := c.do("DELETE", path, nil, nil); st != 200 {
		t.Errorf("delete: %d", st)
	}
	if st := c.do("GET", path, nil, nil); st != http.StatusNotFound {
		t.Errorf("deleted deck: %d", st)
	}
}
