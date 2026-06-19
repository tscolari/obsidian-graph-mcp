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

// Note is the parsed view of a single markdown file.
type Note struct {
	Title string   // filename without .md, unless overridden by frontmatter title
	Tags  []string // frontmatter tags + inline #hashtags, deduped
	Links []string // raw wikilink targets, basename-normalised, deduped, code stripped
	Hash  string   // content hash, for incremental indexing
}

// ParseNote parses one note. title is normally the filename stem; if the
// frontmatter declares a title it wins.
func ParseNote(title, content string) Note {
	fm, body := splitFrontmatter(content)
	if t := frontmatterScalar(fm, "title"); t != "" {
		title = t
	}
	clean := stripCode(body)

	seen := map[string]struct{}{}
	var links []string
	for _, m := range wikilinkRe.FindAllStringSubmatch(clean, -1) {
		t := basename(m[1])
		if t == "" {
			continue
		}
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			links = append(links, t)
		}
	}

	sum := sha256.Sum256([]byte(content))
	return Note{
		Title: title,
		Tags:  parseTags(fm, body),
		Links: links,
		Hash:  hex.EncodeToString(sum[:8]),
	}
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
