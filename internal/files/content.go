package files

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"photobag/internal/bag"
)

// ChunkSize is the size of the pieces contents are stored in.
const ChunkSize = 1 << 20

// pending is content being written: its chunks are stored but it has no
// sha256 yet, which keeps it safe from gcContents.
type pending struct {
	id      int64
	size    int64
	sha256  [32]byte
	sniffed string
}

// writePending stores everything read from r. Chunks are committed as they
// arrive, so a long upload never holds the bag's writer.
func writePending(ctx context.Context, b *bag.Bag, r io.Reader) (*pending, error) {
	p := &pending{}
	err := b.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "INSERT INTO file_contents(created_at) VALUES (?)", bag.NowMillis())
		if err != nil {
			return err
		}
		p.id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	buf := make([]byte, ChunkSize)
	for seq := 0; ; seq++ {
		n, rerr := io.ReadFull(r, buf)
		if n > 0 {
			if seq == 0 {
				p.sniffed = http.DetectContentType(buf[:min(n, 512)])
			}
			h.Write(buf[:n])
			p.size += int64(n)
			if err := b.Tx(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, "INSERT INTO file_chunks(content_id, seq, data) VALUES (?, ?, ?)", p.id, seq, buf[:n])
				return err
			}); err != nil {
				discard(b, p.id)
				return nil, err
			}
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			discard(b, p.id)
			return nil, rerr
		}
	}
	if p.size == 0 {
		p.sniffed = http.DetectContentType(nil)
	}
	copy(p.sha256[:], h.Sum(nil))
	return p, nil
}

// finish turns pending content into stored content (or drops it in favour
// of identical content already stored) and returns the content id.
func (p *pending) finish(ctx context.Context, tx *sql.Tx) (int64, error) {
	var same int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM file_contents WHERE sha256 = ?", p.sha256[:]).Scan(&same)
	switch {
	case err == nil:
		_, err = tx.ExecContext(ctx, "DELETE FROM file_contents WHERE id = ?", p.id)
		return same, err
	case !errors.Is(err, sql.ErrNoRows):
		return 0, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE file_contents SET sha256 = ?, size = ?, sniffed = ? WHERE id = ?",
		p.sha256[:], p.size, p.sniffed, p.id)
	return p.id, err
}

// discard drops an unfinished content.
func discard(b *bag.Bag, id int64) {
	ctx := context.Background()
	_ = b.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM file_contents WHERE id = ? AND sha256 IS NULL", id)
		return err
	})
}

// gcContents drops contents no file uses (unfinished uploads excepted).
func gcContents(ctx context.Context, q querier) error {
	_, err := q.ExecContext(ctx, `DELETE FROM file_contents WHERE sha256 IS NOT NULL
		AND NOT EXISTS (SELECT 1 FROM files WHERE files.content_id = file_contents.id)`)
	return err
}

// Tidy removes what interrupted uploads left behind (unfinished contents
// over an hour old, so another process's upload in progress is safe), and
// contents no file uses.
func Tidy(ctx context.Context, b *bag.Bag) error {
	return b.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM file_contents WHERE sha256 IS NULL AND created_at < ?",
			bag.NowMillis()-time.Hour.Milliseconds()); err != nil {
			return err
		}
		return gcContents(ctx, tx)
	})
}

// PutOptions say where a file goes.
type PutOptions struct {
	// Parent is the folder Path is relative to (0: the top level).
	Parent int64
	// Path is the file's path below Parent, ending in its name. Missing
	// folders on the way are created.
	Path string
	// Modified is the file's modification time (unix ms); 0 means now.
	Modified int64
	// Conflict is ConflictRename (default), ConflictReplace or ConflictSkip.
	Conflict string
	// Folders, when set, is increased by the number of folders created.
	Folders *int
}

// Store writes the content read from r as a file. Content identical to
// something already stored is kept once.
func Store(ctx context.Context, b *bag.Bag, r io.Reader, o PutOptions) (*PutResult, error) {
	names, err := SplitPath(o.Path)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, invalidf("a file name is required")
	}
	conflict, err := validConflict(o.Conflict)
	if err != nil {
		return nil, err
	}
	if err := folder(ctx, b.R, o.Parent); err != nil {
		return nil, err
	}
	p, err := writePending(ctx, b, r)
	if err != nil {
		return nil, err
	}
	now := bag.NowMillis()
	if o.Modified <= 0 {
		o.Modified = now
	}
	var id int64
	outcome := Added
	err = b.Tx(ctx, func(tx *sql.Tx) error {
		content, err := p.finish(ctx, tx)
		if err != nil {
			return err
		}
		if err := folder(ctx, tx, o.Parent); err != nil {
			return err
		}
		dir, err := ensureDirs(ctx, tx, o.Parent, names[:len(names)-1], o.Folders)
		if err != nil {
			return err
		}
		name := names[len(names)-1]
		existing, isDir, err := lookup(ctx, tx, dir, name)
		if err != nil {
			return err
		}
		if existing != 0 {
			switch {
			case conflict == ConflictSkip:
				id, outcome = existing, Skipped
				return gcContents(ctx, tx)
			case conflict == ConflictReplace && !isDir:
				id, outcome = existing, Replaced
				if _, err := tx.ExecContext(ctx, "UPDATE files SET content_id = ?, size = ?, modified_at = ? WHERE id = ?",
					content, p.size, o.Modified, id); err != nil {
					return err
				}
				return gcContents(ctx, tx)
			}
			if name, err = freeName(ctx, tx, dir, name, false); err != nil {
				return err
			}
			outcome = Renamed
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO files(parent_id, name, key, is_dir, content_id, size, modified_at, created_at)
			VALUES (?, ?, ?, 0, ?, ?, ?, ?)`, nullID(dir), name, nameKey(name), content, p.size, o.Modified, now)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		discard(b, p.id)
		return nil, err
	}
	n, err := Get(ctx, b, id)
	if err != nil {
		return nil, err
	}
	return &PutResult{Node: n, Outcome: outcome}, nil
}

// Exists reports whether something is already at path below parent.
func Exists(ctx context.Context, b *bag.Bag, parent int64, p string) (bool, error) {
	names, err := SplitPath(p)
	if err != nil || len(names) == 0 {
		return false, err
	}
	id := parent
	for _, name := range names {
		next, _, err := lookup(ctx, b.R, id, name)
		if err != nil || next == 0 {
			return false, err
		}
		id = next
	}
	return true, nil
}

// Reader reads a file's content; it seeks, so http.ServeContent can answer
// range requests.
type Reader struct {
	ctx     context.Context
	db      *sql.DB
	content int64
	size    int64
	off     int64
	seq     int64
	buf     []byte
}

// Open opens a file for reading.
func Open(ctx context.Context, b *bag.Bag, id int64) (*Reader, *Node, error) {
	n, err := Get(ctx, b, id)
	if err != nil {
		return nil, nil, err
	}
	if n.Dir {
		return nil, nil, invalidf("“%s” is a folder", n.Name)
	}
	var content int64
	if err := b.R.QueryRowContext(ctx, "SELECT content_id FROM files WHERE id = ?", id).Scan(&content); err != nil {
		return nil, nil, err
	}
	return &Reader{ctx: ctx, db: b.R, content: content, size: n.Size, seq: -1}, n, nil
}

// Size is the content length.
func (r *Reader) Size() int64 { return r.size }

func (r *Reader) Read(p []byte) (int, error) {
	if r.off >= r.size {
		return 0, io.EOF
	}
	seq := r.off / ChunkSize
	if seq != r.seq {
		r.buf = r.buf[:0]
		if err := r.db.QueryRowContext(r.ctx, "SELECT data FROM file_chunks WHERE content_id = ? AND seq = ?",
			r.content, seq).Scan(&r.buf); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				err = fmt.Errorf("file content is missing (chunk %d)", seq)
			}
			return 0, err
		}
		r.seq = seq
	}
	i := r.off - seq*ChunkSize
	if i >= int64(len(r.buf)) {
		return 0, io.ErrUnexpectedEOF
	}
	n := copy(p, r.buf[i:])
	r.off += int64(n)
	return n, nil
}

// Seek implements io.Seeker.
func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += r.off
	case io.SeekEnd:
		offset += r.size
	default:
		return 0, errors.New("invalid whence")
	}
	if offset < 0 {
		return 0, errors.New("negative position")
	}
	r.off = offset
	return offset, nil
}
