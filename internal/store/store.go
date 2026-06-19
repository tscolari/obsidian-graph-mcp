// Package store is the SQLite persistence + graph-query layer for the vault.
// The link graph is modelled as edges (links) between notes; an unresolved
// wikilink keeps dst_id NULL so dangling links are queryable.
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go driver, no cgo
)

const schema = `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS notes (
  id      INTEGER PRIMARY KEY,
  path    TEXT UNIQUE NOT NULL,
  title   TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  mtime   INTEGER NOT NULL DEFAULT 0,
  hash    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS links (
  src_id     INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  dst_id     INTEGER          REFERENCES notes(id) ON DELETE SET NULL, -- NULL = unresolved
  dst_target TEXT NOT NULL,
  PRIMARY KEY (src_id, dst_target)
);
CREATE INDEX IF NOT EXISTS idx_links_dst ON links(dst_id);

CREATE TABLE IF NOT EXISTS tags (
  note_id INTEGER NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
  tag     TEXT NOT NULL,
  PRIMARY KEY (note_id, tag)
);
CREATE INDEX IF NOT EXISTS idx_tags_tag ON tags(tag);

CREATE INDEX IF NOT EXISTS idx_notes_title ON notes(lower(title));
`

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) ApplySchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

// UpsertNote writes the note row and reports whether content actually changed.
// When unchanged, callers can skip the link/tag rewrite (incremental indexing).
func (s *Store) UpsertNote(ctx context.Context, path, title, content, hash string, mtime int64) (id int64, changed bool, err error) {
	var prevHash string
	switch err = s.db.QueryRowContext(ctx, `SELECT id, hash FROM notes WHERE path = ?`, path).Scan(&id, &prevHash); err {
	case nil:
		if prevHash == hash {
			return id, false, nil
		}
	case sql.ErrNoRows:
		// new note
	default:
		return 0, false, err
	}
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO notes (path, title, content, mtime, hash) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET title=excluded.title, content=excluded.content,
		                                mtime=excluded.mtime, hash=excluded.hash
		RETURNING id`, path, title, content, mtime, hash).Scan(&id)
	return id, true, err
}

// ReplaceLinks rewrites all outgoing edges for a note (targets unresolved here;
// call ResolveLinks afterwards to populate dst_id).
func (s *Store) ReplaceLinks(ctx context.Context, srcID int64, targets []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err = tx.ExecContext(ctx, `DELETE FROM links WHERE src_id = ?`, srcID); err != nil {
		return err
	}
	for _, t := range targets {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO links (src_id, dst_target) VALUES (?, ?)`, srcID, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ReplaceTags(ctx context.Context, noteID int64, tags []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err = tx.ExecContext(ctx, `DELETE FROM tags WHERE note_id = ?`, noteID); err != nil {
		return err
	}
	for _, t := range tags {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO tags (note_id, tag) VALUES (?, ?)`, noteID, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ResolveLinks matches every link target to a note by (case-insensitive) title.
// Cheap to run wholesale after a full index; for big vaults resolve per-changed-note.
func (s *Store) ResolveLinks(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE links SET dst_id = (
			SELECT n.id FROM notes n WHERE lower(n.title) = lower(links.dst_target) LIMIT 1
		)`)
	return err
}

type Ref struct {
	Title string `json:"title"`
	Path  string `json:"path"`
}

type Hit struct {
	Title   string `json:"title"`
	Path    string `json:"path"`
	Snippet string `json:"snippet"`
}

// Search is a placeholder LIKE scan; swap for an FTS5 virtual table for scale.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT title, path, substr(content, 1, 200)
		FROM notes
		WHERE title LIKE '%'||?||'%' OR content LIKE '%'||?||'%'
		ORDER BY title LIMIT ?`, query, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.Title, &h.Path, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) ReadNote(ctx context.Context, ref string) (title, path, content string, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT title, path, content FROM notes
		WHERE lower(title) = lower(?) OR path = ? LIMIT 1`, ref, ref).Scan(&title, &path, &content)
	return
}

// Links returns directed outlinks and backlinks for a note title.
func (s *Store) Links(ctx context.Context, title string) (out, back []Ref, err error) {
	if out, err = s.queryRefs(ctx, `
		SELECT n.title, n.path FROM links l
		JOIN notes s ON s.id = l.src_id JOIN notes n ON n.id = l.dst_id
		WHERE lower(s.title) = lower(?) ORDER BY n.title`, title); err != nil {
		return
	}
	back, err = s.queryRefs(ctx, `
		SELECT n.title, n.path FROM links l
		JOIN notes d ON d.id = l.dst_id JOIN notes n ON n.id = l.src_id
		WHERE lower(d.title) = lower(?) ORDER BY n.title`, title)
	return
}

type Neighbor struct {
	Title string `json:"title"`
	Path  string `json:"path"`
	Depth int    `json:"depth"`
}

// Neighborhood returns the undirected n-hop neighbourhood of a note: edges are
// followed in both directions (outlinks + backlinks), bounded by maxDepth, with
// each node reported at its shortest depth. This is the curated-graph payoff —
// "what's related to X within 2 hops" without any embeddings.
func (s *Store) Neighborhood(ctx context.Context, start string, maxDepth int) ([]Neighbor, error) {
	const q = `
WITH RECURSIVE
  seed(id) AS (SELECT id FROM notes WHERE lower(title) = lower(?)),
  nbh(id, depth) AS (
    SELECT id, 0 FROM seed
    UNION
    SELECT CASE WHEN l.src_id = nbh.id THEN l.dst_id ELSE l.src_id END, nbh.depth + 1
    FROM nbh JOIN links l ON (l.src_id = nbh.id OR l.dst_id = nbh.id)
    WHERE nbh.depth < ?
      AND (CASE WHEN l.src_id = nbh.id THEN l.dst_id ELSE l.src_id END) IS NOT NULL
  )
SELECT n.title, n.path, MIN(nbh.depth) AS depth
FROM nbh JOIN notes n ON n.id = nbh.id
GROUP BY n.id ORDER BY depth, n.title;`
	rows, err := s.db.QueryContext(ctx, q, start, maxDepth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Neighbor
	for rows.Next() {
		var n Neighbor
		if err := rows.Scan(&n.Title, &n.Path, &n.Depth); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) NotesByTag(ctx context.Context, tag string) ([]Ref, error) {
	return s.queryRefs(ctx, `
		SELECT n.title, n.path FROM tags t JOIN notes n ON n.id = t.note_id
		WHERE lower(t.tag) = lower(?) ORDER BY n.title`, tag)
}

type Dangling struct {
	From   string `json:"from"`
	Target string `json:"target"`
}

func (s *Store) Dangling(ctx context.Context) ([]Dangling, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.title, l.dst_target FROM links l JOIN notes s ON s.id = l.src_id
		WHERE l.dst_id IS NULL ORDER BY s.title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dangling
	for rows.Next() {
		var d Dangling
		if err := rows.Scan(&d.From, &d.Target); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) queryRefs(ctx context.Context, q string, args ...any) ([]Ref, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ref
	for rows.Next() {
		var r Ref
		if err := rows.Scan(&r.Title, &r.Path); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
