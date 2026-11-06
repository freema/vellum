package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/freema/vellum/internal/config"
)

// cliOptions is what the command line asked for. Everything else is env.
type cliOptions struct {
	healthcheck bool
	version     bool
	// stdio serves MCP over stdin/stdout: `vellum mcp` or -mcp-stdio.
	stdio bool
	// initVault lets stdio mode create a missing vault folder.
	initVault bool
	// vault is the path from the argument or -vault, "" when not given.
	vault string
}

const usageText = `vellum serves a folder of markdown notes over MCP, with a web UI and REST API.

Usage:
  vellum [flags]                serve the web UI, REST API and MCP over HTTP
  vellum mcp [flags] <vault>    serve MCP over stdio for a local client, e.g.
                                claude mcp add vellum -- vellum mcp ~/notes

The vault folder comes from the <vault> argument or -vault, then from
VELLUM_VAULT_PATH. HTTP mode falls back to ./vault and creates it if needed.
MCP over stdio needs a folder that already exists, unless -init is given.

HTTP mode listens on VELLUM_HOST:PORT, by default 127.0.0.1:8080 (this
machine only). Set VELLUM_HOST=0.0.0.0 to accept connections from the
network, and turn on AUTH_ENABLED when you do.

Flags:
`

// parseArgs reads the command line. A leading "mcp" selects stdio mode and
// takes the vault path as its argument; flags may sit before or after it.
// The error is flag.ErrHelp for -h, otherwise already printed to stderr.
func parseArgs(args []string, stderr io.Writer) (cliOptions, error) {
	var o cliOptions
	fs := flag.NewFlagSet("vellum", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), usageText)
		fs.PrintDefaults()
	}
	fs.BoolVar(&o.healthcheck, "healthcheck", false, "probe the running server's /healthz and exit (used by Docker HEALTHCHECK)")
	fs.BoolVar(&o.version, "version", false, "print version and exit")
	fs.BoolVar(&o.stdio, "mcp-stdio", false, "serve MCP over stdio instead of HTTP (same as the mcp subcommand)")
	fs.StringVar(&o.vault, "vault", "", "vault folder (overrides VELLUM_VAULT_PATH; ~/ is expanded)")
	fs.BoolVar(&o.initVault, "init", false, "with MCP over stdio: create the vault folder if it does not exist")

	subcommand := len(args) > 0 && args[0] == "mcp"
	if subcommand {
		o.stdio = true
		args = args[1:]
	}

	// The flag package stops at the first non-flag argument; keep parsing
	// after it so `vellum mcp ~/notes -init` works like `vellum mcp -init ~/notes`.
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return o, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" {
			positional = append(positional, rest...)
			break
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}

	switch {
	case len(positional) == 0:
	case !subcommand:
		return o, usageError(stderr, "unexpected argument %q: the vault path goes after `vellum mcp`, or use -vault", positional[0])
	case len(positional) > 1:
		return o, usageError(stderr, "too many arguments: `vellum mcp` takes one vault path, got %d", len(positional))
	case o.vault != "" && o.vault != positional[0]:
		return o, usageError(stderr, "the vault is given twice (%q and -vault %q): pass only one", positional[0], o.vault)
	default:
		o.vault = positional[0]
	}
	return o, nil
}

func usageError(stderr io.Writer, format string, a ...any) error {
	err := fmt.Errorf(format, a...)
	_, _ = fmt.Fprintln(stderr, "vellum:", err)
	_, _ = fmt.Fprintln(stderr, "Run 'vellum -h' for usage.")
	return err
}

// defaultVaultPath is the HTTP-mode fallback; the Docker image sets
// VELLUM_VAULT_PATH=/vault instead.
const defaultVaultPath = "./vault"

// errNoVault is returned when stdio mode has no vault path at all. A relative
// ./vault would land wherever the MCP client happens to start the process.
var errNoVault = errors.New("no vault folder given: run `vellum mcp /path/to/vault` or set VELLUM_VAULT_PATH")

// resolveVaultPath picks the vault path (argument or -vault, then
// VELLUM_VAULT_PATH, then ./vault for HTTP only), expands a leading ~/ and
// makes it absolute, so the log shows exactly which folder is served.
func resolveVaultPath(arg, env string, stdio bool) (string, error) {
	p := arg
	if p == "" {
		p = env
	}
	if p == "" {
		if stdio {
			return "", errNoVault
		}
		p = defaultVaultPath
	}
	p, err := expandHome(p)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve vault path %s: %w", p, err)
	}
	return abs, nil
}

// expandHome replaces a leading ~ or ~/ with the home directory. MCP clients
// read their config from JSON and start the binary without a shell, so
// nothing else expands it. ~user is left alone.
func expandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand %s: %w", p, err)
	}
	return filepath.Join(home, p[1:]), nil
}

// ensureVaultDir checks that the vault folder exists. A missing folder is an
// error unless create is set: a mistyped path in an MCP client config must
// not leave an empty vault somewhere on disk.
func ensureVaultDir(path string, create bool) error {
	info, err := os.Stat(path)
	switch {
	case err == nil && !info.IsDir():
		return fmt.Errorf("vault path %s is not a folder", path)
	case err == nil:
		return nil
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("vault folder %s: %w", path, err)
	case create:
		return os.MkdirAll(path, 0o755)
	default:
		return fmt.Errorf("vault folder %s does not exist: pass an existing folder (vellum mcp /path/to/vault, or VELLUM_VAULT_PATH), or add -init to create it", path)
	}
}

// listenAddr is the HTTP listen address, host:port.
func listenAddr(cfg config.Config) string {
	return net.JoinHostPort(strings.Trim(cfg.Host, "[]"), cfg.Port)
}

// listen opens the HTTP listener on VELLUM_HOST:PORT.
func listen(cfg config.Config) (net.Listener, error) {
	return net.Listen("tcp", listenAddr(cfg))
}

// boundAddr is the address to report for a listener: the configured host
// (0.0.0.0 stays 0.0.0.0 rather than Go's dual-stack [::]) with the port
// actually bound.
func boundAddr(cfg config.Config, ln net.Listener) string {
	port := cfg.Port
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		port = strconv.Itoa(tcp.Port)
	}
	return net.JoinHostPort(strings.Trim(cfg.Host, "[]"), port)
}

// isLoopback reports whether a listener only accepts connections from its
// own machine.
func isLoopback(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}

// healthcheckURL is where -healthcheck finds the server. A wildcard host
// (0.0.0.0, ::) is reached over loopback; a specific host is used as is.
func healthcheckURL(cfg config.Config) string {
	host := strings.Trim(cfg.Host, "[]")
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, cfg.Port) + "/healthz"
}
