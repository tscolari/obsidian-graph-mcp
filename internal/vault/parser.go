// Package vault parses Obsidian markdown notes: wikilinks, embeds, frontmatter
// tags and inline #hashtags. It is deliberately dependency-free; for full
// YAML frontmatter swap parseTags for gopkg.in/yaml.v3.
package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// [[target]] | [[target|alias]] | [[target#heading]] | ![[embed]] | [[folder/target]]
var wikilinkRe = regexp.MustCompile(`!?\[\[([^\]\[|#]+)(?:#[^\]\[|]+)?(?:\|[^\]\[]+)?\]\]`)

var (
	fenceRe      = regexp.MustCompile("(?s)```.*?```") // fenced code blocks
	inlineCodeRe = regexp.MustCompile("`[^`]*`")       // inline `code`
	hashtagRe    = regexp.MustCompile(`(?m)(?:^|\s)#([A-Za-z0-9_][A-Za-z0-9_/-]*)`)
)

// Link is one wikilink edge out of a note. Rel is the edge type: "" for a body
// wikilink, or the (lowercased) frontmatter property name for a frontmatter link
// (e.g. "origin", "references"). Frontmatter links are the hand-curated,
// typed relations; body links are incidental mentions.
type Link struct {
	Target string // basename-normalised link target
	Rel    string // "" = body link; else frontmatter property name
}

// Note is the parsed view of a single markdown file.
type Note struct {
	Title string   // filename without .md, unless overridden by frontmatter title
	Tags  []string // frontmatter tags + inline #hashtags, deduped
	Links []Link   // body + frontmatter wikilinks, deduped by (rel, target)
	Hash  string   // content hash, for incremental indexing
}

// ParseNote parses one note. title is normally the filename stem; if the
// frontmatter declares a title it wins.
func ParseNote(title, content string) Note {
	fm, body := splitFrontmatter(content)
	if t := frontmatterScalar(fm, "title"); t != "" {
		title = t
	}

	seen := map[string]struct{}{} // dedup key: rel + "\x00" + target
	var links []Link
	add := func(target, rel string) {
		if target == "" {
			return
		}
		key := rel + "\x00" + target
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		links = append(links, Link{Target: target, Rel: rel})
	}

	for _, m := range wikilinkRe.FindAllStringSubmatch(stripCode(body), -1) {
		add(basename(m[1]), "")
	}
	for _, l := range frontmatterLinks(fm) {
		add(l.Target, l.Rel)
	}

	sum := sha256.Sum256([]byte(content))
	return Note{
		Title: title,
		Tags:  parseTags(fm, body),
		Links: links,
		Hash:  hex.EncodeToString(sum[:8]),
	}
}

// frontmatterLinks extracts every [[wikilink]] from frontmatter property values,
// tagging each with its property name (lowercased) as the relation. It handles
// inline scalars (`Origin: "[[X]]"`) and block lists (`References:\n  - "[[A]]"`),
// takes only [[...]] items (plain scalars like Jira IDs are ignored), and skips
// template placeholders such as [[<% ... %>]] and [[{{date:...}}]].
func frontmatterLinks(fm string) []Link {
	var out []Link
	var rel string // current property name, lowercased
	emit := func(s string) {
		if rel == "" {
			return
		}
		for _, m := range wikilinkRe.FindAllStringSubmatch(s, -1) {
			raw := m[1]
			if strings.Contains(raw, "<%") || strings.Contains(raw, "{{") {
				continue // template placeholder, not a real link
			}
			if t := basename(raw); t != "" {
				out = append(out, Link{Target: t, Rel: rel})
			}
		}
	}
	for _, ln := range strings.Split(fm, "\n") {
		if item, ok := strings.CutPrefix(strings.TrimSpace(ln), "- "); ok {
			emit(item) // block-list item under the current key
			continue
		}
		key, val, ok := strings.Cut(ln, ":")
		if !ok || key == "" || key[0] == ' ' || key[0] == '\t' {
			continue // not a top-level key line
		}
		rel = strings.ToLower(strings.TrimSpace(key))
		emit(val) // inline scalar value on the same line
	}
	return out
}

func stripCode(s string) string {
	s = fenceRe.ReplaceAllString(s, "")
	return inlineCodeRe.ReplaceAllString(s, "")
}

// splitFrontmatter returns (frontmatter, body). Minimal on purpose.
func splitFrontmatter(content string) (fm, body string) {
	if !strings.HasPrefix(content, "---\n") {
		return "", content
	}
	rest := content[4:]
	if i := strings.Index(rest, "\n---"); i >= 0 {
		return rest[:i], strings.TrimPrefix(rest[i+4:], "\n")
	}
	return "", content
}

func frontmatterScalar(fm, key string) string {
	for _, ln := range strings.Split(fm, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(ln), key+":"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

func parseTags(fm, body string) []string {
	set := map[string]struct{}{}
	lines := strings.Split(fm, "\n")
	for i, ln := range lines {
		rest, ok := strings.CutPrefix(strings.TrimSpace(ln), "tags:")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, "[") { // inline array: tags: [a, b]
			for _, p := range strings.Split(strings.Trim(rest, "[]"), ",") {
				if p = strings.TrimSpace(p); p != "" {
					set[p] = struct{}{}
				}
			}
		}
		for j := i + 1; j < len(lines); j++ { // block list: tags:\n  - a
			item := strings.TrimSpace(lines[j])
			if d, ok := strings.CutPrefix(item, "- "); ok {
				set[strings.TrimSpace(d)] = struct{}{}
			} else if item != "" {
				break
			}
		}
	}
	for _, m := range hashtagRe.FindAllStringSubmatch(stripCode(body), -1) {
		set[m[1]] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	return out
}

func basename(target string) string {
	target = strings.TrimSpace(target)
	if i := strings.LastIndex(target, "/"); i >= 0 {
		target = target[i+1:]
	}
	return strings.TrimSpace(target)
}
