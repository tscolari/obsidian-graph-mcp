package store

import (
	"context"
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
//	Alpha -> Beta, Gamma, Missing(unresolved)
//	Beta  -> Alpha, Gamma
//	Gamma -> Delta
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
	links := map[string][]string{
		"Alpha": {"Beta", "Gamma", "Missing"},
		"Beta":  {"Alpha", "Gamma"},
		"Gamma": {"Delta"},
	}
	for src, targets := range links {
		if err := st.ReplaceLinks(ctx, ids[src], targets); err != nil {
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

	// Case-insensitive title match: Beta's outlink to "Gamma" resolves.
	out, _, err := st.Links(ctx, "beta") // also exercises case-insensitive lookup
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(out); !eq(got, []string{"Alpha", "Gamma"}) {
		t.Errorf("resolved outlinks = %v, want [Alpha Gamma]", got)
	}

	// "Missing" has no matching note, so it stays unresolved → dangling.
	d, err := st.Dangling(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 1 || d[0].From != "Alpha" || d[0].Target != "Missing" {
		t.Errorf("dangling = %v, want [{Alpha Missing}]", d)
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

	d1, err := st.Neighborhood(ctx, "Alpha", 1)
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
	ns, err := st.Neighborhood(context.Background(), start, depth)
	if err != nil {
		t.Fatal(err)
	}
	return ns
}

func TestLinks_Directionality(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	seed(t, st)

	out, back, err := st.Links(ctx, "Beta")
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(out); !eq(got, []string{"Alpha", "Gamma"}) {
		t.Errorf("Beta outlinks = %v, want [Alpha Gamma]", got)
	}
	if got := titles(back); !eq(got, []string{"Alpha"}) {
		t.Errorf("Beta backlinks = %v, want [Alpha]", got)
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
