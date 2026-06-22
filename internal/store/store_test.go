package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.ApplySchema(context.Background()); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return st
}

// seed builds a small fixed graph and returns the note ids by title.
//
//	body links:   Alpha -> Beta, Gamma, Missing(unresolved); Beta -> Alpha, Gamma; Gamma -> Delta
//	origin chain: Gamma =origin=> Beta =origin=> Alpha =origin=> Missing(unresolved)
//	tags: topic on Alpha and Delta
func seed(t *testing.T, st *Store) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	ids := map[string]int64{}
	for _, title := range []string{"Alpha", "Beta", "Gamma", "Delta"} {
		id, _, err := st.UpsertNote(ctx, title+".md", title, "body of "+title, "h-"+title, 1)
		if err != nil {
			t.Fatalf("upsert %s: %v", title, err)
		}
		ids[title] = id
	}
	links := map[string][]LinkInput{
		"Alpha": {{Target: "Beta"}, {Target: "Gamma"}, {Target: "Missing"}, {Target: "Missing", Rel: "origin"}},
		"Beta":  {{Target: "Alpha"}, {Target: "Gamma"}, {Target: "Alpha", Rel: "origin"}},
		"Gamma": {{Target: "Delta"}, {Target: "Beta", Rel: "origin"}},
	}
	for src, in := range links {
		if err := st.ReplaceLinks(ctx, ids[src], in); err != nil {
			t.Fatalf("links %s: %v", src, err)
		}
	}
	for _, title := range []string{"Alpha", "Delta"} {
		if err := st.ReplaceTags(ctx, ids[title], []string{"topic"}); err != nil {
			t.Fatalf("tags %s: %v", title, err)
		}
	}
	if err := st.ResolveLinks(ctx); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return ids
}

func titles(refs []Ref) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Title
	}
	sort.Strings(out)
	return out
}

// bodyTitles returns only the titles reached by a body link (rel ""), sorted.
func bodyTitles(refs []Ref) []string {
	var out []string
	for _, r := range refs {
		if r.Rel == "" {
			out = append(out, r.Title)
		}
	}
	sort.Strings(out)
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestApplySchema_MigratesV1Links(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t) // already at current schema; simulate a v1 links table
	if _, err := st.db.ExecContext(ctx, `
		DROP TABLE links;
		CREATE TABLE links (
		  src_id INTEGER NOT NULL, dst_id INTEGER, dst_target TEXT NOT NULL,
		  PRIMARY KEY (src_id, dst_target)
		);
		PRAGMA user_version = 0;`); err != nil {
		t.Fatal(err)
	}
	// Seed a note + an old-style (rel-less) link and a stale hash.
	id, _, err := st.UpsertNote(ctx, "n.md", "N", "body", "stalehash", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO links(src_id, dst_target) VALUES (?, 'X')`, id); err != nil {
		t.Fatal(err)
	}

	// Re-applying the schema must detect the rel-less table and rebuild it.
	if err := st.ApplySchema(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var nLinks int
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM links`).Scan(&nLinks); err != nil {
		t.Fatal(err)
	}
	if nLinks != 0 {
		t.Errorf("links not dropped on migration: %d rows", nLinks)
	}
	var hash string
	if err := st.db.QueryRowContext(ctx, `SELECT hash FROM notes WHERE id=?`, id).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != "" {
		t.Errorf("note hash not cleared on migration: %q (would skip re-index)", hash)
	}
	// New rel column is usable.
	if err := st.ReplaceLinks(ctx, id, []LinkInput{{Target: "X", Rel: "origin"}}); err != nil {
		t.Errorf("rel column unusable after migration: %v", err)
	}
}

func TestUpsertNote_ChangedFlag(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	id1, changed, err := st.UpsertNote(ctx, "n.md", "N", "v1", "hash1", 1)
	if err != nil || !changed {
		t.Fatalf("new note: changed=%v err=%v", changed, err)
	}

	id2, changed, err := st.UpsertNote(ctx, "n.md", "N", "v1", "hash1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("re-upsert with same hash should report changed=false")
	}
	if id1 != id2 {
		t.Errorf("id changed across upserts: %d vs %d", id1, id2)
	}

	_, changed, err = st.UpsertNote(ctx, "n.md", "N2", "v2", "hash2", 2)
	if err != nil || !changed {
		t.Fatalf("changed hash: changed=%v err=%v", changed, err)
	}
	title, _, content, err := st.ReadNote(ctx, "n.md")
	if err != nil {
		t.Fatal(err)
	}
	if title != "N2" || content != "v2" {
		t.Errorf("row not updated: title=%q content=%q", title, content)
	}
}

func TestResolveLinks(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seed(t, st)

	// Case-insensitive title match: Beta's body outlinks to "Alpha"/"Gamma" resolve.
	out, _, err := st.Links(ctx, "beta") // also exercises case-insensitive lookup
	if err != nil {
		t.Fatal(err)
	}
	if got := bodyTitles(out); !eq(got, []string{"Alpha", "Gamma"}) {
		t.Errorf("resolved body outlinks = %v, want [Alpha Gamma]", got)
	}

	// "Missing" has no matching note, so it stays unresolved → dangling. Alpha
	// links to it both in body ("") and via origin, so both edges are reported
	// with their rel.
	d, err := st.Dangling(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rels := map[string]bool{}
	for _, dl := range d {
		if dl.From != "Alpha" || dl.Target != "Missing" {
			t.Errorf("unexpected dangling: %+v", dl)
		}
		rels[dl.Rel] = true
	}
	if len(d) != 2 || !rels[""] || !rels["origin"] {
		t.Errorf("dangling = %+v, want body + origin edges to Missing", d)
	}
}

func TestNeighborhood(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seed(t, st)

	depthOf := func(ns []Neighbor) map[string]int {
		m := map[string]int{}
		for _, n := range ns {
			m[n.Title] = n.Depth
		}
		return m
	}

	d1, err := st.Neighborhood(ctx, "Alpha", 1, nil, "both")
	if err != nil {
		t.Fatal(err)
	}
	m := depthOf(d1)
	if m["Alpha"] != 0 || m["Beta"] != 1 || m["Gamma"] != 1 {
		t.Errorf("depth-1 neighborhood = %v", m)
	}
	if _, ok := m["Delta"]; ok {
		t.Error("Delta should not appear at depth 1")
	}

	d2 := depthOf(mustNbh(t, st, "Alpha", 2))
	if d2["Delta"] != 2 {
		t.Errorf("Delta depth = %d, want 2 (shortest)", d2["Delta"])
	}
	// Beta is reachable directly (d1) despite also being on a longer cycle path.
	if d2["Beta"] != 1 {
		t.Errorf("Beta depth = %d, want 1 (shortest, cycle-safe)", d2["Beta"])
	}

	// Undirected: starting from Delta (which has no outlinks) still reaches
	// Gamma via backlink at d1, and Beta/Alpha at d2.
	fromDelta := depthOf(mustNbh(t, st, "Delta", 2))
	if fromDelta["Gamma"] != 1 {
		t.Errorf("Delta->Gamma depth = %d, want 1 (backlink traversal)", fromDelta["Gamma"])
	}
	if fromDelta["Alpha"] != 2 || fromDelta["Beta"] != 2 {
		t.Errorf("undirected reach from Delta = %v", fromDelta)
	}
}

func mustNbh(t *testing.T, st *Store, start string, depth int) []Neighbor {
	t.Helper()
	ns, err := st.Neighborhood(context.Background(), start, depth, nil, "both")
	if err != nil {
		t.Fatal(err)
	}
	return ns
}

func TestNeighborhood_RelFilter(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seed(t, st)

	// Following only origin edges from Gamma: Gamma -> Beta -> Alpha (directed
	// origin edges, but neighbourhood is undirected so this is the reachable set).
	ns, err := st.Neighborhood(ctx, "Gamma", 3, []string{"origin"}, "both")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, n := range ns {
		got[n.Title] = n.Depth
	}
	if got["Gamma"] != 0 || got["Beta"] != 1 || got["Alpha"] != 2 {
		t.Errorf("origin-only neighbourhood = %v, want Gamma0 Beta1 Alpha2", got)
	}
	// Delta is reachable from Gamma only by a body edge, so it must be excluded.
	if _, ok := got["Delta"]; ok {
		t.Errorf("Delta leaked into origin-only neighbourhood: %v", got)
	}
}

// TestNeighborhood_Direction reproduces the "downstream child leaks into
// background" bug. Beta is the entry note (think: a career matrix). Beta's own
// origin points up to Alpha (its background). Gamma's origin points DOWN at Beta
// (Gamma =origin=> Beta), so Gamma is a note derived FROM Beta — a downstream
// child, not background.
func TestNeighborhood_Direction(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seed(t, st)

	relDir := func(ns []Neighbor) map[string][2]string {
		m := map[string][2]string{}
		for _, n := range ns {
			m[n.Title] = [2]string{n.Rel, n.Dir}
		}
		return m
	}

	// direction=out follows Beta -> Alpha (what Beta draws on) and must NOT
	// surface Gamma, the downstream child whose origin points back at Beta.
	out, err := st.Neighborhood(ctx, "Beta", 1, []string{"origin"}, "out")
	if err != nil {
		t.Fatal(err)
	}
	od := relDir(out)
	if got, ok := od["Alpha"]; !ok || got != [2]string{"origin", "out"} {
		t.Errorf("out: Alpha = %v (ok=%v), want origin/out", got, ok)
	}
	if _, ok := od["Gamma"]; ok {
		t.Errorf("out: downstream child Gamma leaked into background: %v", od)
	}

	// direction=in finds the downstream child Gamma and not the background Alpha.
	in, err := st.Neighborhood(ctx, "Beta", 1, []string{"origin"}, "in")
	if err != nil {
		t.Fatal(err)
	}
	id := relDir(in)
	if got, ok := id["Gamma"]; !ok || got != [2]string{"origin", "in"} {
		t.Errorf("in: Gamma = %v (ok=%v), want origin/in", got, ok)
	}
	if _, ok := id["Alpha"]; ok {
		t.Errorf("in: background Alpha leaked into downstream view: %v", id)
	}

	// direction=both surfaces both, each labelled with how it connects.
	both, err := st.Neighborhood(ctx, "Beta", 1, []string{"origin"}, "both")
	if err != nil {
		t.Fatal(err)
	}
	bd := relDir(both)
	if got := bd["Alpha"]; got != [2]string{"origin", "out"} {
		t.Errorf("both: Alpha = %v, want origin/out", got)
	}
	if got := bd["Gamma"]; got != [2]string{"origin", "in"} {
		t.Errorf("both: Gamma = %v, want origin/in", got)
	}
}

func TestOriginChain(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seed(t, st)

	// Gamma =origin=> Beta =origin=> Alpha =origin=> Missing(unresolved, stops).
	chain, err := st.OriginChain(ctx, "Gamma", 10)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for i, n := range chain {
		if n.Depth != i {
			t.Errorf("chain[%d] depth = %d, want %d", i, n.Depth, i)
		}
		order = append(order, n.Title)
	}
	if !eq(order, []string{"Gamma", "Beta", "Alpha"}) {
		t.Errorf("origin chain = %v, want [Gamma Beta Alpha] (stops at unresolved Missing)", order)
	}

	// Depth bound truncates the walk.
	short, err := st.OriginChain(ctx, "Gamma", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(short) != 2 || short[1].Title != "Beta" {
		t.Errorf("depth-1 chain = %v, want [Gamma Beta]", short)
	}
}

func TestLinks_CarriesRel(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seed(t, st)

	out, _, err := st.Links(ctx, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	// Beta -> Alpha exists as both a body edge and an origin edge.
	var bodyAlpha, originAlpha bool
	for _, r := range out {
		if r.Title == "Alpha" && r.Rel == "" {
			bodyAlpha = true
		}
		if r.Title == "Alpha" && r.Rel == "origin" {
			originAlpha = true
		}
	}
	if !bodyAlpha || !originAlpha {
		t.Errorf("Beta outlinks missing typed rels: %+v", out)
	}
}

func TestLinks_Directionality(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seed(t, st)

	out, back, err := st.Links(ctx, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	if got := bodyTitles(out); !eq(got, []string{"Alpha", "Gamma"}) {
		t.Errorf("Beta body outlinks = %v, want [Alpha Gamma]", got)
	}
	if got := bodyTitles(back); !eq(got, []string{"Alpha"}) {
		t.Errorf("Beta body backlinks = %v, want [Alpha]", got)
	}
}

func TestNotesByTag(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seed(t, st)

	notes, err := st.NotesByTag(ctx, "TOPIC") // case-insensitive
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(notes); !eq(got, []string{"Alpha", "Delta"}) {
		t.Errorf("tag topic = %v, want [Alpha Delta]", got)
	}
}

func TestSearch(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seed(t, st)

	// Title match.
	if hits, err := st.Search(ctx, "Gamma", 10); err != nil || len(hits) != 1 || hits[0].Title != "Gamma" {
		t.Fatalf("title search = %v err=%v", hits, err)
	}
	// Body match ("body of Delta") with no title hit.
	if hits, err := st.Search(ctx, "of Delta", 10); err != nil || len(hits) != 1 || hits[0].Title != "Delta" {
		t.Fatalf("body search = %v err=%v", hits, err)
	}
	// Limit honored: "body of" matches all 4, cap at 2.
	if hits, err := st.Search(ctx, "body of", 2); err != nil || len(hits) != 2 {
		t.Fatalf("limited search returned %d hits (err=%v), want 2", len(hits), err)
	}
}

func TestReadNote(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seed(t, st)

	// By title, case-insensitive.
	title, path, content, err := st.ReadNote(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if title != "Alpha" || path != "Alpha.md" || content != "body of Alpha" {
		t.Errorf("by title: %q %q %q", title, path, content)
	}

	// By exact path.
	title, _, _, err = st.ReadNote(ctx, "Delta.md")
	if err != nil {
		t.Fatal(err)
	}
	if title != "Delta" {
		t.Errorf("by path: title = %q, want Delta", title)
	}
}

func TestResolveLinks_PathBased(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	// Two notes with the same title in different folders.
	projID, _, _ := st.UpsertNote(ctx, "projects/Alpha.md", "Alpha", "", "h1", 1)
	jrnlID, _, _ := st.UpsertNote(ctx, "journal/Alpha.md", "Alpha", "", "h2", 1)
	srcID, _, _ := st.UpsertNote(ctx, "src.md", "Src", "", "h3", 1)

	if err := st.ReplaceLinks(ctx, srcID, []LinkInput{
		{Target: "projects/Alpha"}, // explicit path → should pick projects/Alpha.md
		{Target: "journal/Alpha"},  // explicit path → should pick journal/Alpha.md
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolveLinks(ctx); err != nil {
		t.Fatal(err)
	}

	var projResolved, jrnlResolved int64
	if err := st.db.QueryRowContext(ctx,
		`SELECT dst_id FROM links WHERE src_id=? AND dst_target=?`, srcID, "projects/Alpha",
	).Scan(&projResolved); err != nil {
		t.Fatalf("query projects/Alpha: %v", err)
	}
	if err := st.db.QueryRowContext(ctx,
		`SELECT dst_id FROM links WHERE src_id=? AND dst_target=?`, srcID, "journal/Alpha",
	).Scan(&jrnlResolved); err != nil {
		t.Fatalf("query journal/Alpha: %v", err)
	}
	if projResolved != projID {
		t.Errorf("projects/Alpha resolved to id %d, want %d (projects note)", projResolved, projID)
	}
	if jrnlResolved != jrnlID {
		t.Errorf("journal/Alpha resolved to id %d, want %d (journal note)", jrnlResolved, jrnlID)
	}
}

func TestResolveLinks_WrongPathStaysDangling(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	// Note exists, but the link uses a wrong path — should stay dangling, not fall back to title.
	st.UpsertNote(ctx, "notes/Alpha.md", "Alpha", "", "h1", 1)
	srcID, _, _ := st.UpsertNote(ctx, "src.md", "Src", "", "h2", 1)

	if err := st.ReplaceLinks(ctx, srcID, []LinkInput{
		{Target: "wrong/Alpha"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolveLinks(ctx); err != nil {
		t.Fatal(err)
	}

	var dstID sql.NullInt64
	if err := st.db.QueryRowContext(ctx,
		`SELECT dst_id FROM links WHERE src_id=? AND dst_target=?`, srcID, "wrong/Alpha",
	).Scan(&dstID); err != nil {
		t.Fatalf("query: %v", err)
	}
	if dstID.Valid {
		t.Errorf("wrong-path link should be dangling (dst_id NULL), got %d", dstID.Int64)
	}
}

func TestResolveLinks_BareTargetUnchanged(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	noteID, _, _ := st.UpsertNote(ctx, "notes/Alpha.md", "Alpha", "", "h1", 1)
	srcID, _, _ := st.UpsertNote(ctx, "src.md", "Src", "", "h2", 1)

	if err := st.ReplaceLinks(ctx, srcID, []LinkInput{{Target: "Alpha"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolveLinks(ctx); err != nil {
		t.Fatal(err)
	}

	var dstID int64
	if err := st.db.QueryRowContext(ctx,
		`SELECT dst_id FROM links WHERE src_id=? AND dst_target=?`, srcID, "Alpha",
	).Scan(&dstID); err != nil {
		t.Fatalf("query: %v", err)
	}
	if dstID != noteID {
		t.Errorf("bare target: resolved to %d, want %d", dstID, noteID)
	}
}
