package bag

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestCreateSetsLayoutPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.photobag")
	b, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !b.Created() {
		t.Fatal("expected Created()")
	}
	ctx := context.Background()
	var pageSize, autoVac, appID, version int64
	var journal string
	q := func(sql string, dst any) {
		t.Helper()
		if err := b.R.QueryRowContext(ctx, sql).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	q("PRAGMA page_size", &pageSize)
	q("PRAGMA auto_vacuum", &autoVac)
	q("PRAGMA application_id", &appID)
	q("PRAGMA user_version", &version)
	q("PRAGMA journal_mode", &journal)
	if pageSize != PageSize || autoVac != 2 || appID != ApplicationID || version != int64(LatestVersion()) || journal != "wal" {
		t.Fatalf("got page_size=%d auto_vacuum=%d app_id=%#x version=%d journal=%s", pageSize, autoVac, appID, version, journal)
	}
	var fk int64
	q("PRAGMA foreign_keys", &fk)
	if fk != 1 {
		t.Fatal("foreign_keys not enabled on reader")
	}
	name, _ := b.Meta(ctx, "name")
	if name != "new" {
		t.Fatalf("meta name = %q", name)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err == nil {
			t.Errorf("%s left behind after Close", suffix)
		}
	}
	// Reopen is a no-op open.
	b, err = Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if b.Created() {
		t.Fatal("reopen reported Created")
	}
	b.Close()
}

func TestIdentity(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "id.photobag")
	b, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := b.ID(ctx)
	if again, _ := b.ID(ctx); err != nil || len(id) != 32 || again != id {
		t.Fatalf("id %q %q %v", id, again, err)
	}
	b.Close()
	b, err = Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := b.ID(ctx); again != id {
		t.Errorf("identity changed on reopening: %q", again)
	}
	copyPath := path + ".copy"
	if _, err := b.W.ExecContext(ctx, "VACUUM INTO ?", copyPath); err != nil {
		t.Fatal(err)
	}
	b.Close()
	if err := Reidentify(ctx, copyPath); err != nil {
		t.Fatal(err)
	}
	c, err := Open(copyPath, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if other, _ := c.ID(ctx); other == id || len(other) != 32 {
		t.Errorf("copy identity %q (source %q)", other, id)
	}
}

func TestRefusesForeignDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE x(a)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path, Options{}); !errors.Is(err, ErrNotABag) {
		t.Fatalf("want ErrNotABag, got %v", err)
	}
}

func TestRefusesNonSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk.photobag")
	os.WriteFile(path, []byte("this is definitely not a sqlite database, just some text padding it out"), 0o644)
	if _, err := Open(path, Options{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestMigrationTakesBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.photobag")
	v1, _ := embeddedMigrations.ReadFile("migrations/001_init.sql")
	migrationFS = fstest.MapFS{"migrations/001_init.sql": {Data: v1}}
	t.Cleanup(func() { migrationFS = embeddedMigrations })
	b, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.W.Exec(`INSERT INTO tags(name, key, created_at) VALUES ('x', 'x', 0)`); err != nil {
		t.Fatal(err)
	}
	b.Close()

	// Upgrade a v1 bag with the real migrations.
	migrationFS = embeddedMigrations
	b, err = Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var version int64
	b.R.QueryRow("PRAGMA user_version").Scan(&version)
	latest := LatestVersion()
	if version != int64(latest) || latest < 2 {
		t.Fatalf("user_version = %d, want %d", version, latest)
	}
	if _, err := os.Stat(fmt.Sprintf("%s.pre-v%d.bak", path, latest)); err != nil {
		t.Fatalf("expected pre-migration backup: %v", err)
	}
	var n int
	if err := b.R.QueryRow("SELECT count(*) FROM tags JOIN analyses ON 0").Scan(&n); err != nil {
		t.Fatalf("analyses table after migration: %v", err)
	}
	if err := b.R.QueryRow("SELECT count(source) FROM image_tags").Scan(&n); err != nil {
		t.Fatalf("image_tags.source after migration: %v", err)
	}
}

func TestDeleteJournalMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "del.photobag")
	b, err := Open(path, Options{Journal: JournalDelete})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var mode string
	b.R.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if mode != "delete" {
		t.Fatalf("journal_mode = %s", mode)
	}
	if err := b.SetMeta(context.Background(), "k", "v"); err != nil {
		t.Fatal(err)
	}
}

func TestGlobAndLabels(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"*.JPG", "holiday.jpg", true},
		{"img_00?.jpg", "IMG_001.JPG", true},
		{"[ab]*", "Beach.png", true},
		{"[!ab]*", "Beach.png", false},
		{"ÉTÉ*", "été 2020.jpg", true},
	}
	for _, c := range cases {
		got, err := GlobMatch(c.pat, c.name)
		if err != nil || got != c.want {
			t.Errorf("GlobMatch(%q,%q) = %v,%v", c.pat, c.name, got, err)
		}
	}
	if LabelKey("  Straße   Photos ") != LabelKey("STRASSE photos") {
		t.Error("label keys should fold ß/SS and whitespace")
	}
	if ValidateLabel("   ") == nil {
		t.Error("blank label should be invalid")
	}
	if !Contains("1girl, long_hair, SMILE", "Long Hair") || !Contains("Straße", "STRASSE") || Contains("cat", "dog") {
		t.Error("Contains should fold case and treat _ as a space")
	}
}
