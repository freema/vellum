# Search quality

How good is `search_notes` at putting the right note first, and what does it
cost? This page holds the measuring stick: a fixed corpus, a golden query set
with graded answers, the metrics, and the numbers of the rankers: the
hand-tuned points vellum started with (now `legacy`) and BM25F, the default
since NSO-577.
Any change to ranking (BM25F, Czech word forms, typo weights, the relaxed
AND) is judged against it. How search works is described in
[search.md](search.md).

## Run it

```sh
# quality: per-query table, per-kind summary, latency and heap of the ranker
go test -run TestSearchQuality -v ./internal/vault

# latency percentiles and memory on 5 000 notes, BM25F and legacy
go test ./internal/vault -run '^$' -bench BenchmarkSearch5k -benchtime 300x
```

The quality test scores both rankers, `scan` (the default ScanSearcher,
BM25F) and `legacy`, each against its own floors. A running server picks
the ranker with `VELLUM_RANKER=bm25f|legacy` (default `bm25f`) while BM25F
rolls out; the legacy ranker and the switch go once BM25F is accepted.

Everything runs offline from the repository. The quality test is part of
`go test ./...`, so it runs in CI on every push and pull request. It is
deterministic: the corpus is copied to a temp directory with one fixed
modification time, so ties fall through to the path, and
`TestSearchQualityDeterministic` runs the set twice and requires identical
rankings.

## Corpus

`testdata/search/vault/`: 343 notes, about 210 KB of plain Markdown,
in Czech and English. Daily journal entries, project notes, meeting notes,
engineering notes, recipes, book notes, trips, home, health and finance,
in folders like a real vault (`areas/`, `projects/`, `work/`, `journal/`,
`recipes/`, `reference/`, `travel/`). Notes differ in length (two lines up to
a long diary), use frontmatter titles and tags, inline headings, lists and
tables, and similar topics appear in several folders. About 50 notes are
written by hand as answers and traps for the golden queries, the rest is
background composed from varied per-topic sentences. No private data.

## Golden set

`testdata/search/golden.json`: 47 queries. Each lists the notes that answer
it with a grade:

- **2**: exactly the note the query is after
- **1**: related, a fine result but not the answer
- unlisted: not relevant

Coverage (a query can have several kinds):

| Kind | What it tests |
|---|---|
| `single-word` | one word |
| `multi-word` | two or three words |
| `sentence` | a whole sentence, the way an agent writes it |
| `inflection` | Czech word forms: query `faktury`, notes say `fakturu`, `faktur` |
| `no-diacritics` | query without diacritics against accented text |
| `typo` | a misspelled term |
| `common-rare` | a word that is everywhere next to a rare one |
| `filename-only` | the term is only in the file name |
| `heading-only` | the term is only in a heading |
| `tag-only` | the term is only a tag |
| `long-vs-short` | a long note repeating the word against a short note about it |
| `filter` | a query with a `dir` filter |
| `no-answer` | nothing in the vault answers it |

**Rules.** The golden set was written before any new ranker and does not
change to suit one. A change to `golden.json` or the corpus needs a reason
in the pull request (a wrong judgment, a missing case), never "the new
ranker ranks it differently". `TestGoldenSetValid` keeps the set well
formed: 30 to 50 queries, unique ids, grades 1 or 2, an exact answer for
every answerable query, every path present in the corpus, every kind above
covered.

## Metrics

Each query runs with the search engine's default of 50 results, the page
size of `GET /api/search`. Over MCP, `search_notes` returns pages of 20 by
default: nDCG@10 and recall@5 are the same either way, and MRR differs
only for an answer ranked below 20.

- **MRR**: mean of 1/rank of the first exact answer (grade 2); 0 when it is
  not returned. Over answerable queries.
- **nDCG@10**: graded gain `2^grade - 1`, discount `log2(rank + 1)`,
  normalised by the ideal order of all judged notes.
- **recall@5**: share of all judged notes (grade 1 or 2) in the top 5.
- **No-answer results**: how many results the no-answer queries return
  (ideally none), as a count of empty answers and a mean.
- **Latency**: p50 and p95 over five warm runs of every query.
- **Heap**: retained heap of the metadata index and of the warm searcher.

**Regression gate.** `testdata/search/baseline.json` stores floors per
ranker: MRR, nDCG@10 and recall@5 must not fall below them, the no-answer
mean must not rise above. The floors are the measured values truncated to
four decimals. When a ranker improves, raise them in the same pull request.
`TestSearchQualityGateCatchesRegressions` proves the gate fails for a
reversed ranking and for a ranker that returns everything.

**Another ranker.** The harness takes any `Searcher`. Register a candidate
in `qualityRankers` in `internal/vault/search_quality_test.go` and it gets
its own table next to the current one.

**Scores.** For a ranker that returns `score`, the report adds the spread
(min / median / max) of the top score of answerable and of no-answer
queries, of the exact answers and of the returned notes the golden set does
not judge relevant, and a top score column per query. This is the basis for
the `min_score` advice in the `search_notes` description.

## Baseline: legacy ranker (hand-tuned points)

Measured 2026-11-05, before NSO-577. Ranker `scan` at the time, `legacy`
now (`ScanSearcher` with the hand-tuned points: title 35 to 60, tag 15 to
30, path 10, body 3 per hit, typo 1 to 3).

| Metric | Value |
|---|---|
| MRR | 0.7857 |
| nDCG@10 | 0.8206 |
| recall@5 | 0.8452 |
| answerable queries | 42 |
| no-answer queries returning nothing | 5 of 5 |
| no-answer queries, mean results | 0.00 |
| latency p50 / p95, golden set, 343 notes | 0.15 to 0.49 ms / 0.22 to 4.0 ms (three runs, machine under load) |
| heap: index / warm searcher, 343 notes | about 0.9 MiB / 3.4 MiB |

Per kind:

| Kind | Queries | MRR | nDCG@10 | recall@5 |
|---|---|---|---|---|
| common-rare | 3 | 0.833 | 0.877 | 1.000 |
| filename-only | 2 | 1.000 | 1.000 | 1.000 |
| filter | 1 | 1.000 | 1.000 | 1.000 |
| heading-only | 2 | 1.000 | 1.000 | 1.000 |
| inflection | 9 | 0.500 | 0.536 | 0.556 |
| long-vs-short | 2 | 0.500 | 0.797 | 1.000 |
| multi-word | 18 | 0.861 | 0.868 | 0.889 |
| no-diacritics | 4 | 1.000 | 1.000 | 1.000 |
| sentence | 5 | 0.200 | 0.165 | 0.100 |
| single-word | 19 | 0.868 | 0.948 | 1.000 |
| tag-only | 2 | 1.000 | 1.000 | 1.000 |
| typo | 4 | 0.750 | 0.898 | 1.000 |

Per query (`Rank` is the rank of the first exact answer, `-` when it is not
returned at all):

| Query | Kinds | Rank | nDCG@10 | R@5 | Results |
|---|---|---|---|---|---|
| single-kubernetes | single-word | 1 | 1.000 | 1.000 | 2 |
| single-kvasek | single-word | 1 | 1.000 | 1.000 | 2 |
| single-invoice | single-word | 1 | 1.000 | 1.000 | 3 |
| single-passport | single-word | 1 | 1.000 | 1.000 | 3 |
| single-rozpocet | single-word | 1 | 1.000 | 1.000 | 4 |
| multi-restic-backup | multi-word | 1 | 1.000 | 1.000 | 2 |
| multi-oprava-strechy | multi-word | 1 | 1.000 | 1.000 | 2 |
| multi-postgres-pool | multi-word | 1 | 1.000 | 1.000 | 2 |
| multi-okr-review | multi-word | 1 | 1.000 | 1.000 | 2 |
| multi-sleep-tracking | multi-word | 1 | 1.000 | 1.000 | 1 |
| sentence-tls-rotate | sentence | - | 0.000 | 0.000 | 0 |
| sentence-ucetni-system | sentence, inflection | - | 0.000 | 0.000 | 0 |
| sentence-database-hosting | sentence | - | 0.000 | 0.000 | 0 |
| sentence-kvaskovy-chleb | sentence, inflection | - | 0.000 | 0.000 | 0 |
| sentence-japan-packing | sentence | 1 | 0.826 | 0.500 | 1 |
| inflection-faktury | inflection, single-word | 1 | 1.000 | 1.000 | 2 |
| inflection-najemni-smlouvy | inflection, multi-word | 1 | 1.000 | 1.000 | 1 |
| inflection-ockovani-psa | inflection, multi-word | - | 0.000 | 0.000 | 0 |
| inflection-zahrady | inflection, single-word | 2 | 0.821 | 1.000 | 3 |
| inflection-dovolena-chorvatsko | inflection, multi-word | 1 | 1.000 | 1.000 | 1 |
| inflection-uctenky | inflection, single-word | 1 | 1.000 | 1.000 | 1 |
| inflection-zalohovani-fotek | inflection, multi-word | - | 0.000 | 0.000 | 0 |
| nodiacritics-zarucni-lhuta | no-diacritics, multi-word | 1 | 1.000 | 1.000 | 1 |
| nodiacritics-skoleni-bozp | no-diacritics, multi-word | 1 | 1.000 | 1.000 | 1 |
| nodiacritics-kvaskovy-chleb | no-diacritics, multi-word | 1 | 1.000 | 1.000 | 1 |
| nodiacritics-rozpocet-koupelny | no-diacritics, multi-word | 1 | 1.000 | 1.000 | 1 |
| typo-kubernets | typo, single-word | 2 | 0.797 | 1.000 | 2 |
| typo-postgress-pooling | typo, multi-word | 1 | 1.000 | 1.000 | 1 |
| typo-rekonstukce | typo, multi-word | 1 | 1.000 | 1.000 | 1 |
| typo-wireguad | typo, single-word | 2 | 0.797 | 1.000 | 2 |
| common-rare-projekt-dotace | common-rare, multi-word | 2 | 0.631 | 1.000 | 2 |
| common-rare-deploy-rollback | common-rare, multi-word | 1 | 1.000 | 1.000 | 1 |
| common-rare-recept-bez-lepku | common-rare, multi-word | 1 | 1.000 | 1.000 | 1 |
| filename-k3s | filename-only, single-word | 1 | 1.000 | 1.000 | 1 |
| filename-lisbon | filename-only, single-word | 1 | 1.000 | 1.000 | 2 |
| heading-troubleshooting | heading-only, single-word | 1 | 1.000 | 1.000 | 1 |
| heading-alergeny | heading-only, single-word | 1 | 1.000 | 1.000 | 1 |
| tag-taxes | tag-only, single-word | 1 | 1.000 | 1.000 | 2 |
| tag-woodworking | tag-only, single-word | 1 | 1.000 | 1.000 | 1 |
| long-short-mulcovani | long-vs-short, single-word | 2 | 0.797 | 1.000 | 2 |
| long-short-espresso | long-vs-short, single-word | 2 | 0.797 | 1.000 | 2 |
| filter-dir-rozpocet | filter, single-word | 1 | 1.000 | 1.000 | 2 |
| none-kvantovy-pocitac | no-answer | n/a | 0.000 | 0.000 | 0 |
| none-helicopter-license | no-answer | n/a | 0.000 | 0.000 | 0 |
| none-tiskarna | no-answer, sentence | n/a | 0.000 | 0.000 | 0 |
| none-xylofon | no-answer | n/a | 0.000 | 0.000 | 0 |
| none-blockchain-audit | no-answer | n/a | 0.000 | 0.000 | 0 |

### Where the legacy ranker fails

This is the input for the BM25F weights (NSO-577) and the match quality
experiments (NSO-578).

- **Whole sentences return nothing** (4 of 5: `sentence-tls-rotate`,
  `sentence-database-hosting`, `sentence-ucetni-system`,
  `sentence-kvaskovy-chleb`). Every term must match, so one word the note
  does not contain empties the result: the certificate note misses only
  `do`, the hosting decision misses `what`, `did`, `moving`, the bread
  recipe misses `dlouhým kynutím` (it says `dlouhé studené kynutí`). Only the
  packing-list sentence survives, by luck.
- **Czech inflection** (MRR 0.50, recall@5 0.56). `očkování psa` and
  `zálohování fotek` return nothing: `psa` is too short for the typo budget
  (terms under 4 letters must match exactly), and `zálohování` is too far
  from `záloha` and `zálohuji`. Where a form is within
  one edit (`faktury`/`fakturu`, `smlouvy`/`smlouvu`) the answer is found
  only through the typo fallback, with 3 points instead of tens.
- **Typo and inflection ties are broken by the path.** `kubernets`,
  `wireguad` and `zahrady` give every fuzzy hit the same score, so the
  alphabetically first path wins (`k3s-cluster` before the cheatsheet, the
  homelab log before the WireGuard note, `zahony-bez-plevele` before
  `zahrada-jaro`).
- **Common words outweigh rare ones.** `projekt dotace` puts the project
  rules (`projekt` in almost every paragraph, `dotace` once) above the
  subsidy note: `projekt` is in 40 notes, `dotace` in 2, yet both count the
  same.
- **Long notes beat short ones on topic.** `mulčování` and `espresso` rank
  the long diary and the café log above the short notes that are about the
  topic: body hits are capped at 8 but not normalised by length.
- Exact field matches already work: filename-only, heading-only, tag-only,
  no-diacritics and `dir` filtering are all at 1.0, and every no-answer
  query returns nothing. A new ranker must keep these.

## BM25F (NSO-577)

Measured 2026-11-05 on the same corpus and golden set. Ranker `scan`
(`ScanSearcher`, BM25F): fields title (weight 5, b 0.3), file name and path
(3, 0), headings (3, 0.5), tags (3, 0), body (1, 0.75), k1 1.2, an
occurrence inside a word counts 0.5, a typo match 0.5; statistics over the
candidates after the filters. How it works: [search.md](search.md#ranking).

| Metric | Legacy | BM25F |
|---|---|---|
| MRR | 0.7857 | **0.8333** |
| nDCG@10 | 0.8206 | **0.8433** |
| recall@5 | 0.8452 | 0.8452 |
| no-answer queries returning nothing | 5 of 5 | 5 of 5 |
| exact answers at rank 1 | 30 of 42 | 34 of 42 |

Recall@5 cannot move: both rankers return the same notes (every term must
match, as before), only in another order. `TestBM25FSameResultSetAsLegacy`
checks that on the golden set and on 600 random queries.

Per kind:

| Kind | Queries | MRR legacy | MRR BM25F | nDCG@10 legacy | nDCG@10 BM25F |
|---|---|---|---|---|---|
| common-rare | 3 | 0.833 | **1.000** | 0.877 | **1.000** |
| filename-only | 2 | 1.000 | 1.000 | 1.000 | 1.000 |
| filter | 1 | 1.000 | 1.000 | 1.000 | 1.000 |
| heading-only | 2 | 1.000 | 1.000 | 1.000 | 1.000 |
| inflection | 9 | 0.500 | **0.556** | 0.536 | **0.556** |
| long-vs-short | 2 | 0.500 | **0.750** | 0.797 | **0.898** |
| multi-word | 18 | 0.861 | **0.889** | 0.868 | **0.889** |
| no-diacritics | 4 | 1.000 | 1.000 | 1.000 | 1.000 |
| sentence | 5 | 0.200 | 0.200 | 0.165 | 0.165 |
| single-word | 19 | 0.868 | **0.947** | 0.948 | **0.979** |
| tag-only | 2 | 1.000 | 1.000 | 1.000 | 1.000 |
| typo | 4 | 0.750 | **1.000** | 0.898 | **1.000** |

Queries whose rank of the exact answer changed:

| Query | Legacy | BM25F | Why |
|---|---|---|---|
| common-rare-projekt-dotace | 2 | **1** | `dotace` (2 notes) now outweighs `projekt` (40 notes) |
| long-short-espresso | 2 | **1** | the short grinder note beats the long café log after length normalisation |
| typo-kubernets | 2 | **1** | typo hits are no longer all equal: the cheatsheet has the word in its title |
| typo-wireguad | 2 | **1** | same, the WireGuard note has it in title and headings |
| inflection-zahrady | 2 | **1** | same, `zahrada` sits in the title of the right note |
| single-passport | **1** | 2 | see below |

`single-passport` is the one first place BM25F gives up. The checklist
(the answer, 135 words) and the itinerary (related, 99 words) both say
`passport` exactly once, in the body. The legacy ranker scored them the
same and the path broke the tie in the checklist's favour; BM25F prefers
the shorter note when the evidence is equal. Nothing in the text says the
checklist is more about passports, so this is accepted rather than tuned
around. `TestBM25FKeepsFirstPlaceAnswers` fails for any other query that
loses its first place.

Still failing, out of scope here: the long-vs-short `mulčování` (the long
diary repeats the word 10 times in 245 words against 3 in 61, almost the
same density; body b of 0.9 or more fixes it but loses `single-kvasek`,
whose answer only matches through inflected forms), the four sentence
queries and the two inflection queries that return nothing (every term
must match; NSO-578 takes these on with prefix matching and a relaxed AND).

### Scores and `min_score`

| Score of | min / median / max |
|---|---|
| top hit, answerable queries with results (36) | 0.257 / 0.716 / 0.930 |
| top hit, no-answer queries (5; 0 = no results) | 0 / 0 / 0 |
| exact answers (37) | 0.257 / 0.716 / 0.930 |
| returned notes not judged relevant (6) | 0.295 / 0.472 / 0.593 |

No-answer queries return nothing, so their best score is 0, far below the
answered queries (`TestBM25FNoAnswerScoresLower` requires under half the
median). Among the answers, the bands are: about 0.7 and up when the terms
are in the title, a heading or a tag (one tag or file name hit alone is
0.714) or all over the body; 0.4 to 0.7 for a body mention; under 0.3 for a
passing mention in a long note, a part of a longer word or a typo. The
lowest exact answers (0.257 `faktury`, 0.283 `účtenky`) are Czech word
forms found only through the typo fallback. Hence the advice in the tool
description: `min_score: 0.3` keeps the clear matches, and 0 (the default)
is right when a typo or another word form may be all there is.
`TestBM25FScoreInRange` checks that every score of 600 random queries lies
in 0.001 to 1.

### Tuning and checks

- **Parameter sweep** on the golden set: k1 0.9, 1.2, 1.5, 2.0 against body
  b 0.5, 0.75, 0.9, 1.0; title weight 3, 5, 8; heading, tag and path weight
  1 to 5; inside-word weight 0.25 to 1; typo weight 0.3 to 0.8. Nothing beat
  the starting point of the issue. Body b 0.5 loses `espresso`; b 0.9 and
  up wins `mulčování` but loses `kvásek`, so it stays at 0.75; typo weight
  0.8 drops `passport` one more place. The rest does not move the metrics
  on this set.
- **Statistics over the candidates or the whole vault**: the only filtered
  query (`filter-dir-rozpocet`) ranks the same either way (scores 0.779 and
  0.472 over the candidates, 0.781 and 0.478 over the vault). Candidates
  it is: scores do not depend on notes the query excludes, and no note
  outside the filter has to be read.
- **Single-word queries**: the rarity cancels out, so the score is the
  saturated field sum alone. One body hit in a note of average length is
  0.455, a tag or file name hit 0.714, a title hit about 0.8.

### Latency and memory

`BenchmarkSearch5k` and `BenchmarkSearch5kLegacy` on the same machine and
load (Apple M4 Pro), median of five runs of 300 iterations, warm:

| Case | p50 legacy | p50 BM25F | p95 legacy | p95 BM25F | p95 change |
|---|---|---|---|---|---|
| exact | 0.91 ms | 0.92 ms | 1.12 ms | 1.16 ms | +4 % |
| diacritics | 1.75 ms | 1.83 ms | 1.89 ms | 2.02 ms | +7 % |
| typo (worst case) | 0.93 ms | 0.93 ms | 1.14 ms | 1.15 ms | +1 % |
| no match | 0.82 ms | 0.83 ms | 0.94 ms | 0.96 ms | +2 % |

All within the +20 % budget. The diacritics case is the hardest for BM25F:
every one of the 5 000 notes matches, with both terms on all 40 lines, and
BM25F counts every occurrence where the legacy ranker stopped at 8. A note
that repeats its words a lot is therefore counted over its unique words
(each with its body count, kept in the word list the typo matcher already
has) instead of scanning the text; without that the case took 3.2 ms.

Memory: idle heap 207.1 MiB against 206.5 MiB before (+0.3 %), peak 209.9
MiB against 209.0 MiB for the diacritics case. The cache keeps per note the
heading texts, the body offset, five word counts and one flag; the per-word
body count fits in the existing word entry (rune length and count are
32-bit now), so the word list did not grow. A server on the golden corpus
holds the same RSS with either ranker. No new dependency; the BM25F code
adds about 9 KB to the binary.

## Match quality (NSO-578)

BM25F decides how much a match weighs; NSO-578 asks what counts as a match.
Four experiments, measured 2026-11-05 on the same corpus, golden set and
machine, starting from BM25F as NSO-577 left it. The rule: an experiment
that does not raise MRR or nDCG@10, or makes anything else worse, is not
kept. How the kept ones work: [search.md](search.md#what-a-query-matches).

| # | Experiment | Status | MRR | nDCG@10 | recall@5 | inflection recall@5 | no-answer queries returning nothing |
|---|---|---|---|---|---|---|---|
| | BM25F (NSO-577), the starting point | | 0.8333 | 0.8433 | 0.8452 | 0.556 | 5 of 5 |
| 1a | Prefix weight `0.6 · len / (len + 0.3 · excess)` for a term that only starts longer words (`pool` in `pooling`), instead of 1 | rejected | 0.8333 | 0.8433 | 0.8452 | 0.556 | 5 of 5 |
| 1b | Czech word forms: cut a case ending off a term that is nowhere in the note, match the rest as a word start, with the prefix weight | **accepted** | **0.8571** | **0.8671** | **0.8690** | **0.667** | 5 of 5 |
| 1a+1b | both | (1b) | 0.8571 | 0.8671 | 0.8690 | 0.667 | 5 of 5 |
| 2a | Typo weight `0.45 · len / (len + distance)` instead of a flat 0.5 | rejected | 0.8333 | 0.8433 | 0.8452 | 0.556 | 5 of 5 |
| 2b | Meilisearch typo budget: one edit from 5 letters, two from 9 (today 4 and 8) | rejected | 0.8095 | 0.8195 | 0.8214 | 0.444 | 5 of 5 |
| 3 | Proximity: up to +10 % of the score for query words close together in the body (smallest distance, same line above neighbouring lines) | rejected | 0.8333 | 0.8433 | 0.8452 | 0.556 | 5 of 5 |
| 4 | Relaxed AND: when nothing holds every term, return the notes that miss one | (refined below) | 0.9048 | 0.9106 | 0.9048 | 0.778 | **4 of 5** |
| 4 | Relaxed AND, a note counts only when the terms it holds carry more than half the query's IDF | **accepted** | 0.9048 | 0.9106 | 0.9048 | 0.778 | 5 of 5 |
| 1b+4 | the result | **default** | **0.9048** | **0.9106** | **0.9048** | **0.778** | 5 of 5 |

Each row except the last two is the experiment alone on top of NSO-577.
Combinations measured on the way (1b+2a+4, 1b+2a+3+4) give exactly the
1b+4 numbers: 2a and 3 change no rank in any combination either.

- **1a, prefix weight: rejected.** Not a single rank moves. Every term
  that starts a longer word in an answer also starts it in the notes
  around it, so the weight cancels out, and it costs one more pass over
  the word list for every matched term.
- **1b, Czech word forms: accepted.** The endings are the case endings of
  the "light" Czech stemmer of Dolamic and Savoy (the one Lucene ships),
  cut only when what remains keeps 4 letters, and tried only for a term
  that is nowhere in the note as a substring, so no exact result changes.
  `recept na kváskový chléb s dlouhým kynutím` is found (`dlouhým` gives
  `dlouh` for `dlouhé`, `kynutím` gives `kynut` for `kynutí`), and the
  inflection answers that the typo fallback used to find score higher:
  `faktury` 0.257 to 0.485, `účtenky` 0.283 to 0.323, `zahrady` 0.463 to
  0.536, `dovolená v Chorvatsku` 0.385 to 0.444, `nájemní smlouvy` 0.464
  to 0.530. Every exact answer found for an inflection query now scores
  above 0.3, the `min_score` advice. The reverse direction (query
  `fakturami`, text `faktura`) works too, as long as the cut form starts
  the word in the note. A Czech stemmer was not needed for that.
- **2a, typo weight by edit distance: rejected.** No rank moves: within
  one query, the typo matches compete with each other at the same
  distance. The flat 0.5 stays.
- **2b, Meilisearch typo budget: rejected.** Worse: `faktury` and
  `účtenky` (7 letters) lose their only match, and the stricter budget
  did not remove a false hit on this set.
- **3, proximity: rejected.** No rank moves (the multi-word answers are
  already first), and finding the smallest distance means walking every
  line of every match: the diacritics case on 5 000 notes went from 1.83
  to 7.04 ms (p50), far over the +20 % budget.
- **4, relaxed AND: accepted, with a rule.** `how do I rotate the TLS
  certificates on the homelab reverse proxy` (the note lacks `do`) and
  `očkování psa` (the note says `pes`, `psovi`) are now answered at rank
  1. Without a rule `kvantový počítač`, a no-answer query, returned two
  notes that say `počítač`: the dropped word was the one no note knows. The
  rule keeps a note only when the words it holds carry more than half the
  summed IDF, so the missing word must be the less telling one. That
  removes the no-answer results and the notes of `očkování psa` that hold
  only the short, common `psa`, and the metrics stay. The
  results carry `relaxed: true` (`Page.Relaxed`, MCP and REST). Only when
  the strict AND finds nothing, never for one-word queries, regex or the
  legacy ranker.

Still failing: `sentence-database-hosting` and `sentence-ucetni-system`
miss two or more words each (`what`, `did`, `moving`; `jaký`, `byl`,
`výběru`, `nového`): function words would need a stop word list, which is
not in this issue. `zálohování fotek` needs derivation, not case endings
(`zálohování` against `záloha`, `fotek` against `fotky`). `single-passport`
and `long-short-mulcovani` are unchanged from NSO-577.

Queries whose rank of the exact answer changed against NSO-577:

| Query | NSO-577 | NSO-578 | By |
|---|---|---|---|
| sentence-kvaskovy-chleb | - | **1** | 1b, word forms |
| sentence-tls-rotate | - | **1** | 4, relaxed |
| inflection-ockovani-psa | - | **1** | 4, relaxed |

Per kind, changed rows (MRR / nDCG@10 / recall@5):

| Kind | Queries | NSO-577 | NSO-578 |
|---|---|---|---|
| inflection | 9 | 0.556 / 0.556 / 0.556 | **0.778 / 0.778 / 0.778** |
| multi-word | 18 | 0.889 / 0.889 / 0.889 | **0.944 / 0.944 / 0.944** |
| sentence | 5 | 0.200 / 0.165 / 0.100 | **0.600 / 0.530 / 0.400** |

Scores after NSO-578:

| Score of | min / median / max |
|---|---|
| top hit, answerable queries with results (39) | 0.323 / 0.714 / 0.930 |
| top hit, no-answer queries (5; 0 = no results) | 0 / 0 / 0 |
| exact answers (40) | 0.323 / 0.716 / 0.930 |
| returned notes not judged relevant (6) | 0.295 / 0.472 / 0.593 |

`score` stays within 0 to 1: a match weight is at most 1 and a missing term
adds 0, so the BM25F bound holds. `TestBM25FScoreInRange` and
`TestRelaxedScoreInRange` check it on 600 random queries, the latter on the
relaxed ones. `TestMatchQualityGoldenSet` holds the inflection recall@5
above its NSO-577 value. The floors in `baseline.json` are raised to the
new values.

### Latency and memory

`BenchmarkSearch5k`, same machine, warm. Before: NSO-577, median of three
runs of 300 iterations; after: median of five. A fifth case, `relaxed`
(`jehla xqzwrtplk`: the strict scan finds nothing, so the relaxed scan
runs, and drops every note because the unknown word outweighs the rest),
measures the new path.

| Case | p50 before | p50 after | p95 before | p95 after | p95 change |
|---|---|---|---|---|---|
| exact | 0.92 ms | 0.98 ms | 1.17 ms | 1.23 ms | +5 % |
| diacritics | 1.84 ms | 1.79 ms | 2.03 ms | 1.98 ms | -2 % |
| typo (worst case) | 0.93 ms | 0.98 ms | 1.17 ms | 1.26 ms | +7 % |
| no match | 0.83 ms | 0.85 ms | 0.97 ms | 1.04 ms | +8 % |
| relaxed (new) | | 2.17 ms | | 2.59 ms | |

All within the +20 % budget. The typo case pays for the word form check
before the typo search: a scan of the note's word list, which the typo
search walks anyway. A first version also checked the path and tag words
of every note and cost about +14 %; they are now split into words only when the
cut term occurs in them. The relaxed path costs about two and a half
strict scans: it only runs when the strict AND returns nothing, for a
query of two or more words.

Memory: idle heap 207.1 MiB before and after. The cache holds nothing new;
the word forms work over the existing word list. Peak 207.7 MiB for the
relaxed case, about 210 MiB for diacritics as before. No new
dependency; the new code is about 4 KB of the binary.

## Latency and memory on 5 000 notes

`BenchmarkSearch5k`: the four existing benchmark cases on 5 000 generated
notes (40 lines each, Czech diacritics on every line, about 14 MB of text),
warm cache, Apple M4 Pro, median of three runs of 300 iterations. The
machine was under load from parallel builds, so take the p95 column as an
upper bound and re-measure the baseline on the same machine before
comparing a new ranker.

| Case | Query | p50 | p95 | ns/op | B/op | allocs/op |
|---|---|---|---|---|---|---|
| exact | `jehla` | 0.93 ms | 1.30 ms | 1.01 ms | 245 KB | 297 |
| diacritics | `ukoly prilohami` | 1.74 ms | 2.02 ms | 1.80 ms | 2.44 MB | 6 012 |
| typo (worst case) | `jehlla` | 1.20 ms | 2.14 ms | 1.37 ms | 249 KB | 445 |
| no match | `xqzwrtplk` | 0.92 ms | 1.47 ms | 1.00 ms | 197 KB | 45 |

Memory: **idle heap 206.7 MiB** (index plus warm content cache, after GC),
**peak 206.9 MiB** per query (209.1 MiB for the diacritics case; heap at
the end of a query with the GC off, an upper bound).

The idle number is large: the content cache keeps each note as raw lines,
folded lines, the joined folded text, a unique word list and, for every
line with a non-ASCII character, a byte offset map of 8 bytes per byte.
For Czech text that is about 15 times the size of the notes on disk. It is
the baseline the M8 memory budget (at most +10 %) is measured against, and
a candidate for a later optimisation (for example 32-bit offsets, or
offsets computed only for snippet lines).

That optimisation is done: the folded lines are now substrings of the
joined folded text, the offset maps are gone (a snippet maps its span back
to the raw line when it is built), the word list keeps no spare capacity,
and the index no longer holds each note's whole content through the
substrings in its entries. The same benchmark, same machine: **idle heap
43.1 MiB** (from 206.7), exact 0.90 ms, diacritics 1.69 ms, typo 0.91 ms,
no match 0.80 ms (p50). Search results, scores and snippets are byte for
byte the same as before. (Measured without other load, so part of the speed-up is the quieter machine.)

The 2 000-note benchmarks from [search.md](search.md) (same machine, same
load): exact 0.47 ms, diacritics 0.75 ms, typo 0.45 ms, no match 0.34 ms
per query.

## Experiments

Each ranking change adds a row with the metrics before and after.

| Change | Status | MRR | nDCG@10 | recall@5 | p95 5k (worst case) | Note |
|---|---|---|---|---|---|---|
| Baseline, hand-tuned points | replaced (`legacy`) | 0.7857 | 0.8206 | 0.8452 | 2.14 ms (typo) | this page |
| BM25F, NSO-577 weights | accepted, default | 0.8333 | 0.8433 | 0.8452 | 2.02 ms (diacritics; legacy 1.89 ms in the same run) | [BM25F](#bm25f-nso-577) |
| NSO-578 1a: prefix weight for terms that only start longer words | rejected | 0.8333 | 0.8433 | 0.8452 | not measured | no rank moves |
| NSO-578 1b: Czech word forms (case endings cut, prefix match) | accepted | 0.8571 | 0.8671 | 0.8690 | 1.26 ms (typo, with 4) | [Match quality](#match-quality-nso-578) |
| NSO-578 2: typo weight by edit distance; Meilisearch typo budget | rejected | 0.8333; 0.8095 | 0.8433; 0.8195 | 0.8452; 0.8214 | not measured | no rank moves; loses inflection answers |
| NSO-578 3: proximity bonus up to 10 % | rejected | 0.8333 | 0.8433 | 0.8452 | 7.04 ms p50 (diacritics) | no rank moves, 3.8 times slower |
| NSO-578 4: relaxed AND (one term missing, majority of IDF held) | accepted, default with 1b | 0.9048 | 0.9106 | 0.9048 | 1.26 ms (typo); relaxed path 2.59 ms | [Match quality](#match-quality-nso-578) |
