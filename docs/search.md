# Search

How vellum's full-text search works — for the workspace palette (⌘K), the
`GET /api/search` endpoint and the `search_notes` MCP tool. All three share
one engine and one cache.

## What a query matches

- **Case-insensitive** — `roadmap` finds `Roadmap`.
- **Diacritics-insensitive**, both directions — `ukol` finds `úkol`,
  `přílohy` finds text written as `prilohy`. Snippets always show the
  original accented text.
- **Substring** — `roadm` finds `roadmap` (works for search-as-you-type).
- **Typo-tolerant** — a term of 4–7 letters may miss by one edit, 8 or more
  letters by two (`preklapy` finds `překlepy`). Terms under 4 letters must
  match exactly, so short words never fuzz into each other. A typo hit is
  always ranked below any exact hit, and the snippet highlights the actual
  word from the note.
- **Multi-word queries AND their terms** — `vellum deploy` returns notes
  containing both, in any position; each term independently gets the full
  treatment above.
- `#tag` words in the palette (or `tags=` in the API) filter through the
  metadata index before any content is looked at.

## Ranking

Where a term matches decides the score, per term:

| Match | Weight |
|---|---|
| Title starts with the term | highest |
| Term at a word start in the title | high |
| Term inside a title word | medium |
| Exact tag / tag substring | medium |
| Path substring | low |
| Body hits (word-start hits count double, capped) | low, additive |
| Typo match | below all exact hits |

Multi-word queries get a bonus when the title contains the words in order.
Ties break by modification time (newest first), then path. Snippets pick the
best lines: a line with the whole phrase beats a line with more terms beats
a line with fewer.

## API

```
GET /api/search?q=<query>&tags=a,b&dir=projects&limit=8
```

- `q` — the query; empty with `tags` set = pure tag filter (no snippets).
- `tags` — comma-separated, AND semantics.
- `dir` — limit to a vault subtree.
- `limit` — max results (default 50; the palette asks for 8).
- Add `regex=true` on the MCP tool for line-by-line regular-expression
  matching (case-insensitive, no folding or typo tolerance).

## Performance

Search is a two-phase scan over an in-memory content cache:

1. A parallel scoring sweep over the tag/dir-narrowed candidates. No snippet
   work, near-zero allocations.
2. Snippet extraction for the top `limit` results only.

The cache holds each note pre-folded (plus a unique-word list for the typo
matcher) and is invalidated per note by the metadata index's modTime+size —
a write through the REST API or any MCP tool refreshes exactly the notes it
touched. First search after boot reads the vault once; everything after
that is RAM.

Measured on a 2 000-note vault (40 lines each, Apple M4 Pro), warm, single
query: exact 0.38 ms · accent-folded multi-term 0.74 ms · worst-case typo
0.39 ms · no match 0.35 ms. Reproduce with:

```sh
go test ./internal/vault/ -run '^$' -bench BenchmarkSearch -benchmem
```

There is deliberately no inverted index (bleve & co.): at vault scale a
ranked RAM scan is faster than the bookkeeping an index costs, and there is
nothing to corrupt or rebuild. The `Searcher` interface keeps the door open
if a truly huge vault ever needs one.
