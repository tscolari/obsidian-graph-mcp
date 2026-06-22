# Agent instructions

This file is read by coding agents that support the `AGENTS.md` convention
(Codex, OpenCode, and others). For Claude Code, symlink `CLAUDE.md` to this file:

```sh
ln -s AGENTS.md CLAUDE.md
```

(or add `@AGENTS.md` to an existing `CLAUDE.md`).

To use the protocol below, wire up the `obsidian-graph` MCP server pointed at
your vault — see the README's "Wire into Claude Desktop / Claude Code" section
for the config; Codex and OpenCode take the same `command` + `args` under their
own MCP config.

## Multiple vaults

When several vaults run at once, give each instance a distinct `-name` so its
tools are namespaced separately (`mcp__<name>__search_notes`), and a `-context`
blurb describing what it holds. Then keep this routing table current and pick the
vault whose context matches the question's domain — consult more than one only
when the task spans contexts:

<!-- Edit to match your setup; names are the -name values from your MCP config. -->
- `obsidian-work`     — current job: incidents, projects, people, decisions
- `obsidian-personal` — personal life: reading lists, journaling, notes
- _(add one line per vault you wire up)_

## Knowledge protocol (obsidian-graph MCP)

The user's Obsidian vault is a hand-curated knowledge graph: notes are nodes and
`[[wikilinks]]` are edges, with the most deliberate links in frontmatter
(`Origin`, `References`). Treat it as the user's long-term memory and consult it
as part of your reasoning — not only when explicitly told.

**Look first whenever the task involves:**
- the user's own projects, decisions, incidents, teams, or people
- an internal term, acronym, or project name you can't resolve from the codebase
  or your own knowledge
- "why does X exist?", "where did this come from?", "what's related to Y?"
- writing a doc, ADR, summary, or plan about the user's domain

**Recipe:**
1. `search_notes` — find the entry-point note(s).
2. `neighborhood` — gather the context the user hand-linked around it. Prefer
   `rels=["origin","references"]` to follow curated frontmatter relations and
   skip incidental body mentions. Each node is labelled with the relation and
   direction it was reached by (e.g. `origin (in)`). For "what is X / background
   on X" questions, pass `direction="out"` to get what the note itself draws on:
   a node reached by an **incoming** `origin` is a note derived *from* X (a
   downstream child), not background — don't read it for a definition or rubric
   question. Use `direction="in"` to find that downstream work; `both` (default)
   sweeps everything.
3. `origin_chain` — when the question is about provenance ("why/where from"),
   trace `Origin` to its root.
4. `read_note` — read the specific notes you judged relevant.
5. `notes_by_tag` / `note_links` — when you need a category or a note's exact
   connections rather than a fuzzy search.

**Then:**
- Fold what you find into your answer; cite sources by note title, e.g.
  `[[Career Matrix]]`, so the reasoning is traceable.
- If `dangling_links` shows the user references a note that doesn't exist
  (especially a dangling `Origin`/`References`), flag the gap.

The graph is **read-only and cheap** — when in doubt, look first, then reason.
Scope the lookups to the triggers above; don't search on every turn.
