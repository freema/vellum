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
  match exactly, so short words never fuzz into each other. A typo is
  only tried when the term matches nothing exactly in that note; it then
  counts half of an exact match in the same place, and the snippet
  highlights the actual word from the note.
- **Czech word forms**: when a term is nowhere in a note, its common
  Czech case ending is cut off and the rest is matched as the start of a
  word: `faktury` finds `faktura`, `fakturu` and `faktur`, `dlouhým` finds
  `dlouhé`. The endings are the short list of a "light" Czech stemmer
  (Dolamic and Savoy), not a full stemmer, and what remains must keep at
  least 4 letters (`psa` is not cut). Such a match counts
  `0.6 · len / (len + 0.3 · extra letters)` of an exact one (about 0.57
  for `faktur` in `fakturu`); where a typo match is also possible, the
  better of the two counts. The snippet highlights the word from the note.
- **Multi-word queries AND their terms** — `vellum deploy` returns notes
  containing both, in any position; each term independently gets the full
  treatment above.
- **Relaxed when nothing matches every word**: a long query in natural
  language often holds one word the right note does not use. When no note
  holds every term, search returns the notes that miss exactly one, flagged
  `relaxed: true` (MCP `search_notes` and `GET /api/search`). The missing
  word adds nothing to their score, so they score lower, and a note counts
  only when the words it does hold carry more than half the query's rarity:
  a query word that no note uses is the rarest of all, so a note holding
  only the common rest is not returned. Queries of one word, regex search
  and the legacy ranker never relax.
- `#tag` words in the palette (or `tags=` in the API) filter through the
  metadata index before any content is looked at.

## Ranking

Results are ranked with BM25F, the field-weighted variant of the standard
BM25 formula, computed during the scan (no index, no library). Each note has
five fields:

| Field | Weight | Length normalisation (b) |
|---|---|---|
| Title | 5 | 0.3 |
| File name and path | 3 | 0 |
| Headings in the body | 3 | 0.5 |
| Tags | 3 | 0 |
| Body (after the frontmatter, headings left out) | 1 | 0.75 |

Per term, the occurrences in each field are weighted, divided by how long
the field is against its average, and summed. The sum is saturated
(k1 = 1.2: the tenth occurrence adds much less than the second) and
multiplied by how rare the term is among the notes searched (IDF). So:

- **Rare words count more.** In `projekt dotace`, a note about the subsidy
  (`dotace` is in 2 notes) beats a note that says `projekt` in every
  paragraph (`projekt` is in 40).
- **Short notes on the topic beat long notes that mention it.** A
  five-line note about espresso beats a long café log that repeats the
  word in passing.
- An occurrence at a word start counts 1, one inside a word (`faktur` in
  `nefakturovano`) counts half.
- A typo match counts half of an exact one, a Czech word form 0.6 or a
  little less (see above), the missing word of a relaxed match nothing.

The rarity and the average field lengths are taken over the notes left
after the `tags` and `dir` filters, so notes outside the filter do not move
the scores. Nothing is stored for this beyond a few numbers per note in the
search cache.

**Score.** Every ranked hit carries `score`, the BM25F sum divided by the
sum of the terms' rarity: always between 0 and 1, independent of the number
of words in the query, and not relative to the best hit (a weak match stays
weak even when it is the only one). Rounded to three decimals. On the golden
set ([search-quality.md](search-quality.md)) about 0.7 and up means the
terms are in the title, a heading or a tag, or all over the body; 0.4 to 0.7
is a mention in the body; under 0.3 is weak: a passing mention in a long
note, a typo or a part of a longer word. On the golden set every answer
found through a Czech word form scores above 0.3. A relaxed match scores
what the words it holds earn, without the missing one. Regex search and a
pure tag filter rank nothing and carry no score.

**`min_score`** (MCP `search_notes`) drops hits below the given score before
paging, so `total` counts only the hits that pass. Default 0 keeps all.
`0.3` keeps the clear matches; leave it at 0 when a typo or another word
form (Czech inflection) may be all there is, because those score low.

Ties (equal rounded scores) break by modification time (newest first), then
path. Snippets pick the best lines: a line with the whole phrase beats a
line with more terms beats a line with fewer.

`VELLUM_RANKER=legacy` switches back to the previous hand-tuned points
while BM25F rolls out, to compare both on the same vault; that ranker and
the switch go away once BM25F is accepted. The legacy ranker matches as it
always did: no Czech word forms, no relaxed AND.

## API

```
GET /api/search?q=<query>&tags=a,b&dir=projects&limit=8
```

- `q` — the query; empty with `tags` set = pure tag filter (no snippets).
- `tags` — comma-separated, AND semantics.
- `dir` — limit to a vault subtree.
- `limit` — max results (default 50; the palette asks for 8).
- Each result carries `score` (0 to 1) as described under Ranking; the web
  UI does not show it yet.
- The answer has `relaxed: true` when no note held every word and the
  results miss one each (the key is absent otherwise).
- Add `regex=true` on the MCP tool for line-by-line regular-expression
  matching (case-insensitive, no folding or typo tolerance).

The `search_notes` MCP tool takes the same filters (`query`, `tags`, `dir`,
`regex`) and shapes the answer for agents:

- `max_results`: notes per call, default 20 (the REST API and the web UI
  stay at 50). `offset` skips the first N of the ranking to fetch the next
  page; results are ranked deterministically, so pages do not overlap.
- Every answer carries `total` (notes the query matched in all) and
  `truncated` (more follow this page). A truncated answer also has a
  `hint`, for example `19 more results: narrow with tags or dir, or
  continue with offset: 20`.
- `response_format`: `detailed` (default) returns snippets with context;
  `concise` returns per note only `path`, `title` and `line` (the file line
  of the first match, frontmatter counted, ready for `read_note`'s
  `start_line`) and `score`, without the text of the line. A page of 20
  concise hits is about 2.6 kB of `structuredContent` and 1.9 kB of text
  on the measurement fixture, whose paths are 60 bytes and longer (see
  [payload-baseline.md](payload-baseline.md)), and about 2.0 kB and 1.2 kB
  with short paths.
- `context_lines` (default 2, `0` for the matching line alone) and
  `max_snippets` (default 3) tune the detailed snippets.
- `min_score` (0 to 1, default 0) drops weak hits, see Ranking.
- `relaxed: true` (and a header saying the notes miss one query word each)
  marks an answer from the relaxed AND, see What a query matches.

## Performance

Search is a two-phase scan over an in-memory content cache:

1. A parallel scoring sweep over the tag/dir-narrowed candidates. No snippet
   work, near-zero allocations.
2. Snippet extraction for the top `limit` results only.

The cache holds each note pre-folded (plus a unique-word list for the typo
matcher, with body counts per word, and the heading texts and word count per
field for BM25F) and is invalidated per note by the metadata index's
modification time (nanoseconds) and size: a write through the REST API or any
MCP tool refreshes exactly the notes it touched, and so does a rescan that
finds a note changed outside vellum (see below). First search after boot
reads the vault once; everything after that is RAM.

The cache takes a note while it holds fewer than 8,192 notes or less than
256 MiB, so a vault of short notes is cached whole well past 8,192 notes.
Once both bounds are reached it keeps what it holds and reads the remaining
notes from disk on each search, so a very large vault gets slower
gradually rather than all at once. Deleted and moved notes leave the cache
when it fills. In memory a cached note takes about three times its size on
disk; the metadata index about 1.2 KB per note.

Realistic notes (300 to 700 words from a 40,000-word vocabulary, Czech
diacritics), Apple M4 Pro, warm, one query:

| Notes (on disk) | Typical query | Heap after the first search |
|---|---|---|
| 5,000 (22 MB) | 3.5 to 7 ms | 68 MiB |
| 15,000 (65 MB) | 9 to 21 ms | 204 MiB |
| 30,000 (130 MB) | about 0.45 s (cache full) | 319 MiB |

A query whose words no single note holds all of runs a second, relaxed
pass and takes about twice as long.

Measured on a 2 000-note vault (40 lines each, Apple M4 Pro), warm, single
query: exact 0.38 ms · accent-folded multi-term 0.74 ms · worst-case typo
0.39 ms · no match 0.35 ms. Reproduce with:

```sh
go test ./internal/vault/ -run '^$' -bench BenchmarkSearch -benchmem
```

Ranking quality (MRR, nDCG@10, recall@5 on a golden query set) and the
latency and memory on 5 000 notes are tracked in
[search-quality.md](search-quality.md).

## Notes changed outside vellum

The metadata index (tags, backlinks, tasks, folder descriptions, the list
search scans) lives in memory. Writes through vellum update it at once.
Changes made elsewhere, such as an edit in Obsidian or VS Code, a `git pull`,
or a second vellum process over the same folder (Claude Code and Claude
Desktop each starting their own `vellum mcp`), are picked up by a lazy
rescan:

- Before a call that reads the index (search, tags, backlinks, tasks, the
  curator tools, folder descriptions, and their REST counterparts), vellum
  checks whether the last rescan is older than `VELLUM_RESCAN` (default
  `5s`). If so, it walks the vault and compares each note's size and
  modification time with the index. Only new and changed notes are read;
  notes gone from disk are dropped; tags and links are recomputed once at
  the end, however many notes changed.
- Only one rescan runs at a time. Calls that arrive meanwhile do not wait;
  they answer from the current index.
- There is no background thread and no file watcher. An idle server does
  no work, and the rescan behaves the same on Docker bind mounts and
  network drives, where file watching is unreliable.
- Subscribers of `vellum://note/{path}` get `resources/updated` for notes
  the rescan found changed, added or removed.
- A note renamed outside vellum looks like a delete plus an add. When, in
  one rescan, exactly one note disappears and exactly one appears with the
  same file name and size, vellum treats it as a move, and a share link
  follows the note ([sharing.md](sharing.md)). Anything else stays a
  delete plus an add.
- `VELLUM_RESCAN` takes a duration (`5s`, `30s`, `1m`) or a number of
  seconds; `off` turns the rescan off, and changes made outside vellum then
  show after a restart, as before.

Cost: a rescan of a vault where nothing changed stats every note and opens
none. Measured on Apple M4 Pro: about 4 ms for 2 000 notes and 11 ms for
5 000 notes. Reproduce with:

```sh
go test ./internal/vault/ -run '^$' -bench BenchmarkReconcileUnchanged -benchmem
```

Known limits:

- An edit that changes neither the size nor the modification time is not
  noticed. With the nanosecond timestamps of today's file systems this is
  rare.
- A change becomes visible once the interval has passed, at the next call
  that reads the index, not the moment the file is saved.
- The web workspace does not refresh by itself when a note changes outside
  vellum; it shows the change on the next load.

There is deliberately no inverted index (bleve & co.): at vault scale a
ranked RAM scan is faster than the bookkeeping an index costs, and there is
nothing to corrupt or rebuild. The `Searcher` interface keeps the door open
if a truly huge vault ever needs one.
