package httpapi

import (
	"html/template"
	"net/http"
	"strings"
)

// notFound answers a missing static path. Browser navigations (Accept:
// text/html) get the paper-styled error page from the "Vellum Error Pages"
// design; API probes and CLI clients keep Go's plain 404 text.
func notFound(w http.ResponseWriter, r *http.Request) {
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusNotFound)
	_ = errorPageTmpl.Execute(w, errorPage{
		Code:   "404",
		Label:  "HTTP 404 · Not found",
		Title:  "No note lives here",
		Body:   "The path didn't resolve to anything in the vault. It may have been renamed or moved to another folder.",
		Detail: strings.TrimPrefix(r.URL.Path, "/"),
	})
}

type errorPage struct {
	Code   string
	Label  string
	Title  string
	Body   string
	Detail string
}

// errorPageTmpl renders the shared error-page shell (design:
// design/Vellum-Error-Pages.dc.html — paper card, faint serif code, mono
// label). Self-contained: system fonts, no scripts, dark mode via
// prefers-color-scheme with the midnight-ink tokens from DESIGN.md.
var errorPageTmpl = template.Must(template.New("error").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Label}} — vellum</title>
<style>
  :root {
    --canvas:#E4DED3; --bg:#FAF7F2; --panel:#FFFFFF; --ink:#2A2622;
    --muted:#7A7266; --faint:#DDD4C4; --line:#E0D9CD; --line-soft:#E8E2D8;
    --accent:#8B6F47; --accent-hover:#7A6039; --danger:#B3543D;
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --canvas:#16141F; --bg:#1E1B29; --panel:#16141F; --ink:#E8E4DC;
      --muted:#8F8A9B; --faint:#2E2A3A; --line:#2E2A3A; --line-soft:#2E2A3A;
      --accent:#C2A878; --accent-hover:#C2A878;
    }
  }
  * { box-sizing:border-box; margin:0; }
  body {
    background:var(--canvas); color:var(--ink); min-height:100vh;
    display:flex; align-items:center; justify-content:center; padding:24px;
    font-family:system-ui, -apple-system, sans-serif;
    -webkit-font-smoothing:antialiased;
  }
  .card {
    background:var(--bg); border:1px solid var(--line); border-radius:12px;
    box-shadow:0 18px 50px -24px rgba(42,38,34,.28); max-width:640px; width:100%;
    padding:72px 40px 80px; text-align:center;
  }
  .code {
    font-family:Georgia, 'Times New Roman', serif; font-size:92px;
    font-weight:500; line-height:1; letter-spacing:-.02em; color:var(--faint);
  }
  .label {
    font-family:ui-monospace, SFMono-Regular, Menlo, monospace; font-size:11px;
    letter-spacing:.14em; text-transform:uppercase; color:var(--accent);
    margin:22px 0 12px;
  }
  h1 {
    font-family:Georgia, 'Times New Roman', serif; font-size:30px;
    font-weight:500; letter-spacing:-.01em;
  }
  p { font-size:15px; line-height:1.65; color:var(--muted); max-width:410px; margin:12px auto 0; }
  .detail {
    display:inline-block; font-family:ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size:12.5px; color:var(--muted); background:var(--panel);
    border:1px solid var(--line-soft); border-radius:7px; padding:10px 14px;
    margin-top:22px; word-break:break-all;
  }
  .detail b { color:var(--danger); font-weight:400; }
  a.btn {
    display:inline-block; font-size:13.5px; font-weight:500; color:#FFFFFF;
    background:var(--accent); border:1px solid var(--accent); border-radius:6px;
    padding:10px 18px; margin-top:28px; text-decoration:none;
  }
  a.btn:hover { background:var(--accent-hover); border-color:var(--accent-hover); }
</style>
</head>
<body>
  <main class="card">
    <div class="code">{{.Code}}</div>
    <div class="label">{{.Label}}</div>
    <h1>{{.Title}}</h1>
    <p>{{.Body}}</p>
    {{if .Detail}}<div class="detail"><b>{{.Detail}}</b></div>{{end}}
    <a class="btn" href="/">Open vellum</a>
  </main>
</body>
</html>
`))
