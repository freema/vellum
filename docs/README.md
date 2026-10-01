# vellum documentation

Start with the [README](../README.md) for the quick start and configuration.
The pages here go deeper.

## Running vellum

- [deployment.md](deployment.md): self-hosting behind a reverse proxy, HTTPS,
  the production checklist.
- [observability.md](observability.md): optional Sentry error reporting.
- [logging.md](logging.md): what vellum logs, and what it never logs.

## Using it

- [mcp.md](mcp.md): transports, every MCP tool with its annotations,
  resources and subscriptions.
- [workspace.md](workspace.md): the web workspace: URLs, local state, saving
  and synchronization.
- [search.md](search.md): how full-text search matches and ranks.
- [sharing.md](sharing.md): public read-only links to single notes.

## Security

- [threat-model.md](threat-model.md): trust boundaries, who can authorize a
  client, what a compromised agent can do.
- [SECURITY.md](../SECURITY.md): supported versions and how to report a
  vulnerability.

## Working on vellum

- [CONTRIBUTING.md](../CONTRIBUTING.md): setup, ground rules, pull requests.
- [ARCHITECTURE.md](../ARCHITECTURE.md): packages and the path of a request.
- [DESIGN.md](../DESIGN.md): the binding visual design and its tokens.
- [e2e.md](e2e.md): the end-to-end checklist run against the fixture vault.
- [RELEASING.md](../RELEASING.md): cutting a release and a security fix.
