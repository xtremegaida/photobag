package bag

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// migrationFS is the source of migrations; tests may replace it.
var migrationFS fs.FS = embeddedMigrations

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() []migration {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		panic(err)
	}
	var out []migration
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			panic("bad migration name " + e.Name())
		}
		v, err := strconv.Atoi(num)
		if err != nil {
			panic("bad migration name " + e.Name())
		}
		body, err := fs.ReadFile(migrationFS, "migrations/"+e.Name())
		if err != nil {
			panic(err)
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			panic(fmt.Sprintf("migrations must be numbered consecutively from 1; got %s", m.name))
		}
	}
	return out
}

// LatestVersion is the schema version this build writes.
func LatestVersion() int { return len(loadMigrations()) }

func (b *Bag) migrate(ctx context.Context, from int) error {
	for _, m := range loadMigrations() {
		if m.version <= from {
			continue
		}
		tx, err := b.W.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		b.log.Debug("applied migration", "name", m.name)
	}
	return nil
}
