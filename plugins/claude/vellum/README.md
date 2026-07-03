# vellum plugin

Connects Claude to a self-hosted [vellum](https://github.com/freema/vellum)
markdown vault over MCP (Streamable HTTP + OAuth).

Set `VELLUM_URL` to your instance (defaults to `http://localhost:8080`).
The connector URL must end with `/mcp`. On first use Claude runs the OAuth
flow — have your `VELLUM_CLIENT_SECRET` ready.
