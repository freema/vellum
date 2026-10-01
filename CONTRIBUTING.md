# Contributing to vellum

Thanks for taking the time to help. vellum aims to stay **small, calm and
predictable** — a single Go binary over a folder of markdown. Contributions
that keep that spirit are very welcome.

## Ways to help

- **Report a bug** — open a [bug report](https://github.com/freema/vellum/issues/new?template=bug_report.yml).
- **Suggest a feature** — open a [feature request](https://github.com/freema/vellum/issues/new?template=feature_request.yml).
  Small, sharp additions beat large surface area.
- **Report a security issue** — privately, see [SECURITY.md](SECURITY.md).
  Never in a public issue or pull request.
- **Ask a question** — see [SUPPORT.md](SUPPORT.md).
- **Send a pull request** — see below.

If you plan anything bigger than a bug fix, open an issue first and describe
what you want to change. A short discussion up front saves a rewrite later,
and some ideas are out of scope on purpose (see [Scope](#scope)).

## Development setup

You need **Go 1.25+** (the release image builds with Go 1.26),
[Task](https://taskfile.dev), and **Node 22** for the web UI.

```sh
task            # list available tasks (Taskfile.yml)
task test       # backend tests (go test ./...)
task lint       # golangci-lint (CI pins v2.12.2)
task run        # run the server locally against ./vault
task build-full # SPA + Go binary with the UI embedded
task inspector  # debug the MCP tools in the MCP Inspector — UI at http://localhost:6274
task e2e        # compose stack with the fixture vault (docs/e2e.md)
```

`go build` (no tags) stays node-free; the SPA is only embedded with the
`embedspa` tag. For UI work run the Vite dev server, which proxies `/api`,
`/mcp`, `/token` and `/healthz` to a backend on port 8099:

```sh
PORT=8099 AUTH_ENABLED=false task run   # terminal 1: the backend
cd web && npm install && npm run dev    # terminal 2: the SPA with hot reload
```

Auth is off unless you turn it on. To run the server with OAuth, export the
variables before `task run` (`.env.example` lists all of them; `.env` itself
is only read by Docker Compose):

```sh
export AUTH_ENABLED=true VELLUM_CLIENT_SECRET=$(openssl rand -hex 32) \
       VELLUM_ISSUER_URL=http://localhost:8080
task run
```

### Debugging the MCP tools

`task inspector` launches the [MCP Inspector](https://github.com/modelcontextprotocol/inspector)
over the **stdio** transport (it spawns `vellum --mcp-stdio` against the fixture
vault, no OAuth needed) and opens the UI at <http://localhost:6274>. To inspect a
running HTTP server instead, start one (`AUTH_ENABLED=false task run`) and use
`task inspector-http`, then point the Inspector at `http://localhost:8080/mcp`.

## Finding your way around

[ARCHITECTURE.md](ARCHITECTURE.md) maps the packages and follows a request
from the HTTP mux to the file on disk. The short version:

| Path | What lives there |
|---|---|
| `cmd/vellum/` | `main`: flags, config, wiring |
| `internal/vault/` | the file layer — CRUD, path validation, index, search, tasks, tags |
| `internal/mcpserver/` | MCP tools and resources, 1:1 over vault operations |
| `internal/httpapi/` | HTTP router: `/healthz`, `/mcp`, `/api/*`, `/s/*`, the SPA |
| `internal/auth/` | OAuth 2.1 issuer + bearer verification |
| `internal/share/` | public read-only links (`VELLUM_SHARING`) |
| `internal/activity/`, `notify/`, `obs/` | activity feed, e-mail digest, Sentry |
| `web/` | the React SPA (Vite, plain CSS custom properties) |
| `design/` | the binding visual design (`*.dc.html`, see [DESIGN.md](DESIGN.md)) |
| `docs/` | user and operator documentation ([index](docs/README.md)) |
| `testdata/vault/` | the deterministic fixture vault used by tests and e2e |

## Ground rules

- **The design is binding.** UI changes follow `design/*.dc.html` and
  [DESIGN.md](DESIGN.md) (pixel-faithful); deviations should be discussed first.
- **Tests pass and code is formatted.** `go test ./...` green, `go vet ./...`
  clean, `gofmt` applied, `task lint` clean; for the web UI, `npm run build`
  and `npm run lint`.
- **Keep the tool surface small.** New MCP tools or endpoints should earn their
  place — prefer composing existing ones. Every tool costs agent context in
  every session.
- **No new runtime dependencies without a reason.** vellum is one static
  binary with a short `go.mod`; say in the PR why a new module is worth it.
- **No secrets in code or logs**, ever. Tokens, the client secret and share
  tokens are never logged (see [docs/logging.md](docs/logging.md)).
- **Update the changelog** (`CHANGELOG.md`, *Unreleased* section) for anything
  user-visible. Write it for the person upgrading: what changes for them, not
  which functions moved.
- **English only** in code, comments, commits, PRs and docs.

### Security-sensitive areas

Changes here get a closer review, and need a test that tries the attack, not
only the happy path:

- `internal/vault` path handling — traversal, symlinks, hidden directories,
  null bytes. The existing tests show the style.
- `internal/auth` — who may obtain a token, and how. Read the
  [threat model](docs/threat-model.md) section *Who can authorize a client*
  first.
- `internal/share` and `/s/*` — the only unauthenticated surface.
- Anything that adds an outbound connection. vellum makes none today, and
  that is a property worth keeping.

## Tests

- Backend tests live next to the code (`*_test.go`) and use the standard
  `testing` package, no assertion library. Prefer real HTTP through
  `httptest` over mocks —
  `internal/mcpserver/http_test.go` drives the whole stack, OAuth included.
- `testdata/vault/` is the shared fixture. If you change it, check the tests
  and the e2e checklist that count its notes.
- Bug fixes come with a test that fails without the fix.
- UI changes: walk the relevant part of the [e2e checklist](docs/e2e.md) and
  attach screenshots to the PR.

## Commits and pull requests

Commit subjects follow a light [Conventional Commits](https://www.conventionalcommits.org/)
style, as in the existing history:

```
fix(vault): reject writes that resolve into a hidden dir via symlink
feat(share): public read-only links to single notes
deps: golang.org/x/text 0.39.0
docs: …   ci: …   refactor: …   test: …
```

The scope is the package or area (`vault`, `auth`, `mcp`, `web`, `share`, …).
Use the body to explain *why*, and what a user would notice.

1. Branch from `main`.
2. Keep the change focused; one concern per PR.
3. Fill in the PR template.
4. CI (build, test, lint) must be green.
5. PRs are squash-merged, so the PR title becomes the commit subject.

Releases are cut by the maintainer, see [RELEASING.md](RELEASING.md).

## Scope

vellum deliberately does **not** do some things, and PRs that add them are
unlikely to be merged:

- a database, embeddings or a vector index,
- calling an LLM or any other external service from the server,
- user accounts or multi-tenant hosting (team mode is planned, but small),
- a plugin system or scripting inside the server.

When in doubt, ask in an issue first.

## License

By contributing you agree your work is licensed under the project's
[MIT license](LICENSE).
