# Getting help

vellum is maintained by one person in their spare time. These are the
quickest ways to an answer.

## Read first

- [README](README.md): quick start, configuration, connecting Claude.
- [docs/deployment.md](docs/deployment.md): reverse proxy, HTTPS, the
  production checklist. Most "Claude fails after the consent screen"
  problems are a missing `VELLUM_ISSUER_URL` or `TRUST_PROXY=1`.
- [docs/mcp.md](docs/mcp.md): the MCP tools and resources.
- [All documentation](docs/README.md).

## Ask

| You have… | Go to |
|---|---|
| a question, or want to share your setup | [Discussions](https://github.com/freema/vellum/discussions) |
| found a bug | [bug report](https://github.com/freema/vellum/issues/new?template=bug_report.yml) |
| an idea for a feature | [feature request](https://github.com/freema/vellum/issues/new?template=feature_request.yml) |
| found a security problem | a **private** report, see [SECURITY.md](SECURITY.md) |

A good bug report names the vellum version (`vellum -version`, or `version`
in `GET /healthz`), how you run it (Docker, binary, behind which proxy), the
MCP client, and what you expected versus what happened. Do not paste your
`VELLUM_CLIENT_SECRET`, tokens or share links.

## What to expect

Issues are read, but there is no response-time promise outside security
reports. Pull requests that follow [CONTRIBUTING.md](CONTRIBUTING.md) are
the fastest way to get something changed.
