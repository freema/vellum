# MCP tool payload baseline

How many bytes vellum's MCP tools send back for typical agent tasks, and
which part of a tool result actually reaches the model. This is the
"before" number for every change that aims to make reads cheaper: each such
change attaches the table below measured before and after.

## Running it

```sh
go test -count=1 -run TestToolPayload -v ./internal/mcpserver
# or
task payload
```

The test lives in `internal/mcpserver/payload_test.go`. It takes well under a
second and also runs (silently) as part of `go test ./...`, so a scenario
that stops working fails CI. Without `-v` it prints nothing; with `-v` it
prints two Markdown tables you can paste into a pull request.

What it does:

- **Fixture.** A vault generated into a temp directory on every run: 300
  short notes plus 4 long ones (about 620 lines and 50 headings each),
  304 notes and about 600 KB in total. Nested folders up to four levels
  deep, Czech diacritics in paths and text, frontmatter and inline tags,
  wikilinks between notes of the same folder, a few task notes. The
  generator uses its own seeded PRNG and fixed file times, so the vault is
  byte-for-byte identical on every run and every machine. The small
  `testdata/vault` fixture is not touched.
- **Transport.** The real MCP server (`mcpserver.New`, curator tools on)
  and an MCP client from the same go-sdk, connected through
  `mcp.NewInMemoryTransports`. No network, no HTTP.
- **Measurement.** The client transport is wrapped and records the raw
  JSON-RPC `result` of each `tools/call` response. Per call the test counts
  separately the bytes of `structuredContent` (raw JSON as sent) and of the
  text blocks (decoded UTF-8 text). It also counts the bytes of the call
  arguments, which is what the model has to write. Tokens are not counted:
  bytes are deterministic. The `~Tokens` column is bytes / 4, for rough
  orientation only (Czech text with diacritics tokenizes worse than that).
- **Determinism.** The test replays all scenarios twice on two freshly
  generated vaults and fails unless the numbers match. Time stamps are
  serialized in UTC inside the test, so the time zone of the machine does
  not change the byte counts.

Columns: `Calls` tool calls, `Args B` argument bytes, `structuredContent B`
and `Text B` the two copies of the result, `Result B` their sum (everything
the server sends back), `~Tokens` = `Result B` / 4.

## Scenarios

Each scenario is what an agent has to do today, with the tools that exist
today:

| Scenario | Task | Calls today |
| --- | --- | --- |
| find-and-read | Find a note and read it | `search_notes` with a two-word query (`dlažba rozpočet`), then `read_note` on the top hit |
| long-note-section | Get one paragraph out of a 620-line note | `read_note` with `outline: true`, then `read_note` with `section` (before NSO-569: `read_note` of the whole note) |
| orient | Learn the vault structure | `list_notes` of the root, then of its largest folder, reading the folder counts in `dirs` (before NSO-572: `list_notes` with `recursive: true`) |
| read-five-related | Read five notes of one project | one `read_notes` call with the five paths (before NSO-571: five `read_note` calls) |
| where-does-it-go | Find a folder for a new note | `suggest_location` |
| small-fix | Change one sentence in the long note | `read_note` with `section`, then `replace_in_note` of the sentence with the returned hash (before NSO-574: `patch_note` of the whole section; before NSO-569: `read_note` of the whole note first) |

## Baseline: v1.12.1

Measured on 2026-11-05 with go-sdk v1.6.1. The numbers are identical for a
source export of tag `v1.12.1` and for the `main` branch at the start of M7
(v1.13.0 beta): nothing in the measured tools changed in between.

Per scenario:

| Scenario | Calls | Args B | structuredContent B | Text B | Result B | ~Tokens (Result/4) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| find-and-read | 2 | 95 | 44974 | 44974 | 89948 | 22487 |
| long-note-section | 1 | 69 | 64867 | 64867 | 129734 | 32433 |
| orient | 1 | 18 | 39908 | 39908 | 79816 | 19954 |
| read-five-related | 5 | 281 | 18586 | 18586 | 37172 | 9293 |
| where-does-it-go | 1 | 130 | 539 | 539 | 1078 | 269 |
| small-fix | 2 | 1059 | 65010 | 65010 | 130020 | 32505 |
| **total** | 12 | 1652 | 233884 | 233884 | 467768 | 116942 |

Per tool, over all scenarios:

| Tool | Calls | Args B | structuredContent B | Text B | Result B | ~Tokens (Result/4) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| list_notes | 1 | 18 | 39908 | 39908 | 79816 | 19954 |
| patch_note | 1 | 990 | 143 | 143 | 286 | 71 |
| read_note | 8 | 485 | 151866 | 151866 | 303732 | 75933 |
| search_notes | 1 | 29 | 41428 | 41428 | 82856 | 20714 |
| suggest_location | 1 | 130 | 539 | 539 | 1078 | 269 |
| **total** | 12 | 1652 | 233884 | 233884 | 467768 | 116942 |

What stands out:

- **Every result is sent twice.** vellum's tools return only structured
  output, and go-sdk then mirrors the same JSON into a text block (in
  go-sdk v1.6.1 `mcp/server.go`, the `if res.Content == nil` branch), as
  the MCP specification recommends for older clients. `Text B` therefore
  always equals `structuredContent B`.
- **`read_note` carries the note twice inside `structuredContent` too.**
  The result has both `content` (the raw file) and `body` (the same text
  without frontmatter). The long note is 31,638 bytes on disk; its
  `structuredContent` is 64,867 bytes, and with the text mirror the
  server sends 129,734 bytes, about four times the file, to deliver one
  paragraph of about 250 bytes.
- **`read_note` is 65 % of all bytes**, followed by `search_notes` (default
  50 results with up to three snippets each) and `list_notes` (every note
  with size and time stamp).
- **Small edits are cheap to write but expensive to prepare.** The
  `patch_note` call itself sends about 1 KB of arguments and gets 143 bytes
  back; the `read_note` needed to find the section and its hash costs 65 KB.

## What reaches the model

A tool result holds the same data twice, as `structuredContent` and as a
text block. Which one a client hands to the model cannot be seen from the
server side, so it was tested with a probe server whose two channels carry
different codewords (source below).

| Client | Forwards to the model | How it was checked |
| --- | --- | --- |
| Claude Code 2.1.318 (stdio server) | `structuredContent` only, as its JSON text. The text block is dropped. | Probe over stdio with `claude -p --mcp-config`. The model saw only `STRUCTURED-OMEGA`, and the `tool_result` in the `--output-format stream-json` transcript is exactly `{"codeword":"STRUCTURED-OMEGA"}`. |
| Claude Code with the claude.ai vellum connector | One copy (the result appears once). The two copies are identical, so this alone does not show which one. | A `search_notes` call through the connector. |
| claude.ai (web, custom connector) | Not verified yet. | Needs the probe exposed over HTTPS, see below. |
| Cursor | Not verified yet. | Probe as a stdio server in Cursor's MCP settings, see below. |

Consequences, as far as verified:

- In Claude Code the mirrored text block costs bandwidth but no tokens.
  The tokens come from `structuredContent`, so for Claude Code a cheaper
  read has to make `structuredContent` smaller (for example the
  `content`/`body` duplication in `read_note`). Dropping only the text
  mirror would halve `Result B` in the table without saving a single token
  there.
- Claude Code truncates a tool result above 25,000 tokens (per Anthropic's
  article "Writing effective tools for agents"). The long note's
  `structuredContent` alone is about 16,000 tokens by the bytes / 4 rule,
  and Czech text tokenizes worse, so a note two or three times longer can
  already be cut off.
- When comparing before and after for a pull request, look at
  `structuredContent B` as well as `Result B`, until the claude.ai and
  Cursor rows above are filled in.

### Probe server

A throwaway Go program (not part of vellum) that returns `ALPHA` in the
text block and `STRUCTURED-OMEGA` in `structuredContent`:

```go
package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type out struct {
	Codeword string `json:"codeword"`
}

func main() {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "probe", Description: "Returns a codeword."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, out, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "TEXT-BLOCK codeword: ALPHA"}}},
				out{Codeword: "STRUCTURED-OMEGA"}, nil
		})
	if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
```

Build it in an empty module (`go mod init probe && go get
github.com/modelcontextprotocol/go-sdk@v1.6.1 && go build -o probe .`),
register it as an MCP server in the client, call the `probe` tool and ask
the model to quote the result verbatim. For Claude Code:

```sh
echo '{"mcpServers":{"probe":{"command":"/path/to/probe"}}}' > mcp.json
claude -p "Call the probe tool once and quote the raw result verbatim." \
  --mcp-config mcp.json --strict-mcp-config --allowedTools mcp__probe__probe
```

For Cursor, add the same `command` to its MCP settings. For claude.ai, the
probe has to be served over Streamable HTTP
(`http.Handle("/mcp", mcp.NewStreamableHTTPHandler(...))` instead of
`s.Run`) on a public HTTPS URL and added as a custom connector.

## Decision: what goes into the text block

Decided for NSO-570 on 2026-11-05, after the measurement above.

Three options were on the table for the text block of a tool result:

- **a) Keep the JSON mirror** that go-sdk writes when a handler leaves
  `Content` empty. Most compatible, and what the MCP specification
  suggests for clients without structured output.
- **b) A plain rendering of the data**: a short header and the
  markdown itself, with no JSON envelope and no escaping.
- **c) Only a header in the text**, the content only in
  `structuredContent`. Cheapest, but a client that reads only the text
  would not see the content.

vellum uses **b) for the tools whose results are mostly note text**:
`read_note`, `search_notes`, `suggest_tags` and `suggest_links`. Every
other tool keeps a), its results are small and mostly keys.

For `search_notes` the plain rendering is a summary rather than a copy.
The first version printed every snippet window grep-style (about 30 KB
on the find-and-read query, three windows of five lines for each of 39
notes), and the scenario only dropped 16 %. The list of hits with their
best line is about 7 KB. A text-only client still learns which notes
matched, why, and at which line to open them; the full snippets stay in
`structuredContent`, unchanged. Printing only the match lines of all
snippets (about 13 KB) was measured too and still missed the 40 % goal.

Why b) and not c): the only client verified so far (Claude Code) reads
`structuredContent` and drops the text block, so for it the text block
costs bandwidth but no tokens. For claude.ai and Cursor it is still
unknown which copy reaches the model (see the table above). b) is safe
either way: a client that reads only the text still gets everything it
needs to act, a client that reads `structuredContent` gets exactly what
it got before, minus the `read_note` body. c) would leave a text-only
client without the note content, so it waits until claude.ai and Cursor
are verified.

What the text blocks look like:

- `read_note`: `path:`, `hash:`, `tags:` (when the note has any) and
  `lines:` header lines, a blank line, then the file exactly as on disk,
  frontmatter included. The hash is the full hash, so a text-only client
  can still make conflict-safe edits.
- `search_notes`: a count line, then per note (in ranked order) a
  `path (title)` line and its best matching line as `7: text`. The
  number is the file line number (frontmatter counted), the same as
  `line` in the structured snippets. A very long line is cut to 200
  characters around the match, marked with `…`. The context windows and
  the further snippets of each note are only in `structuredContent`.
  This is the same shape as the concise mode planned in NSO-572.
- `suggest_tags`: the current, neighbor and vault tags as comma-separated
  lines (vault tags with their note counts), then the excerpt.
- `suggest_links`: one `path (title): reason` line per candidate.

Independently of the text block, `read_note` no longer returns `body`
in `structuredContent`. `body` was `content` without the frontmatter, so
every read carried the note twice in the structured result alone. The
REST API (`GET /api/notes/{path}`) still returns `body`; the web UI uses
it.

Clients this was checked on:

| Client | Result |
| --- | --- |
| go-sdk v1.6.1 client (in-memory transport, the tests in `internal/mcpserver/text_test.go`) | `structuredContent` and the new text blocks as described above |
| Claude Code 2.1.318 | Reads only `structuredContent` (probe above), so it sees the smaller `read_note` result and never the text block |
| claude.ai, Cursor | Not verified yet. Both get the full information in either copy |

Once claude.ai and Cursor are verified with the probe: if both read
`structuredContent`, c) becomes safe for these tools and halves the
bytes again. If one of them reads only the text, b) stays.

### After NSO-570

Measured on 2026-11-05 with go-sdk v1.6.1, same fixture and scenarios:

| Scenario | Calls | Args B | structuredContent B | Text B | Result B | ~Tokens (Result/4) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| find-and-read | 2 | 95 | 43422 | 8601 | 52023 | 13005 |
| long-note-section | 1 | 69 | 32678 | 31810 | 64488 | 16122 |
| orient | 1 | 18 | 39908 | 39908 | 79816 | 19954 |
| read-five-related | 5 | 281 | 10373 | 9115 | 19488 | 4872 |
| where-does-it-go | 1 | 130 | 539 | 539 | 1078 | 269 |
| small-fix | 2 | 1059 | 32821 | 31953 | 64774 | 16193 |
| **total** | 12 | 1652 | 159741 | 121926 | 281667 | 70416 |

| Tool | Calls | Args B | structuredContent B | Text B | Result B | ~Tokens (Result/4) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| list_notes | 1 | 18 | 39908 | 39908 | 79816 | 19954 |
| patch_note | 1 | 990 | 143 | 143 | 286 | 71 |
| read_note | 8 | 485 | 77723 | 74485 | 152208 | 38052 |
| search_notes | 1 | 29 | 41428 | 6851 | 48279 | 12069 |
| suggest_location | 1 | 130 | 539 | 539 | 1078 | 269 |
| **total** | 12 | 1652 | 159741 | 121926 | 281667 | 70416 |

Against the v1.12.1 baseline:

| | Before | After | Change |
| --- | ---: | ---: | ---: |
| All scenarios, Result B | 467768 | 281667 | -40 % |
| All scenarios, structuredContent B | 233884 | 159741 | -32 % |
| `read_note` (8 calls), Result B | 303732 | 152208 | -50 % |
| `read_note` (8 calls), structuredContent B | 151866 | 77723 | -49 % |
| `search_notes`, Result B | 82856 | 48279 | -42 % |
| `search_notes`, Text B | 41428 | 6851 | -83 % |
| find-and-read, Result B | 89948 | 52023 | -42 % |

Reading a note now costs about one copy of the file per channel instead
of two. In the find-and-read scenario most of the cost is the search,
not the read: the top hit is a short note (about 1.5 KB), while
`search_notes` returns 39 notes with three five-line snippets each. Its
text block is now the short list described above; its
`structuredContent` did not change. `TestFindAndReadPayloadBudget` in
`internal/mcpserver/text_test.go` fails if the scenario climbs back
above 60 % of the baseline. For a client that reads only
`structuredContent` (Claude Code) the search part shrinks with the
search limits of NSO-572.

### After NSO-569

`read_note` can return part of a note: `outline: true` (headings with
their line numbers, plus frontmatter, tags, hash, size and line count, no
content), `section` (one heading and everything under it, the lines
`patch_note` would replace), or a line range with `start_line` and
`max_lines`. Lines count from 1 over the whole file, frontmatter included,
the numbering `search_notes` uses. `hash` is always the whole note's, so
an edit after a partial read still passes `expected_hash`. A call without
the new parameters returns the same bytes as before.

The two long-note scenarios now use them: long-note-section asks for the
outline and then the section, small-fix reads the section and patches it
with the hash from that read. The other scenarios are unchanged and so
are their numbers, byte for byte.

Measured on 2026-11-05 with go-sdk v1.6.1, same fixture:

| Scenario | Calls | Args B | structuredContent B | Text B | Result B | ~Tokens (Result/4) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| find-and-read | 2 | 95 | 43422 | 8601 | 52023 | 13005 |
| long-note-section | 2 | 199 | 3242 | 2826 | 6068 | 1517 |
| orient | 1 | 18 | 39908 | 39908 | 79816 | 19954 |
| read-five-related | 5 | 281 | 10373 | 9115 | 19488 | 4872 |
| where-does-it-go | 1 | 130 | 539 | 539 | 1078 | 269 |
| small-fix | 2 | 1107 | 1430 | 1175 | 2605 | 651 |
| **total** | 13 | 1830 | 98914 | 62164 | 161078 | 40269 |

| Tool | Calls | Args B | structuredContent B | Text B | Result B | ~Tokens (Result/4) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| list_notes | 1 | 18 | 39908 | 39908 | 79816 | 19954 |
| patch_note | 1 | 992 | 143 | 143 | 286 | 71 |
| read_note | 9 | 661 | 16896 | 14723 | 31619 | 7904 |
| search_notes | 1 | 29 | 41428 | 6851 | 48279 | 12069 |
| suggest_location | 1 | 130 | 539 | 539 | 1078 | 269 |
| **total** | 13 | 1830 | 98914 | 62164 | 161078 | 40269 |

Against the state before (after NSO-570) and v1.12.1:

| | v1.12.1 | Before | After | Change vs before |
| --- | ---: | ---: | ---: | ---: |
| long-note-section, Result B | 129734 | 64488 | 6068 | -91 % |
| long-note-section, structuredContent B | 64867 | 32678 | 3242 | -90 % |
| small-fix, Result B | 130020 | 64774 | 2605 | -96 % |
| small-fix, structuredContent B | 65010 | 32821 | 1430 | -96 % |
| All scenarios, Result B | 467768 | 281667 | 161078 | -43 % |
| All scenarios, structuredContent B | 233884 | 159741 | 98914 | -38 % |

The outline of the 620-line note (51 headings) is 1,955 bytes of
`structuredContent` and 1,794 bytes of text. Its headings are strings in
the form `409: ### 7.3 Harmonogram pokládky dlažby` (file line, level as
`#` marks, text) rather than objects: objects with `level`, `text` and
`line` keys put the same outline at about 3 KB. The outline also leaves
out `content`, `links`, `title` and `modTime`.

`TestLongNoteSectionPayloadBudget` in
`internal/mcpserver/read_test.go` fails if the long-note-section
scenario climbs above 20 % of its size before NSO-569, in total or in
`structuredContent` alone. `TestReadNoteOutlineSize` keeps the outline
under 2,000 bytes per channel.

### After NSO-571

`read_notes` reads several notes in one call: a list of `paths` (at most
20) or a glob `pattern` such as `projects/x/*.md` (`*` stays within one
folder, matches are read in alphabetical order, at most 20). The answer
is per note: content, hash, tags and title, or an error of its own, or
`skipped` with the note's size, line count and a pointer to `read_note`'s
outline, section and line range. A note over `max_bytes_per_note`
(default 10,240 B) is skipped, never cut. Once `max_total_bytes` (default
61,440 B) is spent, every remaining note is skipped the same way. A
summary closes the answer. `outline_only` returns each note's
`read_note` outline instead of its content.

An entry carries less than a `read_note` result: no `frontmatter` (it is
in `content`), no `links`, `size` or `modTime`. The text block has one
`==> path <==` line per note, the `hash:` and `tags:` lines, then the file.

The read-five-related scenario now makes one `read_notes` call. The other
scenarios are unchanged and so are their numbers, byte for byte.

Measured on 2026-11-05 with go-sdk v1.6.1, same fixture:

| Scenario | Calls | Args B | structuredContent B | Text B | Result B | ~Tokens (Result/4) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| find-and-read | 2 | 95 | 43422 | 8601 | 52023 | 13005 |
| long-note-section | 2 | 199 | 3242 | 2826 | 6068 | 1517 |
| orient | 1 | 18 | 39908 | 39908 | 79816 | 19954 |
| read-five-related | 1 | 252 | 9609 | 9096 | 18705 | 4676 |
| where-does-it-go | 1 | 130 | 539 | 539 | 1078 | 269 |
| small-fix | 2 | 1107 | 1430 | 1175 | 2605 | 651 |
| **total** | 9 | 1801 | 98150 | 62145 | 160295 | 40073 |

| Tool | Calls | Args B | structuredContent B | Text B | Result B | ~Tokens (Result/4) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| list_notes | 1 | 18 | 39908 | 39908 | 79816 | 19954 |
| patch_note | 1 | 992 | 143 | 143 | 286 | 71 |
| read_note | 4 | 380 | 6523 | 5608 | 12131 | 3032 |
| read_notes | 1 | 252 | 9609 | 9096 | 18705 | 4676 |
| search_notes | 1 | 29 | 41428 | 6851 | 48279 | 12069 |
| suggest_location | 1 | 130 | 539 | 539 | 1078 | 269 |
| **total** | 9 | 1801 | 98150 | 62145 | 160295 | 40073 |

Against the state before (after NSO-569) and v1.12.1:

| | v1.12.1 | Before | After | Change vs before |
| --- | ---: | ---: | ---: | ---: |
| read-five-related, calls | 5 | 5 | 1 | -4 calls |
| read-five-related, Args B | 281 | 281 | 252 | -10 % |
| read-five-related, structuredContent B | 18586 | 10373 | 9609 | -7 % |
| read-five-related, Text B | 18586 | 9115 | 9096 | -0.2 % |
| read-five-related, Result B | 37172 | 19488 | 18705 | -4 % |
| All scenarios, calls | 12 | 13 | 9 | -4 calls |
| All scenarios, Result B | 467768 | 161078 | 160295 | -0.5 % |

The gain is mostly the calls: one round trip instead of five, and one
tool result in the agent's context instead of five. The bytes drop a
little as well, because an entry leaves out what `content` already
holds. `TestReadFiveRelatedPayloadBudget` in
`internal/mcpserver/readnotes_test.go` fails if one `read_notes` call on
the five notes sends more bytes than the five `read_note` calls, in
`structuredContent` or in the text block.

### After NSO-572

`search_notes` returns 20 notes per call instead of 50, and both
`search_notes` and `list_notes` report `total`, `truncated` and a `hint`
when they cut. `list_notes` caps a listing at 200 notes and always returns
`dirs`, the direct subfolders with their note counts. `search_notes` gained
`offset`, `context_lines`, `max_snippets` and `response_format: concise`
(per note only path, title and the line number of the first match).

find-and-read makes the same calls as before; the search now returns the
first 20 of its 39 hits. orient now lists the root, reads the folder
counts and lists the largest folder (two calls, about 400 bytes), instead
of one recursive listing of every note. The other scenarios are unchanged
and so are their numbers, byte for byte.

Measured on 2026-11-05 with go-sdk v1.6.1, same fixture:

| Scenario | Calls | Args B | structuredContent B | Text B | Result B | ~Tokens (Result/4) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| find-and-read | 2 | 95 | 23591 | 5257 | 28848 | 7212 |
| long-note-section | 2 | 199 | 3242 | 2826 | 6068 | 1517 |
| orient | 2 | 20 | 388 | 388 | 776 | 194 |
| read-five-related | 1 | 252 | 9609 | 9096 | 18705 | 4676 |
| where-does-it-go | 1 | 130 | 539 | 539 | 1078 | 269 |
| small-fix | 2 | 1107 | 1430 | 1175 | 2605 | 651 |
| **total** | 10 | 1803 | 38799 | 19281 | 58080 | 14520 |

| Tool | Calls | Args B | structuredContent B | Text B | Result B | ~Tokens (Result/4) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| list_notes | 2 | 20 | 388 | 388 | 776 | 194 |
| patch_note | 1 | 992 | 143 | 143 | 286 | 71 |
| read_note | 4 | 380 | 6523 | 5608 | 12131 | 3032 |
| read_notes | 1 | 252 | 9609 | 9096 | 18705 | 4676 |
| search_notes | 1 | 29 | 21597 | 3507 | 25104 | 6276 |
| suggest_location | 1 | 130 | 539 | 539 | 1078 | 269 |
| **total** | 10 | 1803 | 38799 | 19281 | 58080 | 14520 |

Against the state before (after NSO-571) and v1.12.1:

| | v1.12.1 | Before | After | Change vs before |
| --- | ---: | ---: | ---: | ---: |
| find-and-read, Result B | 89948 | 52023 | 28848 | -45 % |
| find-and-read, structuredContent B | 44974 | 43422 | 23591 | -46 % |
| `search_notes`, structuredContent B | 41428 | 41428 | 21597 | -48 % |
| `search_notes`, Text B | 41428 | 6851 | 3507 | -49 % |
| orient, Result B | 79816 | 79816 | 776 | -99 % |
| orient, same call as before (recursive `list_notes`), Result B | 79816 | 79816 | 51638 | -35 % |
| All scenarios, Result B | 467768 | 160295 | 58080 | -64 % |
| All scenarios, structuredContent B | 233884 | 98150 | 38799 | -60 % |

The recursive `list_notes` row is the old orient call replayed: it now
returns the first 200 of the 304 notes, the folder counts and the hint
`104 more notes: ...`. With all scenarios together the M7 goal (half of
v1.12.1) is passed: 58,080 B against 467,768 B, 12 %.

The concise format, on the find-and-read query (`dlažba rozpočet`, 20 of 39
hits):

| Format | structuredContent B | Text B | Result B |
| --- | ---: | ---: | ---: |
| detailed (default) | 21597 | 3507 | 25104 |
| concise | 2339 | 1855 | 4194 |

A concise hit is `{"line":7,"path":"...","title":"..."}` in
`structuredContent` and a `path:7 (title)` line in the text block, about
110 and 85 bytes per hit. On this fixture the paths and titles alone are
1,567 bytes of each channel (paths of 60 bytes and more, Czech
diacritics). A first
version also carried the matching line, cut to 40 characters around the
match: 3514 B and 2859 B, over the 3 kB of the NSO-572 acceptance on this
fixture. A line that short mostly repeats the query, so it was dropped;
the line number leads `read_note`'s `start_line` to the rest. On a vault
with everyday paths (`projects/garden/planting-log-07.md`) the same page
is 1730 B of `structuredContent` and 1246 B of text.

The 3 kB is met per channel, which is what a client hands to the model
(Claude Code forwards `structuredContent` alone, see above). Both
channels together are 4194 B on this fixture and cannot get under 3 kB
while both name the notes: the paths and titles alone are 3,134 B over
the two channels. The text block has to name them, so that a text-only
client can still open the hits (the NSO-570 decision). On everyday paths both
together are 2976 B.

Guards: `TestOrientPayloadBudget` (orient under 5 % of v1.12.1),
`TestSearchNotesConciseUnder3KB` (20 concise hits on this fixture under
3,000 B per channel, and both channels under a quarter of the detailed
page), `TestSearchNotesConciseEverydayPaths` (both channels together
under 3,000 B with short paths) and `TestListNotesLargeVaultIsCapped`
(a recursive listing of 5,000 notes returns 200 entries, about 23 KB,
and a hint) in `internal/mcpserver/listing_test.go`.

### After NSO-573

Folders can carry a description in a `_index.md` note (see
[mcp.md](mcp.md#folder-descriptions)). `search_notes` returns it once
per response in `contexts`, `list_notes` on each entry of `dirs`,
`suggest_location` on each candidate and the `read_note` outline as the
chain of folders above the note.

The fixture has no description notes, so every scenario above returns
the same bytes as after NSO-572: a vault without descriptions answers
exactly as before. To see what descriptions cost,
`TestFolderContextPayload` in `internal/mcpserver/contexts_test.go`
replays the scenarios on the same fixture plus six description notes
(the vault root, `projects`, three projects and `areas/finance`, 37 to
81 characters each).

Measured on 2026-11-05 with go-sdk v1.6.1:

| Scenario | Result B before | Result B after | structuredContent B before | structuredContent B after |
| --- | ---: | ---: | ---: | ---: |
| find-and-read | 28848 | 29229 | 23591 | 23784 |
| long-note-section | 6068 | 6449 | 3242 | 3435 |
| orient | 776 | 1774 | 388 | 887 |
| read-five-related | 18705 | 18705 | 9609 | 9609 |
| where-does-it-go | 1078 | 1256 | 539 | 628 |
| small-fix | 2605 | 2605 | 1430 | 1430 |
| **total** | 58080 | 60018 | 38799 | 39773 |

Each described folder appears at most once per response, however many
hits come from it: find-and-read returns 20 hits and carries two
descriptions (`projects` and the bathroom project), and the
long-note-section outline the same two. where-does-it-go grows by exactly one description and its key
(89 B per channel, `projects/zahrada`). orient grows the most because
listing the root and `projects` shows four described folders. The
vault root's description is never in a result: it is in the server
instructions, once per session.

`TestFolderContextPayload` fails if where-does-it-go grows by more than
the descriptions it returns, or if any scenario grows by more than every
description once.

### After NSO-574

`replace_in_note` replaces an exact piece of text: `old_text` must match
character for character and occur once (or `replace_all`). It answers with
the new hash, the number of replacements and the edited lines with two
lines around them, numbered, so the agent checks the edit without reading
the note again.

small-fix now reads the section as before and then sends only the
sentence and its fix, instead of the whole section through `patch_note`.
The other scenarios do not edit and are not affected by this change.

Measured on 2026-11-05 with go-sdk v1.6.1, same fixture. The patch_note
row replays the flow from before (`TestSmallFixPayloadBudget` runs both):

| Scenario | Calls | Args B | structuredContent B | Text B | Result B | ~Tokens (Result/4) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| small-fix (patch_note) | 2 | 1107 | 1430 | 1175 | 2605 | 651 |
| small-fix (replace_in_note) | 2 | 471 | 1761 | 1481 | 3242 | 810 |

| Tool | Calls | Args B | structuredContent B | Text B | Result B | ~Tokens (Result/4) |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| read_note (section) | 1 | 115 | 1287 | 1032 | 2319 | 579 |
| patch_note | 1 | 992 | 143 | 143 | 286 | 71 |
| replace_in_note | 1 | 356 | 474 | 449 | 923 | 230 |

Against v1.12.1 and the state before (after NSO-569):

| | v1.12.1 | Before | After | Change vs v1.12.1 | Change vs before |
| --- | ---: | ---: | ---: | ---: | ---: |
| small-fix, Args B | 1059 | 1107 | 471 | -56 % | -57 % |
| small-fix, Result B | 130020 | 2605 | 3242 | -98 % | +24 % |
| small-fix, Args B + Result B | 131079 | 3712 | 3713 | -97 % | 0 % |
| edit call alone, Args B | 990 | 992 | 356 | -64 % | -64 % |

The gain of this step is in what the model writes: the edit call's
arguments drop from the whole section (992 B) to the sentence, its fix,
the path and the hash (356 B). Arguments are output tokens, the slow
and expensive kind, and an unchanged text the model does not have to
copy is text it cannot change by accident. The answer grows by the
excerpt (five numbered lines, 474 B of `structuredContent`), which stands
in for the read an agent would otherwise make to check a `patch_note`;
in total the two flows send the same bytes. Against v1.12.1 the scenario
is down 97 %, most of it from the section read of NSO-569.

An unchanged excerpt line is cut to 200 characters, a long replacement
keeps its first and last lines, and `replace_all` shows at most three
places, so the excerpt stays small on any note.

`TestSmallFixPayloadBudget` in `internal/mcpserver/replace_test.go` fails
if small-fix climbs above 30 % of v1.12.1 (arguments and result
together), if the `replace_in_note` arguments exceed 40 % of the
`patch_note` ones, or if its result exceeds 600 B per channel.

## Updating this page

Re-run the command above after a change to the tools and paste the new
tables into the pull request next to these. Replace the baseline here only
when a release is meant to become the new reference, and keep the date and
version line in step.
