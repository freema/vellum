# Contributing to vellum

Thanks for taking the time to help. vellum aims to stay **small, calm and
predictable** — a single Go binary over a folder of markdown. Contributions
that keep that spirit are very welcome.

## Ways to help

- **Report a bug** — open a [bug report](https://github.com/freema/vellum/issues/new?template=bug_report.yml).
- **Suggest a feature** — open a [feature request](https://github.com/freema/vellum/issues/new?template=feature_request.yml).
  Small, sharp additions beat large surface area.
- **Report a security issue** — privately, see [SECURITY.md](SECURITY.md).
- **Send a pull request** — see below.

## Development setup

```sh
# Go 1.23+ and Node 20+ (Node only for the web UI / MCP Inspector)
task            # list available tasks (Taskfile.yml)
task test       # backend tests (go test ./...)
task run        # run the server locally
task inspector  # debug the MCP tools in the MCP Inspector — UI at http://localhost:6274
cd web && npm install && npm run build   # build the SPA
go build -tags embedspa ./cmd/vellum     # binary with the UI embedded
```

### Debugging the MCP tools

`task inspector` launches the [MCP Inspector](https://github.com/modelcontextprotocol/inspector)
over the **stdio** transport (it spawns `vellum --mcp-stdio` against the fixture
vault, no OAuth needed) and opens the UI at <http://localhost:6274>. To inspect a
running HTTP server instead, start one (`AUTH_ENABLED=false task run`) and use
`task inspector-http`, then point the Inspector at `http://localhost:8080/mcp`.

`go build` (no tags) stays node-free; the SPA is only embedded with the
`embedspa` tag.

## Ground rules

- **The design is binding.** UI changes follow `design/*.dc.html` and
  `DESIGN.md` (pixel-faithful); deviations should be discussed first.
- **Tests pass and code is formatted.** `go test ./...` green, `go vet ./...`
  clean, `gofmt` applied; for the web UI, `npm run build` and `npm run lint`.
- **Keep the tool surface small.** New MCP tools or endpoints should earn their
  place — prefer composing existing ones.
- **No secrets in code or logs**, ever.
- **Update the changelog** (`CHANGELOG.md`, *Unreleased* section) for anything
  user-visible.

## Pull requests

1. Branch from `main`.
2. Keep the change focused; one concern per PR.
3. Fill in the PR template.
4. CI (build, test, lint) must be green.

By contributing you agree your work is licensed under the project's
[MIT license](LICENSE).
