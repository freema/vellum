# Public share links

A share turns one note into a URL anybody can read without signing in.
It exists so the thing your agent just wrote can be handed to somebody —
a document, a spec, a set of notes — without exporting it, mailing a
file around, or giving anyone access to the vault.

Off by default. This is the only part of vellum that answers an
unauthenticated request, so nothing about it exists until you ask for it:

```sh
VELLUM_SHARING=on     # links + the share_note/unshare_note MCP tools
VELLUM_SHARING=ui     # links, but only the workspace can create them
VELLUM_SHARING=off    # default — no public route, no tools, no state file
```

`ui` is the middle setting worth knowing about: agents keep working with
shared notes (a moved note takes its link along), but the decision to
publish stays with a human. Anything unrecognized reads as `off` — a typo
must not be the reason a vault starts answering anonymous requests.

## What a link is

A **capability**, not a copy. The token in the URL *is* the
authorization; the share stores nothing but a pointer to a vault path.

- The reader always sees the **current** note. Edit it and the link
  updates; there is no snapshot to re-publish.
- Revoking is immediate — the URL stops working on the next request.
- Re-sharing a note **returns the link it already has**, so a URL you
  handed out yesterday keeps working when you touch Share again.
- Only the note's **body** is served. Frontmatter never leaves the vault,
  so private keys in a shared note's header stay private.

```
https://vellum.example.com/s/Xq3K5Idex6Tpm4AD2LwxcX3AnPRbY2CX
                             └─ 192 bits of URL-safe randomness
```

## Where links live

`<vault>/.vellum/shares.json` — one small JSON file next to the notes it
describes, written atomically, mode 600.

That location is deliberate on three counts:

1. **It survives a restart.** vellum's OAuth tokens are in memory and die
   with the process by design; a URL you gave to somebody else must not.
2. **It travels with the vault**, which is backed up by copying a
   directory. No database, nothing else to restore.
3. **It is unreachable from a note path.** The vault refuses writes into
   dot-directories and the index skips them, so an agent holding an MCP
   token cannot publish a note by writing to this file, and there is no
   `share:` frontmatter key it could set instead. Sharing only ever
   happens through the explicit endpoints below.

With sharing off, the directory is never created.

## Lifetimes

A link lives until revoked, or for a lifetime between **1 hour and 365
days**. The workspace offers 7 days, 30 days and no expiry; the MCP tool
takes a human duration (`24h`, `7d`, `4w`). Expired shares are dropped
from the file on the next write, and are invisible to a lookup the
instant they expire.

## Following the vault

A link points at a note, not at a path, so:

| What happens to the note | What happens to the link |
| --- | --- |
| Edited (web, MCP, or your editor) | Keeps working, shows the new content |
| Renamed or moved | **Follows it** — same promise `move_note` makes for backlinks |
| Moved onto another shared note | The overwritten note's link dies with it |
| Deleted | Revoked |
| Its folder deleted | Revoked, along with every other link inside |
| Deleted on disk behind vellum's back | Answers **410 Gone** until you revoke it |

## The pages a reader sees

| Route | Serves |
| --- | --- |
| `GET /s/{token}` | The reader: one note, rendered, with a theme toggle and a download button |
| `GET /s/{token}/note` | The same note as JSON (what the page fetches) |
| `GET /s/{token}/raw` | The markdown itself, as a `.md` download |

A plain `go build` has no embedded UI; there `GET /s/{token}` serves the
markdown directly, so the link still works.

The reader page is one document and nothing else — no tree, no search, no
editor, no other notes. `[[wikilinks]]` render but do not navigate: they
point into a vault the reader cannot see.

Unknown, malformed, expired and revoked tokens all answer the same
**404**; telling them apart is not the visitor's business. A deleted note
behind a valid link answers **410**.

Every response under `/s/` carries `X-Robots-Tag: noindex, nofollow,
noarchive`, `Referrer-Policy: no-referrer` (so the token cannot leak
through the Referer of a link in the note), `Cache-Control: no-store`,
`X-Content-Type-Options: nosniff` and `X-Frame-Options: DENY`. The route
is rate-limited to 120 requests per minute per client IP — set
`TRUST_PROXY=1` behind a reverse proxy so that counts real addresses.

## The workspace

- **Share** in the note header opens the popover: create a link, pick a
  lifetime, copy it, see the view count, stop sharing.
- Shared notes carry a mark in the list, so "who can see this?" is
  answerable without opening them one by one.
- The **Public links** drawer in the top bar lists every live link with
  its view count and a Revoke button.
- Creating and revoking are recorded in the activity feed. Views are
  counted on the share itself, not in the feed — a popular link must not
  push everything else out of it.

## MCP tools (`VELLUM_SHARING=on`)

| Tool | Summary |
| --- | --- |
| `share_note` | Create (or return) a note's public link. Optional `expires_in` like `24h`, `7d`, `4w`. Returns `url`, `token`, `expires_at`, `reused`. |
| `unshare_note` | Revoke a note's link. Revoking an unshared note is a no-op, not an error. |

The server instructions tell the agent plainly that a link needs no
sign-in and that sharing is publishing, so a client that surfaces tool
annotations can ask before it hands one out.

## REST API

| Endpoint | Purpose |
| --- | --- |
| `GET /api/shares` | Every live link, with view counts and expiry |
| `POST /api/shares` | `{"path": "...", "expiresIn": 604800}` — seconds, 0 = until revoked |
| `DELETE /api/shares/{token}` | Revoke |

These sit behind the normal bearer guard; only `/s/` is public.
`GET /api/version` reports the mode (`off` / `ui` / `on`) so the UI can
hide what the server was not started with.

## What sharing does not change

- vellum still makes **no outbound connections**. A link is served, never
  pushed anywhere.
- There is still **no identity**. A link is "anyone who has it", not
  "shared with a person"; per-user access waits for the team mode.
- The vault is still plain files. Delete `.vellum/shares.json` and every
  link is gone, with every note untouched.

See [threat-model.md](threat-model.md) for what a leaked link costs and
what it does not.
