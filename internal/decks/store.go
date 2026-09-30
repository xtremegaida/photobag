// Package decks stores slide decks: library images in a chosen order, with
// the settings of their slideshow.
package decks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"photobag/internal/bag"
	"photobag/internal/library"
)

// ErrNotFound is library.ErrNotFound, so the server maps it to 404.
var ErrNotFound = library.ErrNotFound

// ErrConflict reports a name in use.
var ErrConflict = errors.New("conflict")

// ErrInvalid marks errors in what was asked for (a missing name, a bad
// setting...), as opposed to failures.
var ErrInvalid = errors.New("invalid")

type invalid struct{ error }

func (e invalid) Is(t error) bool { return t == ErrInvalid }
func (e invalid) Unwrap() error   { return e.error }

func invalidf(format string, args ...any) error { return invalid{fmt.Errorf(format, args...)} }

// MaxImages is the most images a deck holds.
const MaxImages = 20000

// active is the condition on images (aliased i) shown in decks.
const active = "i.deleted_at IS NULL AND i.purged_at IS NULL"

// DefaultSettings are the settings of a new deck.
func DefaultSettings() Settings {
	return Settings{Advance: AdvanceTimed, Interval: 5, Crossfade: true, Fade: 1, Fit: FitContain,
		Background: "#000000", Loop: true}
}

// Normalize fills in missing settings and checks the rest.
func (s Settings) Normalize() (Settings, error) {
	d := DefaultSettings()
	switch s.Advance {
	case "":
		s.Advance = d.Advance
	case AdvanceManual, AdvanceTimed:
	default:
		return s, invalidf("unknown way to advance slides %q", s.Advance)
	}
	if s.Interval == 0 {
		s.Interval = d.Interval
	}
	if !(s.Interval >= 1 && s.Interval <= 3600) {
		return s, invalidf("slides show for 1 to 3600 seconds")
	}
	if s.Fade == 0 {
		s.Fade = d.Fade
	}
	if !(s.Fade >= 0.1 && s.Fade <= 10) {
		return s, invalidf("a cross-fade takes 0.1 to 10 seconds")
	}
	s.Interval = math.Round(s.Interval*10) / 10
	s.Fade = math.Round(s.Fade*10) / 10
	switch s.Fit {
	case "":
		s.Fit = d.Fit
	case FitContain, FitCover, FitStretch, FitCenter:
	default:
		return s, invalidf("unknown way to fit images %q", s.Fit)
	}
	if s.Background == "" {
		s.Background = d.Background
	}
	c, ok := hexColour(s.Background)
	if !ok {
		return s, invalidf("the background colour %q is not a #rrggbb colour", s.Background)
	}
	s.Background = c
	return s, nil
}

// hexColour normalises #rgb and #rrggbb to lower-case #rrggbb.
func hexColour(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !strings.HasPrefix(s, "#") {
		return "", false
	}
	h := s[1:]
	for _, r := range h {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return "", false
		}
	}
	switch len(h) {
	case 3:
		return "#" + string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]}), true
	case 6:
		return s, true
	}
	return "", false
}

// parseSettings reads stored settings, falling back to the defaults.
func parseSettings(raw string) Settings {
	s := DefaultSettings()
	if json.Unmarshal([]byte(raw), &s) != nil {
		return DefaultSettings()
	}
	if n, err := s.Normalize(); err == nil {
		return n
	}
	return DefaultSettings()
}

func cleanName(s string) (string, error) {
	s = strings.Join(strings.Fields(s), " ")
	switch {
	case s == "":
		return "", invalidf("the deck needs a name")
	case utf8.RuneCountInString(s) > 200:
		return "", invalidf("the deck name is too long")
	}
	return s, nil
}

// checkName fails if another deck has the name.
func checkName(ctx context.Context, tx *sql.Tx, id int64, name string) error {
	var other int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM decks WHERE key = ? AND id != ?", bag.LabelKey(name), id).Scan(&other)
	switch {
	case err == nil:
		return fmt.Errorf("a deck named %q already exists: %w", name, ErrConflict)
	case errors.Is(err, sql.ErrNoRows):
		return nil
	}
	return err
}

// touch marks a deck changed, failing if there is no such deck.
func touch(ctx context.Context, tx *sql.Tx, id int64) error {
	res, err := tx.ExecContext(ctx, "UPDATE decks SET updated_at = ? WHERE id = ?", bag.NowMillis(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("deck %d: %w", id, ErrNotFound)
	}
	return nil
}

const deckCols = `d.id, d.name, d.notes, d.settings, d.created_at, d.updated_at,
	COALESCE(sum(i.deleted_at IS NULL AND i.purged_at IS NULL), 0),
	COALESCE(sum(i.deleted_at IS NOT NULL AND i.purged_at IS NULL), 0)
	FROM decks d LEFT JOIN deck_images di ON di.deck_id = d.id LEFT JOIN images i ON i.id = di.image_id`

func scanDeck(sc interface{ Scan(...any) error }) (Deck, error) {
	var d Deck
	var settings string
	err := sc.Scan(&d.ID, &d.Name, &d.Notes, &settings, &d.CreatedAt, &d.UpdatedAt, &d.Count, &d.Hidden)
	d.Settings = parseSettings(settings)
	d.Covers = []int64{}
	return d, err
}

// covers loads a deck's first images.
func covers(ctx context.Context, b *bag.Bag, d *Deck) error {
	rows, err := b.R.QueryContext(ctx, `SELECT di.image_id FROM deck_images di JOIN images i ON i.id = di.image_id
		WHERE di.deck_id = ? AND `+active+` ORDER BY di.position LIMIT 4`, d.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		d.Covers = append(d.Covers, id)
	}
	return rows.Err()
}

// List returns the decks, most recently changed first.
func List(ctx context.Context, b *bag.Bag) ([]Deck, error) {
	rows, err := b.R.QueryContext(ctx, "SELECT "+deckCols+" GROUP BY d.id ORDER BY d.updated_at DESC, d.id DESC")
	if err != nil {
		return nil, err
	}
	out := []Deck{}
	for rows.Next() {
		d, err := scanDeck(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := covers(ctx, b, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Get returns a deck and its images in slide order.
func Get(ctx context.Context, b *bag.Bag, id int64) (*Detail, error) {
	d, err := scanDeck(b.R.QueryRowContext(ctx, "SELECT "+deckCols+" WHERE d.id = ? GROUP BY d.id", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("deck %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	out := &Detail{Deck: d}
	if out.IDs, err = IDs(ctx, b, id); err != nil {
		return nil, err
	}
	out.Covers = out.IDs[:min(4, len(out.IDs))]
	return out, nil
}

// IDs returns the images of a deck in slide order (without those in the
// trash).
func IDs(ctx context.Context, b *bag.Bag, id int64) ([]int64, error) {
	rows, err := b.R.QueryContext(ctx, `SELECT di.image_id FROM deck_images di JOIN images i ON i.id = di.image_id
		WHERE di.deck_id = ? AND `+active+` ORDER BY di.position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var x int64
		if err := rows.Scan(&x); err != nil {
			return nil, err
		}
		ids = append(ids, x)
	}
	return ids, rows.Err()
}

// Create makes a deck. Nil settings mean the defaults.
func Create(ctx context.Context, b *bag.Bag, name, notes string, settings *Settings) (*Detail, error) {
	name, err := cleanName(name)
	if err != nil {
		return nil, err
	}
	s := DefaultSettings()
	if settings != nil {
		if s, err = settings.Normalize(); err != nil {
			return nil, err
		}
	}
	js, _ := json.Marshal(s)
	var id int64
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		if err := checkName(ctx, tx, 0, name); err != nil {
			return err
		}
		now := bag.NowMillis()
		res, err := tx.ExecContext(ctx, `INSERT INTO decks(name, key, notes, settings, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`, name, bag.LabelKey(name), notes, string(js), now, now)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return nil, err
	}
	return Get(ctx, b, id)
}

// Change is an edit of a deck; nil fields stay as they are.
type Change struct {
	Name     *string   `json:"name"`
	Notes    *string   `json:"notes"`
	Settings *Settings `json:"settings"`
}

// Update edits a deck.
func Update(ctx context.Context, b *bag.Bag, id int64, c Change) (*Detail, error) {
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		if err := touch(ctx, tx, id); err != nil {
			return err
		}
		if c.Name != nil {
			name, err := cleanName(*c.Name)
			if err != nil {
				return err
			}
			if err := checkName(ctx, tx, id, name); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE decks SET name = ?, key = ? WHERE id = ?", name, bag.LabelKey(name), id); err != nil {
				return err
			}
		}
		if c.Notes != nil {
			if _, err := tx.ExecContext(ctx, "UPDATE decks SET notes = ? WHERE id = ?", *c.Notes, id); err != nil {
				return err
			}
		}
		if c.Settings != nil {
			s, err := c.Settings.Normalize()
			if err != nil {
				return err
			}
			js, _ := json.Marshal(s)
			if _, err := tx.ExecContext(ctx, "UPDATE decks SET settings = ? WHERE id = ?", string(js), id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return Get(ctx, b, id)
}

// Delete removes a deck (not its images).
func Delete(ctx context.Context, b *bag.Bag, id int64) error {
	return b.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM decks WHERE id = ?", id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("deck %d: %w", id, ErrNotFound)
		}
		return nil
	})
}

// Add appends images to the end of a deck in the order given, skipping
// those already in it and those not in the library.
func Add(ctx context.Context, b *bag.Bag, id int64, ids []int64) (Added, error) {
	var r Added
	js, _ := json.Marshal(ids)
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		if err := touch(ctx, tx, id); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM deck_images WHERE deck_id = ?
			AND image_id IN (SELECT value FROM json_each(?))`, id, string(js)).Scan(&r.Present); err != nil {
			return err
		}
		var next int64
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(max(position) + 1, 0) FROM deck_images WHERE deck_id = ?",
			id).Scan(&next); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO deck_images(deck_id, image_id, position, added_at)
			SELECT ?, i.id, ? + j.key, ? FROM json_each(?) j JOIN images i ON i.id = j.value WHERE `+active,
			id, next, bag.NowMillis(), string(js))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		r.Added = int(n)
		var total int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM deck_images WHERE deck_id = ?", id).Scan(&total); err != nil {
			return err
		}
		if total > MaxImages {
			return invalidf("a deck holds at most %d images (this would make %d)", MaxImages, total)
		}
		return nil
	})
	return r, err
}

// Remove takes images out of a deck and returns how many it held.
func Remove(ctx context.Context, b *bag.Bag, id int64, ids []int64) (int, error) {
	js, _ := json.Marshal(ids)
	var n int64
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		if err := touch(ctx, tx, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM deck_images WHERE deck_id = ? AND image_id IN (SELECT value FROM json_each(?))",
			id, string(js))
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}

// Move moves images of a deck, keeping their order, to just before the
// image before (0: to the end).
func Move(ctx context.Context, b *bag.Bag, id int64, ids []int64, before int64) error {
	return reorder(ctx, b, id, func(order []int64) []int64 { return MoveIDs(order, ids, before) })
}

// SetOrder puts the images ids first, in that order, followed by the rest
// of the deck in its current order.
func SetOrder(ctx context.Context, b *bag.Bag, id int64, ids []int64) error {
	return reorder(ctx, b, id, func(order []int64) []int64 {
		in := make(map[int64]bool, len(order))
		for _, x := range order {
			in[x] = true
		}
		out := make([]int64, 0, len(order))
		for _, x := range ids {
			if in[x] {
				out = append(out, x)
				delete(in, x)
			}
		}
		for _, x := range order {
			if in[x] {
				out = append(out, x)
			}
		}
		return out
	})
}

// reorder renumbers a deck (including its hidden images) in the order f
// makes of the current one.
func reorder(ctx context.Context, b *bag.Bag, id int64, f func([]int64) []int64) error {
	return b.Tx(ctx, func(tx *sql.Tx) error {
		if err := touch(ctx, tx, id); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT image_id, position FROM deck_images WHERE deck_id = ? ORDER BY position, added_at", id)
		if err != nil {
			return err
		}
		var order []int64
		pos := map[int64]int64{}
		for rows.Next() {
			var x, p int64
			if err := rows.Scan(&x, &p); err != nil {
				rows.Close()
				return err
			}
			order = append(order, x)
			pos[x] = p
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, "UPDATE deck_images SET position = ? WHERE deck_id = ? AND image_id = ?")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for i, x := range f(order) {
			if pos[x] == int64(i) {
				continue
			}
			if _, err := stmt.ExecContext(ctx, i, id, x); err != nil {
				return err
			}
		}
		return nil
	})
}

// MoveIDs moves the members of ids found in order, keeping their order, to
// just before before (or, when before is 0, not in order, or itself moved
// with nothing unmoved after it, to the end).
func MoveIDs(order, ids []int64, before int64) []int64 {
	moving := make(map[int64]bool, len(ids))
	for _, x := range ids {
		moving[x] = true
	}
	var block []int64
	anchor, seen := int64(0), false
	for _, x := range order {
		if moving[x] {
			block = append(block, x)
		}
		if x == before {
			seen = true
		}
		if seen && anchor == 0 && !moving[x] {
			anchor = x
		}
	}
	out := make([]int64, 0, len(order))
	for _, x := range order {
		if moving[x] {
			continue
		}
		if x == anchor {
			out = append(out, block...)
		}
		out = append(out, x)
	}
	if anchor == 0 {
		out = append(out, block...)
	}
	return out
}

// ForImage lists the decks an image is in.
func ForImage(ctx context.Context, b *bag.Bag, imageID int64) ([]Ref, error) {
	rows, err := b.R.QueryContext(ctx, `SELECT d.id, d.name FROM deck_images di JOIN decks d ON d.id = di.deck_id
		WHERE di.image_id = ? ORDER BY d.name COLLATE NOCASE`, imageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Ref{}
	for rows.Next() {
		var r Ref
		if err := rows.Scan(&r.ID, &r.Name); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Merge puts keeper in the place of a duplicate merged into it, in every
// deck of the duplicate's that keeper is not in already.
func Merge(ctx context.Context, tx *sql.Tx, duplicate, keeper int64) error {
	if _, err := tx.ExecContext(ctx, "UPDATE OR IGNORE deck_images SET image_id = ? WHERE image_id = ?", keeper, duplicate); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM deck_images WHERE image_id = ?", duplicate)
	return err
}
