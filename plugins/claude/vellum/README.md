# vellum plugin

Connects Claude to a [vellum](https://github.com/freema/vellum) markdown
vault over MCP (Streamable HTTP).

Set `VELLUM_URL` to your instance (defaults to `http://localhost:8080`).
The connector URL must end with `/mcp`. On a server with auth on, Claude
runs the OAuth flow the first time: have your `VELLUM_CLIENT_SECRET` ready.

## Local vault

The default URL already fits vellum running on your own machine in HTTP
mode, which listens on `127.0.0.1` and needs no auth there:

```sh
vellum -vault ~/notes
```

Leave `VELLUM_URL` unset and the plugin connects to it. One process serves
the web UI and every client shares its index.

If you would rather have Claude Code start vellum itself over stdio (no
port, no UI), add vellum directly instead of installing this plugin:

```sh
claude mcp add --scope user vellum -- vellum mcp ~/notes
```

Use one of the two, not both: two servers with the same tools side by side
confuse the agent. Install options for the `vellum` binary are in the
[main README](https://github.com/freema/vellum#run-locally).
