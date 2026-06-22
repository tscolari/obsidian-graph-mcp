// Package store is the SQLite persistence + graph-query layer for the vault.
// The link graph is modelled as edges (links) between notes; an unresolved
// wikilink keeps dst_id NULL so dangling links are queryable.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

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
  rel        TEXT NOT NULL DEFAULT '', -- edge type: '' = body link, else frontmatter property
  PRIMARY KEY (src_id, dst_target, rel)
);
CREATE INDEX IF NOT EXISTS idx_links_dst ON links(dst_id);
CREATE INDEX IF NOT EXISTS idx_links_rel ON links(rel);

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
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// HTTP mode serves concurrent requests on separate goroutines; a single
	// connection (atop WAL + busy_timeout above) avoids SQLITE_BUSY races
	// between MCP tool reads and a /reindex write without an app-level mutex.
	db.SetMaxOpenConns(1)
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// schemaVersion bumps whenever the on-disk shape changes. The DB is a cache
// rebuildable from the vault, so an older version is migrated by dropping the
// affected table and letting the next index repopulate it.
const schemaVersion = 3

func (s *Store) ApplySchema(ctx context.Context) error {
	// v1 (which never set user_version, so it reads as 0) had a links table
	// without the rel column / composite PK. Detect it by the missing column
	// rather than the version, then drop it and clear note hashes so the next
	// index treats every note as changed and rewrites its edges (UpsertNote
	// otherwise skips unchanged notes).
	stale, err := s.linksTableStale(ctx)
	if err != nil {
		return err
	}
	if stale {
		if _, err := s.db.ExecContext(ctx, `DROP TABLE IF EXISTS links; UPDATE notes SET hash = '';`); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}

	// v2 → v3: dst_target now preserves folder prefixes (e.g. "folder/Note"
	// instead of "Note"). Clear links and reset hashes so the next index
	// re-parses every note and repopulates edges with the new targets.
	var version int
	_ = s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version)
	if version < 3 {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM links; UPDATE notes SET hash = '';`); err != nil {
			return err
		}
	}

	_, err = s.db.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion))
	return err
}

// linksTableStale reports whether a links table exists but predates the rel
// column (a v1 cache that must be rebuilt).
func (s *Store) linksTableStale(ctx context.Context) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(links)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	exists, hasRel := false, false
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		exists = true
		if name == "rel" {
			hasRel = true
		}
	}
	return exists && !hasRel, rows.Err()
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

// LinkInput is one outgoing edge to write: a target plus its relation type
// ("" for a body link, else the frontmatter property name).
type LinkInput struct {
	Target string
	Rel    string
}

// ReplaceLinks rewrites all outgoing edges for a note (targets unresolved here;
// call ResolveLinks afterwards to populate dst_id).
func (s *Store) ReplaceLinks(ctx context.Context, srcID int64, links []LinkInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err = tx.ExecContext(ctx, `DELETE FROM links WHERE src_id = ?`, srcID); err != nil {
		return err
	}
	for _, l := range links {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO links (src_id, dst_target, rel) VALUES (?, ?, ?)`, srcID, l.Target, l.Rel); err != nil {
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

// ResolveLinks matches every link target to a note. Three passes:
//
// ResolveLinks resolves wikilink targets to note IDs in two passes:
//  1. Path match for targets containing "/" — the author wrote [[folder/Note]]
//     explicitly, so honour the path. Unresolved path-prefixed links stay dangling.
//  2. Title match for bare targets (no "/") — existing behaviour.
func (s *Store) ResolveLinks(ctx context.Context) error {
	// Pass 1: path match for explicit folder/note targets.
	if _, err := s.db.ExecContext(ctx, `
		UPDATE links SET dst_id = (
			SELECT n.id FROM notes n
			WHERE lower(n.path) = lower(links.dst_target||'.md')
			   OR lower(n.path) LIKE lower('%/'||links.dst_target||'.md')
			LIMIT 1
		) WHERE dst_id IS NULL AND instr(dst_target, '/') > 0`); err != nil {
		return err
	}

	// Pass 2: title match for bare targets.
	if _, err := s.db.ExecContext(ctx, `
		UPDATE links SET dst_id = (
			SELECT n.id FROM notes n WHERE lower(n.title) = lower(links.dst_target) LIMIT 1
		) WHERE dst_id IS NULL AND instr(dst_target, '/') = 0`); err != nil {
		return err
	}

	return nil
}

type Ref struct {
	Title string `json:"title"`
	Path  string `json:"path"`
	Rel   string `json:"rel,omitempty"` // edge type, when the query carries one
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

// Links returns directed outlinks and backlinks for a note title. Each Ref
// carries the relation (rel) of the edge: "" for body links, else the
// frontmatter property name (e.g. "origin", "references").
func (s *Store) Links(ctx context.Context, title string) (out, back []Ref, err error) {
	if out, err = s.queryRefsRel(ctx, `
		SELECT n.title, n.path, l.rel FROM links l
		JOIN notes s ON s.id = l.src_id JOIN notes n ON n.id = l.dst_id
		WHERE lower(s.title) = lower(?) ORDER BY l.rel, n.title`, title); err != nil {
		return
	}
	back, err = s.queryRefsRel(ctx, `
		SELECT n.title, n.path, l.rel FROM links l
		JOIN notes d ON d.id = l.dst_id JOIN notes n ON n.id = l.src_id
		WHERE lower(d.title) = lower(?) ORDER BY l.rel, n.title`, title)
	return
}

func (s *Store) queryRefsRel(ctx context.Context, q string, args ...any) ([]Ref, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ref
	for rows.Next() {
		var r Ref
		if err := rows.Scan(&r.Title, &r.Path, &r.Rel); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type Neighbor struct {
	Title string `json:"title"`
	Path  string `json:"path"`
	Depth int    `json:"depth"`
}

// Neighborhood returns the undirected n-hop neighbourhood of a note: edges are
// followed in both directions (outlinks + backlinks), bounded by maxDepth, with
// each node reported at its shortest depth. This is the curated-graph payoff —
// "what's related to X within 2 hops" without any embeddings. When rels is
// non-empty, only edges with those relation types are traversed (e.g. ["origin",
// "references"] to walk only curated frontmatter links, ignoring body mentions).
func (s *Store) Neighborhood(ctx context.Context, start string, maxDepth int, rels []string) ([]Neighbor, error) {
	relFilter, args := "", []any{start, maxDepth}
	if len(rels) > 0 {
		ph := make([]string, len(rels))
		for i, r := range rels {
			ph[i] = "?"
			args = append(args, r)
		}
		relFilter = " AND l.rel IN (" + strings.Join(ph, ",") + ")"
	}
	q := `
WITH RECURSIVE
  seed(id) AS (SELECT id FROM notes WHERE lower(title) = lower(?)),
  nbh(id, depth) AS (
    SELECT id, 0 FROM seed
    UNION
    SELECT CASE WHEN l.src_id = nbh.id THEN l.dst_id ELSE l.src_id END, nbh.depth + 1
    FROM nbh JOIN links l ON (l.src_id = nbh.id OR l.dst_id = nbh.id)
    WHERE nbh.depth < ?
      AND (CASE WHEN l.src_id = nbh.id THEN l.dst_id ELSE l.src_id END) IS NOT NULL` + relFilter + `
  )
SELECT n.title, n.path, MIN(nbh.depth) AS depth
FROM nbh JOIN notes n ON n.id = nbh.id
GROUP BY n.id ORDER BY depth, n.title;`
	rows, err := s.db.QueryContext(ctx, q, args...)
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

// OriginChain follows a note's Origin frontmatter link directionally to its root
// — the provenance/genealogy trace of why the note exists. It walks src→dst over
// rel='origin' edges only, bounded by maxDepth, cycle-safe, stopping at an
// unresolved Origin. The start note is depth 0; its Origin is depth 1, etc.
func (s *Store) OriginChain(ctx context.Context, start string, maxDepth int) ([]Neighbor, error) {
	const q = `
WITH RECURSIVE
  chain(id, depth) AS (
    SELECT id, 0 FROM notes WHERE lower(title) = lower(?)
    UNION
    SELECT l.dst_id, chain.depth + 1
    FROM chain JOIN links l ON l.src_id = chain.id AND l.rel = 'origin'
    WHERE chain.depth < ? AND l.dst_id IS NOT NULL
  )
SELECT n.title, n.path, chain.depth
FROM chain JOIN notes n ON n.id = chain.id
ORDER BY chain.depth;`
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
	Rel    string `json:"rel,omitempty"` // edge type: "" body, else frontmatter property
}

func (s *Store) Dangling(ctx context.Context) ([]Dangling, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.title, l.dst_target, l.rel FROM links l JOIN notes s ON s.id = l.src_id
		WHERE l.dst_id IS NULL ORDER BY s.title, l.rel`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dangling
	for rows.Next() {
		var d Dangling
		if err := rows.Scan(&d.From, &d.Target, &d.Rel); err != nil {
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
