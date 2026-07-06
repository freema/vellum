# vellum

> *a calm window into a folder of markdown*

[![Release](https://img.shields.io/github/v/release/freema/vellum)](https://github.com/freema/vellum/releases)
[![CI](https://github.com/freema/vellum/actions/workflows/ci.yml/badge.svg)](https://github.com/freema/vellum/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-brown.svg)](LICENSE)
[![GHCR](https://img.shields.io/badge/ghcr.io-freema%2Fvellum-8B6F47)](https://github.com/freema/vellum/pkgs/container/vellum)

A lightweight, **self-hosted MCP server** over a folder of markdown files.
One static Go binary with an embedded web UI, one ~24 MB Docker image,
~5 MB idle RAM. `docker compose up -d` and you're done.

**Deliberately light** — the smallest stack that just works, for one person
or a small team:

- **Your data is flat markdown.** Portable, readable in any editor, git or
  Obsidian. No lock-in, no database, no embeddings.
- **vellum never calls an LLM itself.** The optional curator tools prepare
  context; the agent on *your* side decides.
- **The vault layer is dumb and deterministic.** Everything is a file;
  config is environment variables.
- **Structure is a feature.** `inbox/`, `projects/`, `archive/` — not a
  pile of files. Unfiled notes land in the inbox.
- **Small MCP tool surface** (15 tools) — less agent context burned on
  tool definitions.

## Quick start

```sh
mkdir vellum && cd vellum
curl -fsSLO https://raw.githubusercontent.com/freema/vellum/main/docker-compose.yml
cat > .env <<EOF
AUTH_ENABLED=true
VELLUM_CLIENT_SECRET=$(openssl rand -hex 32)
EOF
docker compose up -d
curl http://localhost:8080/healthz
```

Open http://localhost:8080, paste the client secret — that's your vault.

## Connect Claude

The connector URL **must end with `/mcp`**:

```sh
claude mcp add --transport http vellum https://your-host/mcp
```

Claude runs the OAuth flow: a browser window opens vellum's consent screen,
you approve, tokens are exchanged with your `VELLUM_CLIENT_SECRET` behind
the scenes (authorization code + PKCE; vellum is its own OAuth issuer — no
external identity provider, no calls out). The same works for a
[claude.ai custom connector](https://claude.ai/settings/connectors) — enter
`vellum` as client ID and your secret.

Local, no Docker: `vellum -mcp-stdio` serves MCP over stdio.

### MCP tools

`list_notes`, `read_note`, `write_note`, `patch_note`, `append_to_note`,
`prepend_to_note`, `delete_note`, `move_note`, `search_notes`, `list_tags`,
`add_tags`, `remove_tags`, `get_backlinks`, `set_status`, `list_tasks` —
plus, with `VELLUM_CURATOR=on`: `suggest_location`, `suggest_tags`,
`suggest_links`, `find_untagged`, `find_orphans`, `find_inbox_stale`.

Writes are conflict-safe: `read_note` returns a content hash, write tools
accept `expected_hash` and fail on mismatch instead of clobbering.

## Vault structure, tasks, curator

- **Structure**: an empty vault is initialized with `inbox/`, `projects/`,
  `archive/` (disable with `VELLUM_INIT_STRUCTURE=false`). A `write_note`
  without a path files the note into the inbox under a title slug.
  Pointing vellum at an existing vault changes nothing.
- **Tasks**: a note with `type: task` and `status: backlog | in-progress |
  done` in the frontmatter. `set_status` edits just those lines; the UI and
  `list_tasks` filter by state and project. Notes without a type are
  `knowledge`.
- **Curator** (`VELLUM_CURATOR=on`, default off): deterministic context
  tools — folder suggestions by tag overlap, tag vocabulary for a note,
  link candidates, untagged/orphaned/stale-inbox lists. No API keys, no
  LLM calls from the server, moves stay behind the regular `move_note`.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8080` | HTTP listen port |
| `VELLUM_VAULT_PATH` | `./vault` | Vault directory (Docker: `/vault`) |
| `AUTH_ENABLED` | `false` | OAuth 2.1 with a single client secret — **required for anything public** |
| `VELLUM_CLIENT_ID` | `vellum` | OAuth client id |
| `VELLUM_CLIENT_SECRET` | — | The access key (`openssl rand -hex 32`, min 32 chars) |
| `VELLUM_ISSUER_URL` | `http://localhost:PORT` | **Public HTTPS URL when behind a proxy** — without it OAuth metadata advertises localhost and clients fail |
| `TRUST_PROXY` | `false` | Set `1` behind Caddy/Traefik so rate limiting sees real client IPs |
| `CORS_ORIGINS` | claude.ai, claude.com | Origins receiving CORS headers (localhost always allowed) |
| `VELLUM_ALLOWED_ORIGINS` | claude.ai, claude.com | Browser origins allowed on `/mcp` + `/api` (same-origin and localhost always pass) |
| `VELLUM_REDIRECT_URIS` | any | Optional exact OAuth redirect allowlist |
| `VELLUM_INIT_STRUCTURE` | `true` | Create inbox/projects/archive in an empty vault |
| `VELLUM_INBOX_DIR` / `VELLUM_PROJECTS_DIR` / `VELLUM_ARCHIVE_DIR` | `inbox`/`projects`/`archive` | Conventional directory names |
| `VELLUM_CURATOR` | `off` | Enable the suggest_*/find_* context tools |

## Deployment

vellum stays on the internal network; only your reverse proxy is exposed.
Set `VELLUM_ISSUER_URL=https://<domain>`, `TRUST_PROXY=1` and terminate TLS
in the proxy. Details and a smoke-test checklist: [docs/deployment.md](docs/deployment.md).

**Coolify / Traefik**: point Coolify at the repo or image
`ghcr.io/freema/vellum` (pin a version, e.g. `:0.2.0`, not `:latest`),
mount a volume at `/vault`, set the env from the table above. Running raw
compose behind Traefik, uncomment the labels in `docker-compose.yml`:

```yaml
labels:
  - traefik.enable=true
  - traefik.http.routers.vellum.rule=Host(`vellum.example.com`)
  - traefik.http.services.vellum.loadbalancer.server.port=8080
```

**Caddy** is two lines:

```
vellum.example.com {
    reverse_proxy vellum:8080
}
```

Security posture (read-only container, threat model, what is never
logged): [SECURITY.md](SECURITY.md), [docs/threat-model.md](docs/threat-model.md),
[docs/logging.md](docs/logging.md).

## Development

Requires Go 1.25+, [Task](https://taskfile.dev), and Node 22 for the SPA.

```sh
task build        # Go binary without the SPA (no node needed)
task build-full   # SPA + Go binary with the UI embedded
task test         # go test ./...
task lint         # golangci-lint
task e2e          # compose stack with the fixture vault (docs/e2e.md)
```

### Design decisions kept deliberately simple

- **Search = ranked RAM scan, not bleve.** The metadata index narrows by
  tag/dir, content is matched from an in-memory cache invalidated precisely
  by the index (modTime+size), and results are ranked (title > tag > path >
  body, word starts beat mid-word). Matching is case-, diacritics- and
  typo-insensitive — "preklapy" finds "překlepy". A query over a 2 000-note
  vault takes ~0.4 ms warm ([docs/search.md](docs/search.md)). Zero heavy
  dependencies, no index to corrupt, no warm-up. The `Searcher` interface
  exists so bleve can slot in *if it ever hurts*.
- **Plain CSS custom properties, not Tailwind.** The design system is a
  fixed token sheet (`DESIGN.md`); custom properties map to it 1:1 with no
  build-time dependency.
- **Opaque in-memory tokens, not JWTs.** Tokens die with the process and
  clients silently re-authorize; no signing keys to manage.
- **No database, ever (v1).** Tags, backlinks and task states live in an
  in-RAM index rebuilt in ~50 ms at startup.

## Why vellum and not …?

- **Obsidian + plugins** — vellum doesn't replace your editor; it happily
  serves the same vault folder to your agent while you keep editing
  anywhere. No sync, no plugin sandbox, no Electron on the server.
- **Notion/Anytype-style apps** — those own your data. vellum's "database"
  is `ls` and `grep`-able markdown.
- **Heavier MCP note servers** — vellum ships one 24 MB container, no
  vector DB, no API keys, and burns minimal agent context (15 tools).

## Author

Created by **Tomáš Grasl** ([@freema](https://github.com/freema)).

Issues and PRs welcome — the [threat model](docs/threat-model.md) and
[SECURITY.md](SECURITY.md) explain the boundaries contributions must keep.

## License

[MIT](LICENSE)
