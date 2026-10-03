package files

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"photobag/internal/bag"
)

// querier is what both *sql.DB and *sql.Tx offer.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

const nodeCols = `f.id, COALESCE(f.parent_id, 0), f.name, f.is_dir, f.size, f.modified_at, f.created_at,
	COALESCE(c.sha256, x''), COALESCE(c.sniffed, ''),
	CASE WHEN f.is_dir THEN (SELECT count(*) FROM files ch WHERE ch.parent_id = f.id) ELSE 0 END`

const nodeFrom = ` FROM files f LEFT JOIN file_contents c ON c.id = f.content_id`

func scanNode(sc interface{ Scan(...any) error }) (Node, error) {
	var n Node
	var sha []byte
	var sniffed string
	err := sc.Scan(&n.ID, &n.ParentID, &n.Name, &n.Dir, &n.Size, &n.ModifiedAt, &n.CreatedAt, &sha, &sniffed, &n.Items)
	if n.Dir {
		n.Kind = KindFolder
	} else {
		n.Type, n.Kind = TypeOf(n.Name, sniffed)
		n.SHA256 = hex.EncodeToString(sha)
	}
	return n, err
}

func scanNodes(rows *sql.Rows, err error) ([]Node, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// withSizes fills in the total size of the folders among nodes.
func withSizes(ctx context.Context, q querier, nodes []Node) error {
	var dirs []int64
	for _, n := range nodes {
		if n.Dir {
			dirs = append(dirs, n.ID)
		}
	}
	if len(dirs) == 0 {
		return nil
	}
	js, _ := json.Marshal(dirs)
	rows, err := q.QueryContext(ctx, `WITH RECURSIVE sub(root, id) AS (
			SELECT value, value FROM json_each(?)
			UNION ALL
			SELECT sub.root, f.id FROM files f JOIN sub ON f.parent_id = sub.id
		)
		SELECT sub.root, COALESCE(sum(f.size), 0) FROM sub JOIN files f ON f.id = sub.id AND NOT f.is_dir
		GROUP BY sub.root`, string(js))
	if err != nil {
		return err
	}
	defer rows.Close()
	sizes := map[int64]int64{}
	for rows.Next() {
		var id, size int64
		if err := rows.Scan(&id, &size); err != nil {
			return err
		}
		sizes[id] = size
	}
	for i := range nodes {
		if nodes[i].Dir {
			nodes[i].Size = sizes[nodes[i].ID]
		}
	}
	return rows.Err()
}

func get(ctx context.Context, q querier, id int64) (*Node, error) {
	n, err := scanNode(q.QueryRowContext(ctx, "SELECT "+nodeCols+nodeFrom+" WHERE f.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// Get returns one file or folder.
func Get(ctx context.Context, b *bag.Bag, id int64) (*Node, error) {
	n, err := get(ctx, b.R, id)
	if err != nil {
		return nil, err
	}
	list := []Node{*n}
	if err := withSizes(ctx, b.R, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// folder checks that id is a folder (0, the top level, always is).
func folder(ctx context.Context, q querier, id int64) error {
	if id == 0 {
		return nil
	}
	var dir bool
	err := q.QueryRowContext(ctx, "SELECT is_dir FROM files WHERE id = ?", id).Scan(&dir)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !dir {
		return invalidf("not a folder")
	}
	return nil
}

func children(ctx context.Context, q querier, parent int64) ([]Node, error) {
	return scanNodes(q.QueryContext(ctx, "SELECT "+nodeCols+nodeFrom+
		" WHERE COALESCE(f.parent_id, 0) = ? ORDER BY f.is_dir DESC, f.name COLLATE NOCASE, f.id", parent))
}

// List returns a folder's children (0: the top level), folders first.
func List(ctx context.Context, b *bag.Bag, parent int64) ([]Node, error) {
	if err := folder(ctx, b.R, parent); err != nil {
		return nil, err
	}
	out, err := children(ctx, b.R, parent)
	if err != nil {
		return nil, err
	}
	return out, withSizes(ctx, b.R, out)
}

// Ancestors returns the folders from the top level down to id, including id.
func Ancestors(ctx context.Context, b *bag.Bag, id int64) ([]Node, error) {
	return ancestors(ctx, b.R, id)
}

func ancestors(ctx context.Context, q querier, id int64) ([]Node, error) {
	if id == 0 {
		return []Node{}, nil
	}
	return scanNodes(q.QueryContext(ctx, `WITH RECURSIVE up(id, depth) AS (
			SELECT ?, 0
			UNION ALL
			SELECT f.parent_id, up.depth + 1 FROM files f JOIN up ON f.id = up.id WHERE f.parent_id IS NOT NULL
		)
		SELECT `+nodeCols+` FROM up JOIN files f ON f.id = up.id LEFT JOIN file_contents c ON c.id = f.content_id
		ORDER BY up.depth DESC`, id))
}

func lookup(ctx context.Context, q querier, parent int64, name string) (id int64, dir bool, err error) {
	err = q.QueryRowContext(ctx, "SELECT id, is_dir FROM files WHERE COALESCE(parent_id, 0) = ? AND key = ?",
		parent, nameKey(name)).Scan(&id, &dir)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, dir, err
}

// Resolve finds the node at a slash-separated path ("" is the top level,
// which gives nil). Names match ignoring case.
func Resolve(ctx context.Context, b *bag.Bag, p string) (*Node, error) {
	names, err := SplitPath(p)
	if err != nil {
		return nil, err
	}
	var id int64
	for i, name := range names {
		next, dir, err := lookup(ctx, b.R, id, name)
		if err != nil {
			return nil, err
		}
		if next == 0 || (!dir && i < len(names)-1) {
			return nil, ErrNotFound
		}
		id = next
	}
	if id == 0 {
		return nil, nil
	}
	return Get(ctx, b, id)
}

// Browse describes the node at path p: its ancestors and, for a folder
// (or the top level), its children.
func Browse(ctx context.Context, b *bag.Bag, p string) (*Listing, error) {
	n, err := Resolve(ctx, b, p)
	if err != nil {
		return nil, err
	}
	return browse(ctx, b, n)
}

// BrowseID is Browse by id (0: the top level).
func BrowseID(ctx context.Context, b *bag.Bag, id int64) (*Listing, error) {
	if id == 0 {
		return browse(ctx, b, nil)
	}
	n, err := Get(ctx, b, id)
	if err != nil {
		return nil, err
	}
	return browse(ctx, b, n)
}

func browse(ctx context.Context, b *bag.Bag, n *Node) (*Listing, error) {
	l := &Listing{Node: n, Path: []Node{}}
	var id int64
	if n != nil {
		id = n.ID
		up, err := ancestors(ctx, b.R, n.ID)
		if err != nil {
			return nil, err
		}
		up[len(up)-1] = *n // with its size
		l.Path = up
	}
	if n == nil || n.Dir {
		kids, err := List(ctx, b, id)
		if err != nil {
			return nil, err
		}
		l.Children = kids
	}
	return l, nil
}

// PathOf returns the slash-separated path of a node.
func PathOf(ctx context.Context, b *bag.Bag, id int64) (string, error) {
	up, err := ancestors(ctx, b.R, id)
	if err != nil {
		return "", err
	}
	if len(up) == 0 {
		return "", ErrNotFound
	}
	p := ""
	for i, n := range up {
		if i > 0 {
			p += "/"
		}
		p += n.Name
	}
	return p, nil
}

// Folders lists every folder (for choosing where to move things).
func Folders(ctx context.Context, b *bag.Bag) ([]Node, error) {
	return scanNodes(b.R.QueryContext(ctx, "SELECT "+nodeCols+nodeFrom+
		" WHERE f.is_dir ORDER BY f.name COLLATE NOCASE, f.id"))
}

// GetSummary totals what is stored.
func GetSummary(ctx context.Context, b *bag.Bag) (Summary, error) {
	var s Summary
	err := b.R.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM files WHERE NOT is_dir),
		(SELECT count(*) FROM files WHERE is_dir),
		(SELECT COALESCE(sum(size), 0) FROM files WHERE NOT is_dir),
		(SELECT COALESCE(sum(size), 0) FROM file_contents WHERE sha256 IS NOT NULL)`).Scan(&s.Files, &s.Folders, &s.Bytes, &s.Stored)
	return s, err
}

// freeName returns name, or name with " (n)" if that is taken in parent.
func freeName(ctx context.Context, q querier, parent int64, name string, dir bool) (string, error) {
	for n := 1; ; n++ {
		cand := name
		if n > 1 {
			cand = numbered(name, n, dir)
		}
		id, _, err := lookup(ctx, q, parent, cand)
		if err != nil || id == 0 {
			return cand, err
		}
	}
}

func insertDir(ctx context.Context, q querier, parent int64, name string, modified int64) (int64, error) {
	now := bag.NowMillis()
	if modified == 0 {
		modified = now
	}
	res, err := q.ExecContext(ctx, `INSERT INTO files(parent_id, name, key, is_dir, modified_at, created_at)
		VALUES (?, ?, ?, 1, ?, ?)`, nullID(parent), name, nameKey(name), modified, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// MakeDir creates a folder.
func MakeDir(ctx context.Context, b *bag.Bag, parent int64, name string) (*Node, error) {
	name, err := CleanName(name)
	if err != nil {
		return nil, err
	}
	var id int64
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		if err := folder(ctx, tx, parent); err != nil {
			return err
		}
		if other, _, err := lookup(ctx, tx, parent, name); err != nil {
			return err
		} else if other != 0 {
			return conflictf(name)
		}
		id, err = insertDir(ctx, tx, parent, name, 0)
		return err
	})
	if err != nil {
		return nil, err
	}
	return Get(ctx, b, id)
}

func conflictf(name string) error {
	return &conflictErr{name}
}

type conflictErr struct{ name string }

func (e *conflictErr) Error() string {
	return "there is already something called “" + e.name + "” here"
}
func (e *conflictErr) Is(t error) bool { return t == ErrConflict }

// ensureDirs walks (creating as needed) the folders names below parent and
// returns the last one.
func ensureDirs(ctx context.Context, q querier, parent int64, names []string, created *int) (int64, error) {
	for _, name := range names {
		id, dir, err := lookup(ctx, q, parent, name)
		if err != nil {
			return 0, err
		}
		switch {
		case id == 0:
			if id, err = insertDir(ctx, q, parent, name, 0); err != nil {
				return 0, err
			}
			if created != nil {
				*created++
			}
		case !dir:
			return 0, invalidf("a file called “%s” is in the way of a folder of that name", name)
		}
		parent = id
	}
	return parent, nil
}

// MakeDirs creates the folders on a relative path (keeping those that
// exist) and returns the last.
func MakeDirs(ctx context.Context, b *bag.Bag, parent int64, p string) (int64, error) {
	names, err := SplitPath(p)
	if err != nil {
		return 0, err
	}
	var id int64
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		if err := folder(ctx, tx, parent); err != nil {
			return err
		}
		id, err = ensureDirs(ctx, tx, parent, names, nil)
		return err
	})
	return id, err
}

// Rename renames a file or folder. Changing only the case is allowed.
func Rename(ctx context.Context, b *bag.Bag, id int64, name string) (*Node, error) {
	name, err := CleanName(name)
	if err != nil {
		return nil, err
	}
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		n, err := get(ctx, tx, id)
		if err != nil {
			return err
		}
		if other, _, err := lookup(ctx, tx, n.ParentID, name); err != nil {
			return err
		} else if other != 0 && other != id {
			return conflictf(name)
		}
		_, err = tx.ExecContext(ctx, "UPDATE files SET name = ?, key = ? WHERE id = ?", name, nameKey(name), id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return Get(ctx, b, id)
}

// MoveResult reports a move.
type MoveResult struct {
	Moved int `json:"moved"`
	// Renamed counts items given a " (n)" name because the destination
	// already had something of that name.
	Renamed int `json:"renamed"`
}

// Move puts files and folders into the folder parent (0: the top level).
// Names already taken there are kept apart with " (n)".
func Move(ctx context.Context, b *bag.Bag, ids []int64, parent int64) (MoveResult, error) {
	var r MoveResult
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		r = MoveResult{}
		if err := folder(ctx, tx, parent); err != nil {
			return err
		}
		up, err := ancestors(ctx, tx, parent)
		if err != nil {
			return err
		}
		for _, id := range ids {
			n, err := get(ctx, tx, id)
			if err != nil {
				return err
			}
			if n.ParentID == parent {
				continue
			}
			if slices.ContainsFunc(up, func(a Node) bool { return a.ID == id }) {
				return invalidf("a folder cannot be moved into itself")
			}
			name, err := freeName(ctx, tx, parent, n.Name, n.Dir)
			if err != nil {
				return err
			}
			if name != n.Name {
				r.Renamed++
			}
			if _, err := tx.ExecContext(ctx, "UPDATE files SET parent_id = ?, name = ?, key = ? WHERE id = ?",
				nullID(parent), name, nameKey(name), id); err != nil {
				return err
			}
			r.Moved++
		}
		return nil
	})
	return r, err
}

// DeleteResult reports a deletion.
type DeleteResult struct {
	Files   int   `json:"files"`
	Folders int   `json:"folders"`
	Bytes   int64 `json:"bytes"`
}

// Delete removes files and folders (with everything in them) for good.
func Delete(ctx context.Context, b *bag.Bag, ids []int64) (DeleteResult, error) {
	var r DeleteResult
	if len(ids) == 0 {
		return r, nil
	}
	js, _ := json.Marshal(ids)
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		r = DeleteResult{}
		if err := tx.QueryRowContext(ctx, `WITH RECURSIVE sub(id) AS (
				SELECT id FROM files WHERE id IN (SELECT value FROM json_each(?))
				UNION
				SELECT f.id FROM files f JOIN sub ON f.parent_id = sub.id
			)
			SELECT COALESCE(sum(NOT f.is_dir), 0), COALESCE(sum(f.is_dir), 0), COALESCE(sum(f.size), 0)
			FROM sub JOIN files f ON f.id = sub.id`, string(js)).Scan(&r.Files, &r.Folders, &r.Bytes); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM files WHERE id IN (SELECT value FROM json_each(?))", string(js)); err != nil {
			return err
		}
		return gcContents(ctx, tx)
	})
	if err == nil && r.Bytes > 0 {
		_, err = b.W.ExecContext(ctx, "PRAGMA incremental_vacuum")
	}
	return r, err
}
