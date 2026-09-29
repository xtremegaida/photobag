package experiments

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"photobag/internal/bag"
	"photobag/internal/comfy"
	"photobag/internal/library"
)

// ErrNotFound is library.ErrNotFound, so the server maps it to 404.
var ErrNotFound = library.ErrNotFound

// ErrConflict reports a name in use.
var ErrConflict = errors.New("conflict")

// ErrInvalid marks errors in what was asked for (bad JSON, a missing
// name, an unknown node...), as opposed to failures.
var ErrInvalid = errors.New("invalid")

type invalid struct{ error }

func (e invalid) Is(t error) bool { return t == ErrInvalid }
func (e invalid) Unwrap() error   { return e.error }

// invalidf returns an ErrInvalid error.
func invalidf(format string, args ...any) error { return invalid{fmt.Errorf(format, args...)} }

const settingsKey = "comfy.settings"

// LoadSettings reads the ComfyUI connection.
func LoadSettings(ctx context.Context, b *bag.Bag) (Settings, error) {
	var s Settings
	raw, err := b.Meta(ctx, settingsKey)
	if err != nil || raw == "" {
		return s, err
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Settings{}, nil
	}
	return s, nil
}

// SaveSettings stores the ComfyUI connection.
func SaveSettings(ctx context.Context, b *bag.Bag, s Settings) (Settings, error) {
	s.Endpoint = comfy.NormalizeEndpoint(s.Endpoint)
	if err := comfy.ValidateEndpoint(s.Endpoint); err != nil {
		return s, err
	}
	js, _ := json.Marshal(s)
	return s, b.SetMeta(ctx, settingsKey, string(js))
}

func cleanName(s, what string) (string, error) {
	s = strings.Join(strings.Fields(s), " ")
	switch {
	case s == "":
		return "", invalidf("the %s needs a name", what)
	case utf8.RuneCountInString(s) > 200:
		return "", invalidf("the %s name is too long", what)
	}
	return s, nil
}

// versionFrom parses workflow JSON into a Version (not yet stored).
func versionFrom(id int64, js string, created int64) (Version, *comfy.Workflow, error) {
	w, err := comfy.Parse([]byte(js))
	if err != nil {
		return Version{}, nil, invalid{err}
	}
	kind, _ := w.Outputs()
	return Version{ID: id, JSON: string(w.JSON()), Nodes: w.Nodes(), Problems: nonNil(w.Problems()), Classes: w.Classes(),
		Output: kind, CreatedAt: created}, w, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ParseWorkflow checks workflow JSON and describes it, without storing it.
func ParseWorkflow(js string) (Version, error) {
	v, _, err := versionFrom(0, js, 0)
	return v, err
}

// ensureVersion stores a workflow's content, returning the existing
// version when it is already stored.
func ensureVersion(ctx context.Context, tx *sql.Tx, w *comfy.Workflow) (int64, error) {
	js := w.JSON()
	sum := sha256.Sum256(js)
	if _, err := tx.ExecContext(ctx, `INSERT INTO workflow_versions(sha256, json, created_at) VALUES (?, ?, ?)
		ON CONFLICT(sha256) DO NOTHING`, sum[:], string(js), bag.NowMillis()); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM workflow_versions WHERE sha256 = ?", sum[:]).Scan(&id)
	return id, err
}

// dropVersion deletes a version nothing refers to any more.
func dropVersion(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM workflow_versions WHERE id = ?
		AND NOT EXISTS (SELECT 1 FROM workflows WHERE version_id = ?)
		AND NOT EXISTS (SELECT 1 FROM generation_runs WHERE version_id = ?)
		AND NOT EXISTS (SELECT 1 FROM generations WHERE version_id = ?)`, id, id, id, id)
	return err
}

// GetVersion returns a workflow version.
func GetVersion(ctx context.Context, b *bag.Bag, id int64) (*Version, error) {
	var js string
	var created int64
	err := b.R.QueryRowContext(ctx, "SELECT json, created_at FROM workflow_versions WHERE id = ?", id).Scan(&js, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("workflow version %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	v, _, err := versionFrom(id, js, created)
	return &v, err
}

// loadVersion returns a version's parsed workflow.
func loadVersion(ctx context.Context, b *bag.Bag, id int64) (*comfy.Workflow, error) {
	var js string
	err := b.R.QueryRowContext(ctx, "SELECT json FROM workflow_versions WHERE id = ?", id).Scan(&js)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("workflow version %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return comfy.Parse([]byte(js))
}

const workflowCols = `w.id, w.name, w.notes, w.version_id, w.created_at, w.updated_at, v.json`

func scanWorkflow(sc interface{ Scan(...any) error }) (Workflow, string, error) {
	var w Workflow
	var js string
	if err := sc.Scan(&w.ID, &w.Name, &w.Notes, &w.VersionID, &w.CreatedAt, &w.UpdatedAt, &js); err != nil {
		return w, "", err
	}
	if cw, err := comfy.Parse([]byte(js)); err == nil {
		w.NodeCount = len(cw.Nodes())
		w.Output, _ = cw.Outputs()
	}
	return w, js, nil
}

// ListWorkflows returns the templates by name.
func ListWorkflows(ctx context.Context, b *bag.Bag) ([]Workflow, error) {
	rows, err := b.R.QueryContext(ctx, `SELECT `+workflowCols+` FROM workflows w
		JOIN workflow_versions v ON v.id = w.version_id ORDER BY w.name COLLATE NOCASE, w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Workflow{}
	for rows.Next() {
		w, _, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetWorkflow returns a template with its content.
func GetWorkflow(ctx context.Context, b *bag.Bag, id int64) (*WorkflowDetail, error) {
	w, js, err := scanWorkflow(b.R.QueryRowContext(ctx, `SELECT `+workflowCols+` FROM workflows w
		JOIN workflow_versions v ON v.id = w.version_id WHERE w.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("workflow %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var created int64
	if err := b.R.QueryRowContext(ctx, "SELECT created_at FROM workflow_versions WHERE id = ?", w.VersionID).Scan(&created); err != nil {
		return nil, err
	}
	v, _, err := versionFrom(w.VersionID, js, created)
	if err != nil {
		return nil, err
	}
	return &WorkflowDetail{Workflow: w, Version: v}, nil
}

// WorkflowChange edits a template; nil fields stay.
type WorkflowChange struct {
	Name  *string `json:"name"`
	Notes *string `json:"notes"`
	JSON  *string `json:"json"`
}

// SaveWorkflow creates a template (id 0) or changes one. New content
// becomes a new version; images made with the old one keep it.
func SaveWorkflow(ctx context.Context, b *bag.Bag, id int64, c WorkflowChange) (*WorkflowDetail, error) {
	var name string
	var err error
	if c.Name != nil {
		if name, err = cleanName(*c.Name, "workflow"); err != nil {
			return nil, err
		}
	}
	var parsed *comfy.Workflow
	if c.JSON != nil {
		if parsed, err = comfy.Parse([]byte(*c.JSON)); err != nil {
			return nil, invalid{err}
		}
	}
	if id == 0 && (c.Name == nil || parsed == nil) {
		return nil, invalidf("a new workflow needs a name and its JSON")
	}
	now := bag.NowMillis()
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		if c.Name != nil {
			var other int64
			err := tx.QueryRowContext(ctx, "SELECT id FROM workflows WHERE key = ? AND id != ?", bag.LabelKey(name), id).Scan(&other)
			if err == nil {
				return fmt.Errorf("%w: there is already a workflow called “%s”", ErrConflict, name)
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		if id == 0 {
			vid, err := ensureVersion(ctx, tx, parsed)
			if err != nil {
				return err
			}
			notes := ""
			if c.Notes != nil {
				notes = strings.TrimSpace(*c.Notes)
			}
			res, err := tx.ExecContext(ctx, `INSERT INTO workflows(name, key, notes, version_id, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?)`, name, bag.LabelKey(name), notes, vid, now, now)
			if err != nil {
				return err
			}
			id, _ = res.LastInsertId()
			return nil
		}
		var old int64
		if err := tx.QueryRowContext(ctx, "SELECT version_id FROM workflows WHERE id = ?", id).Scan(&old); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("workflow %d: %w", id, ErrNotFound)
		} else if err != nil {
			return err
		}
		if c.Name != nil {
			if _, err := tx.ExecContext(ctx, "UPDATE workflows SET name = ?, key = ? WHERE id = ?", name, bag.LabelKey(name), id); err != nil {
				return err
			}
		}
		if c.Notes != nil {
			if _, err := tx.ExecContext(ctx, "UPDATE workflows SET notes = ? WHERE id = ?", strings.TrimSpace(*c.Notes), id); err != nil {
				return err
			}
		}
		if parsed != nil {
			vid, err := ensureVersion(ctx, tx, parsed)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE workflows SET version_id = ? WHERE id = ?", vid, id); err != nil {
				return err
			}
			if vid != old {
				if err := dropVersion(ctx, tx, old); err != nil {
					return err
				}
			}
		}
		_, err := tx.ExecContext(ctx, "UPDATE workflows SET updated_at = ? WHERE id = ?", now, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return GetWorkflow(ctx, b, id)
}

// DeleteWorkflow removes a template. Images made with it keep their
// workflow version.
func DeleteWorkflow(ctx context.Context, b *bag.Bag, id int64) error {
	return b.Tx(ctx, func(tx *sql.Tx) error {
		var vid int64
		if err := tx.QueryRowContext(ctx, "SELECT version_id FROM workflows WHERE id = ?", id).Scan(&vid); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("workflow %d: %w", id, ErrNotFound)
		} else if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM workflows WHERE id = ?", id); err != nil {
			return err
		}
		return dropVersion(ctx, tx, vid)
	})
}

func scanExperiment(sc interface{ Scan(...any) error }) (Experiment, error) {
	var e Experiment
	var req string
	err := sc.Scan(&e.ID, &e.Name, &e.Notes, &req, &e.CreatedAt, &e.UpdatedAt, &e.Held, &e.Moved, &e.Runs)
	if err != nil {
		return e, err
	}
	_ = json.Unmarshal([]byte(req), &e.Last)
	if e.Last.Overrides == nil {
		e.Last.Overrides = []comfy.Override{}
	}
	e.Covers = []string{}
	return e, nil
}

const experimentCols = `e.id, e.name, e.notes, e.request, e.created_at, e.updated_at,
	(SELECT count(*) FROM generations g WHERE g.experiment_id = e.id AND g.image_id IS NULL),
	(SELECT count(*) FROM generations g WHERE g.experiment_id = e.id AND g.image_id IS NOT NULL),
	(SELECT count(*) FROM generation_runs r WHERE r.experiment_id = e.id)`

func (e *Experiment) loadCovers(ctx context.Context, b *bag.Bag) error {
	rows, err := b.R.QueryContext(ctx, `SELECT lower(hex(sha256)) FROM generations
		WHERE experiment_id = ? AND image_id IS NULL ORDER BY id DESC LIMIT 4`, e.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return err
		}
		e.Covers = append(e.Covers, sha)
	}
	return rows.Err()
}

// ListExperiments returns experiments, most recently used first.
func ListExperiments(ctx context.Context, b *bag.Bag) ([]Experiment, error) {
	rows, err := b.R.QueryContext(ctx, `SELECT `+experimentCols+` FROM experiments e ORDER BY e.updated_at DESC, e.id DESC`)
	if err != nil {
		return nil, err
	}
	out := []Experiment{}
	for rows.Next() {
		e, err := scanExperiment(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := out[i].loadCovers(ctx, b); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GetExperiment returns an experiment.
func GetExperiment(ctx context.Context, b *bag.Bag, id int64) (*Experiment, error) {
	e, err := scanExperiment(b.R.QueryRowContext(ctx, `SELECT `+experimentCols+` FROM experiments e WHERE e.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("experiment %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return &e, e.loadCovers(ctx, b)
}

// ExperimentChange edits an experiment; nil fields stay.
type ExperimentChange struct {
	Name    *string  `json:"name"`
	Notes   *string  `json:"notes"`
	Request *Request `json:"request"`
}

// SaveExperiment creates an experiment (id 0) or changes one.
func SaveExperiment(ctx context.Context, b *bag.Bag, id int64, c ExperimentChange) (*Experiment, error) {
	var name string
	var err error
	if c.Name != nil {
		if name, err = cleanName(*c.Name, "experiment"); err != nil {
			return nil, err
		}
	} else if id == 0 {
		return nil, invalidf("a new experiment needs a name")
	}
	now := bag.NowMillis()
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		if id == 0 {
			res, err := tx.ExecContext(ctx, "INSERT INTO experiments(name, created_at, updated_at) VALUES (?, ?, ?)", name, now, now)
			if err != nil {
				return err
			}
			id, _ = res.LastInsertId()
		} else {
			res, err := tx.ExecContext(ctx, "UPDATE experiments SET updated_at = ? WHERE id = ?", now, id)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("experiment %d: %w", id, ErrNotFound)
			}
			if c.Name != nil {
				if _, err := tx.ExecContext(ctx, "UPDATE experiments SET name = ? WHERE id = ?", name, id); err != nil {
					return err
				}
			}
		}
		if c.Notes != nil {
			if _, err := tx.ExecContext(ctx, "UPDATE experiments SET notes = ? WHERE id = ?", strings.TrimSpace(*c.Notes), id); err != nil {
				return err
			}
		}
		if c.Request != nil {
			return saveRequest(ctx, tx, id, *c.Request)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return GetExperiment(ctx, b, id)
}

func saveRequest(ctx context.Context, tx *sql.Tx, id int64, r Request) error {
	if r.Overrides == nil {
		r.Overrides = []comfy.Override{}
	}
	js, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE experiments SET request = ?, updated_at = ? WHERE id = ?", string(js), bag.NowMillis(), id)
	return err
}

// DeleteExperiment deletes an experiment and the images it holds; images
// moved to the library stay there.
func DeleteExperiment(ctx context.Context, b *bag.Bag, id int64) error {
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := discardTx(ctx, tx, "experiment_id = ? AND image_id IS NULL", id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM experiments WHERE id = ?", id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("experiment %d: %w", id, ErrNotFound)
		}
		return nil
	})
	if err != nil {
		return err
	}
	_, err = b.W.ExecContext(ctx, "PRAGMA incremental_vacuum")
	return err
}
