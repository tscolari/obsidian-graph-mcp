package vault

import (
	"sort"
	"testing"
)

// containsAll reports whether got holds every want value (order-independent).
func containsAll(got []string, want ...string) bool {
	set := make(map[string]struct{}, len(got))
	for _, g := range got {
		set[g] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func TestParseNote_Links(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string // exact set, order-independent
	}{
		{"plain", "see [[Beta]]", []string{"Beta"}},
		{"alias", "see [[sub/Gamma|Gamma]]", []string{"Gamma"}},
		{"heading", "see [[Delta#heading]]", []string{"Delta"}},
		{"embed", "see ![[Image]]", []string{"Image"}},
		{"folder basename", "see [[a/b/Note]]", []string{"Note"}},
		{"folder+heading+alias", "see [[folder/N#h|alias]]", []string{"N"}},
		{"multiple", "[[Beta]] and [[Delta]]", []string{"Beta", "Delta"}},
		{"none", "no links here", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseNote("Stem", tt.content).Links
			if len(got) != len(tt.want) {
				t.Fatalf("links = %v, want %v", got, tt.want)
			}
			if !containsAll(got, tt.want...) {
				t.Fatalf("links = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseNote_StripsCode(t *testing.T) {
	content := "real [[Beta]]\n" +
		"inline `[[NotALink]]` ignored\n" +
		"```\n[[Nope]] in fence\n```\n"
	got := ParseNote("Stem", content).Links
	if !containsAll(got, "Beta") {
		t.Fatalf("expected Beta in %v", got)
	}
	for _, bad := range []string{"NotALink", "Nope"} {
		if containsAll(got, bad) {
			t.Errorf("code-stripped link %q leaked into %v", bad, got)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want exactly [Beta], got %v", got)
	}
}

func TestParseNote_DedupLinks(t *testing.T) {
	got := ParseNote("Stem", "[[Beta]] then [[Beta]] again").Links
	if len(got) != 1 || got[0] != "Beta" {
		t.Fatalf("want single Beta, got %v", got)
	}
}

func TestParseNote_FrontmatterTitleOverride(t *testing.T) {
	withTitle := "---\ntitle: Alpha\n---\nbody"
	if got := ParseNote("Stem", withTitle).Title; got != "Alpha" {
		t.Errorf("title = %q, want Alpha (frontmatter wins)", got)
	}
	if got := ParseNote("Stem", "no frontmatter").Title; got != "Stem" {
		t.Errorf("title = %q, want Stem (filename fallback)", got)
	}
}

func TestParseNote_Tags(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{"inline array", "---\ntags: [foo, bar]\n---\nbody", []string{"foo", "bar"}},
		{"block list", "---\ntags:\n  - a\n  - b\n---\nbody", []string{"a", "b"}},
		{"inline hashtag", "body with #project tag", []string{"project"}},
		{"dedup fm+inline", "---\ntags: [project]\n---\nalso #project", []string{"project"}},
		{"hashtag in code ignored", "text `#nope` and #yes", []string{"yes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseNote("Stem", tt.content).Tags
			if len(got) != len(tt.want) || !containsAll(got, tt.want...) {
				t.Fatalf("tags = %v, want %v", sorted(got), sorted(tt.want))
			}
		})
	}
}

func TestParseNote_Hash(t *testing.T) {
	a := ParseNote("Stem", "content one").Hash
	again := ParseNote("Stem", "content one").Hash
	b := ParseNote("Stem", "content two").Hash
	if a == "" {
		t.Fatal("hash is empty")
	}
	if a != again {
		t.Errorf("hash unstable for identical content: %q vs %q", a, again)
	}
	if a == b {
		t.Errorf("hash collided for different content: %q", a)
	}
}

func TestParseNote_FrontmatterEdgeCases(t *testing.T) {
	// Empty content: no panic, no links/tags, filename title kept.
	n := ParseNote("Stem", "")
	if n.Title != "Stem" || len(n.Links) != 0 || len(n.Tags) != 0 {
		t.Errorf("empty content gave %+v", n)
	}

	// Unterminated frontmatter is treated as no frontmatter: title stays the
	// stem and the would-be frontmatter line does not become a tag.
	un := ParseNote("Stem", "---\ntitle: Ghost\nbody with no close")
	if un.Title != "Stem" {
		t.Errorf("unterminated frontmatter: title = %q, want Stem", un.Title)
	}
}
