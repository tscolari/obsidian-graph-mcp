// Package index walks an Obsidian vault and feeds it into the store.
package index

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tscolari/obsidian-graph-mcp/internal/store"
	"github.com/tscolari/obsidian-graph-mcp/internal/vault"
)

type Stats struct {
	Seen, Changed int
}

// Vault indexes every .md file under root. It is incremental: notes whose
// content hash is unchanged skip the link/tag rewrite. Link resolution runs
// once at the end so newly-added notes resolve previously-dangling links.
func Vault(ctx context.Context, st *store.Store, root string) (Stats, error) {
	var stats Stats
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// skip Obsidian's config and hidden dirs
			if name := d.Name(); strings.HasPrefix(name, ".") && path != root {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		stats.Seen++

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

		n := vault.ParseNote(stem, string(raw))
		id, changed, err := st.UpsertNote(ctx, rel, n.Title, string(raw), n.Hash, info.ModTime().Unix())
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		stats.Changed++
		links := make([]store.LinkInput, len(n.Links))
		for i, l := range n.Links {
			links[i] = store.LinkInput{Target: l.Target, Rel: l.Rel}
		}
		if err := st.ReplaceLinks(ctx, id, links); err != nil {
			return err
		}
		return st.ReplaceTags(ctx, id, n.Tags)
	})
	if err != nil {
		return stats, err
	}
	return stats, st.ResolveLinks(ctx)
}
