# Architecture

A map for contributors: what each package does, how a request travels from
the network to a file on disk, and which invariants the layers rely on. For
*why* things are this simple, see the README's
[design decisions](README.md#design-decisions-kept-deliberately-simple); for
the security boundaries, see the [threat model](docs/threat-model.md).

## The shape of it

```
            ┌───────────────────────── vellum (one process) ──────────────────────────┐
 agent ──▶  │ /mcp ─────▶ mcpserver ──┐                                               │
            │                         ├──▶ vault.Index ───▶ vault.Vault ──▶ folder of │
 browser ─▶ │ /api/* ───▶ httpapi.api ┘    (RAM: tags,      (paths, files,     .md    │
            │ /  ───────▶ embedded SPA      links, tasks)    hashes)          files  │
            │ /s/{token} ▶ public share page ──▶ share.Store (.vellum/shares.json)    │
            │ /authorize, /token, … ▶ auth.Provider (in-memory clients and tokens)    │
            └─────────────────────────────────────────────────────────────────────────┘
```

There is no database and no background worker that touches notes. Everything
except share links lives in memory and is rebuilt from the folder on start.

## Packages

| Package | Responsibility |
|---|---|
| `cmd/vellum` | Flags (`-mcp-stdio`, `-healthcheck`, `-version`), config, wiring of everything below, graceful shutdown. |
| `internal/config` | Reads the environment. There is no config file; every setting is an env variable listed in `.env.example`. |
| `internal/vault` | The file layer. `Vault` validates every path and does CRUD with optimistic concurrency (content hashes). `Index` holds the metadata in RAM. `ScanSearcher` ranks search results. Also tasks, tags and the inbox/projects/archive `Structure`. |
| `internal/mcpserver` | MCP tools and resources (`vellum://note/{path}`) on the official Go SDK. Each tool maps 1:1 to a vault or index operation. Curator tools only *prepare context*, they never decide. |
| `internal/httpapi` | The root router: `/healthz`, `/mcp`, the JSON API for the SPA under `/api/*`, public share pages under `/s/*`, the embedded SPA, compression, the origin check, error pages and panic recovery. |
| `internal/auth` | vellum's own OAuth 2.1 authorization server and the bearer check (`RequireBearer`). Clients, codes and tokens are in memory. |
| `internal/share` | Public read-only links: token → path, persisted in `<vault>/.vellum/shares.json`, kept in step with renames and deletes. |
| `internal/activity` | A bounded in-memory ring of recent tool calls and connected clients, for the workspace's Connections and Activity panels. |
| `internal/notify` | Optional e-mail digest of open tasks over SMTP. |
| `internal/obs` | Optional Sentry reporting (`SENTRY_DSN`). |
| `web/` | React SPA built with Vite. Plain CSS custom properties mapped 1:1 to the design tokens. Embedded into the binary with `-tags embedspa`. |

## A request, end to end

Take `write_note` from Claude:

1. **Transport.** `POST /mcp` reaches the `http.ServeMux` built by
   `httpapi.NewRouter`. CORS and panic recovery wrap the whole mux.
2. **Guard.** The `/mcp` and `/api/` mounts go through `guard`: the bearer
   check (`auth.Provider.RequireBearer`, when `AUTH_ENABLED=true`) and the
   Origin allowlist. Share pages and OAuth endpoints are deliberately outside
   it.
3. **Activity.** `mcpRecord` notes the call for the workspace's live panels.
4. **Tool.** The MCP SDK decodes the call and invokes the handler in
   `mcpserver`, which validates arguments and calls the vault.
5. **Vault.** `Vault.Write` resolves the path (`filepath.Clean`, root
   confinement, symlink checks, no hidden segments, `.md` only, size cap),
   compares the expected content hash when the caller sent one, and writes.
6. **Index.** The caller tells the index (`Index.Update` / `Remove` /
   `Rename`). The index re-reads that one file, recomputes tags, wikilink
   resolution and tasks in RAM, and notifies observers (MCP resource
   subscriptions). On a move or delete the same caller also updates the
   share store (`Shares.Rename` / `RevokePath`), which is how a share link
   follows its note.
7. **Response.** The tool returns text plus structured content. Errors from
   the vault are sentinel errors (`ErrNotFound`, `ErrConflict`, …) mapped to
   MCP errors.

The web workspace takes the same path through `/api/*` instead of `/mcp`. It
gets its token with the `client_credentials` grant from the connect screen.

## Invariants worth knowing

- **Only `internal/vault` touches note files.** Everything else goes through
  `Vault`, so the path checks live in one place. Keep it that way.
- **The vault layer is dumb.** It knows files, not tags or links. Derived
  data belongs to `Index`, and `Index` changes only through
  `Update`/`Remove`/`Rename` after a successful vault operation.
- **Writes that bypass vellum are not watched.** The index is built once at
  startup. A note changed by another program (an editor, `git pull`) is read
  fresh when opened, but lists, search, tags and backlinks see the change
  only after a restart.
- **Hidden paths are off-limits.** Any dot segment (`.git`, `.obsidian`,
  `.vellum`) is invisible and unwritable. That is also what keeps
  `.vellum/shares.json` out of reach of note writes.
- **No outbound connections**, except the opt-in SMTP digest and Sentry.
- **Tokens are opaque and in memory.** A restart signs every client out, and
  clients re-authorize silently. Share links are the one piece of state that
  persists.

## Where to add things

| You want to… | Start in |
|---|---|
| add or change an MCP tool | `internal/mcpserver/server.go`, plus a test in `http_test.go` or `server_test.go`, the tool table in `README.md` and the consent list in `internal/auth/consent.go` |
| change what is indexed or how search ranks | `internal/vault/index.go`, `search.go`, and [docs/search.md](docs/search.md) |
| add a REST endpoint for the SPA | `internal/httpapi/api.go`, then `web/src/lib/api.ts` |
| add a configuration option | `internal/config/config.go`, `.env.example`, and the README table |
| change the UI | `web/src/`, following [DESIGN.md](DESIGN.md) |
