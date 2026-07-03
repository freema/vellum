# vellum

A lightweight, self-hosted MCP server over a folder of markdown files.
One static Go binary (with an embedded React SPA), one Docker image.
`docker compose up -d` and you're done.

> **Status: work in progress** — walking skeleton. Vault, MCP tools, OAuth
> and the web UI are being built milestone by milestone.

## Principles

- **Your data is flat markdown.** Portable, readable in any editor, git or Obsidian. No lock-in.
- **vellum never calls an LLM itself.** Curation prepares context; the agent on the user's side decides.
- **The vault layer is dumb and deterministic.** No database — config via env + `vellum.yaml`.
- **Structure is a feature.** `inbox/`, `projects/`, `archive/` — not a pile of files.

## Quick start

```sh
docker compose up -d
curl http://localhost:8080/healthz
```

## Development

Requires Go 1.23+ and [Task](https://taskfile.dev).

```sh
task build        # build ./bin/vellum
task run          # run locally
task test         # go test ./...
task lint         # golangci-lint
task docker-build # build the Docker image
task docker-run   # docker compose up
```

## Configuration

| Variable            | Default   | Description                          |
| ------------------- | --------- | ------------------------------------ |
| `PORT`              | `8080`    | HTTP listen port                     |
| `VELLUM_VAULT_PATH` | `./vault` | Path to the markdown vault directory |

More configuration (auth, structure, curator) lands with upcoming milestones.

## License

[MIT](LICENSE)
