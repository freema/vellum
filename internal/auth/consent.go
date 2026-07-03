package auth

import (
	"html/template"
	"net/http"
	"net/url"
)

// consentTool is one row of the tool list on the consent screen.
type consentTool struct {
	Name string
	Kind string // read | write | delete
	Desc string
}

// consentTools mirrors the MCP tool surface (PHY-111), grouped by scope kind.
var consentTools = []consentTool{
	{"read_note", "read", "Read any note in the vault"},
	{"search_notes", "read", "Fulltext + tag search across the vault"},
	{"list_notes", "read", "List folders and note metadata"},
	{"list_tags", "read", "List all tags with counts"},
	{"get_backlinks", "read", "Show connections between notes"},
	{"list_tasks", "read", "List task notes and their states"},
	{"write_note", "write", "Create and edit notes"},
	{"patch_note", "write", "Replace a single note section"},
	{"append_to_note", "write", "Append to a note"},
	{"prepend_to_note", "write", "Prepend to a note"},
	{"add_tags", "write", "Add frontmatter tags"},
	{"remove_tags", "write", "Remove frontmatter tags"},
	{"set_status", "write", "Set task status"},
	{"move_note", "write", "Move notes between folders"},
	{"delete_note", "delete", "Permanently remove a note"},
}

// Design tokens from DESIGN.md / design/Vellum-Auth.dc.html (artboard 1b).
// Fonts fall back to system faces until the self-hosted webfonts land with
// the embedded SPA (PHY-116/130) — no CDN fonts, per project rules.
var consentTmpl = template.Must(template.New("consent").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Authorize · vellum</title>
<style>
  * { box-sizing: border-box; }
  html, body { margin: 0; padding: 0; }
  body {
    background: #E7E1D6;
    font-family: 'Inter', system-ui, -apple-system, sans-serif;
    color: #2A2622;
    -webkit-font-smoothing: antialiased;
    display: flex; justify-content: center; align-items: flex-start;
    padding: 52px 20px;
  }
  ::selection { background: #F1EAE0; color: #2A2622; }
  .serif { font-family: 'Newsreader', Georgia, 'Times New Roman', serif; }
  .mono { font-family: 'JetBrains Mono', ui-monospace, SFMono-Regular, Menlo, monospace; }
  .card { width: 520px; background: #FAF7F2; border: 1px solid #E0D9CD; border-radius: 12px; overflow: hidden; }
  .header { padding: 30px 34px 22px; border-bottom: 1px solid #EDE6DB; }
  .marks { display: flex; align-items: center; justify-content: center; gap: 16px; margin-bottom: 22px; }
  .vmark { position: relative; width: 40px; height: 40px; }
  .vmark .tile { position: absolute; inset: 0; background: #8B6F47; border-radius: 8px; }
  .vmark .foldbg { position: absolute; top: 0; right: 0; width: 14px; height: 14px; background: #FAF7F2; border-bottom-left-radius: 5px; }
  .vmark .fold { position: absolute; top: 0; right: 0; width: 14px; height: 14px; background: #6F5836; clip-path: polygon(0 0, 100% 100%, 0 100%); }
  .vmark .v { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; padding-top: 4px; font-size: 23px; font-weight: 500; color: #FBF7F0; line-height: 1; }
  .arrow { color: #CFC6B7; font-size: 14px; }
  .agent { width: 40px; height: 40px; border-radius: 8px; background: #FFFFFF; border: 1px solid #E0D9CD; display: flex; align-items: center; justify-content: center; font-size: 20px; color: #7A7266; }
  h1 { text-align: center; font-size: 21px; font-weight: 500; line-height: 1.3; margin: 0; }
  h1 em { font-style: italic; }
  .signed { text-align: center; font-size: 13px; color: #7A7266; margin-top: 6px; }
  .signed .host { font-size: 12px; color: #8B6F47; }
  .tools { padding: 22px 34px 8px; }
  .tools-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 4px; }
  .tools-title { font-size: 13px; font-weight: 600; }
  .tools-count { font-size: 11px; color: #9A938A; }
  .tools-sub { font-size: 12px; color: #7A7266; margin-bottom: 16px; line-height: 1.5; }
  .tool { display: flex; align-items: center; gap: 13px; padding: 11px 12px; border: 1px solid #E0D4C1; background: #FFFFFF; border-radius: 8px; margin-bottom: 8px; }
  .check { width: 18px; height: 18px; border-radius: 5px; border: 1.5px solid #8B6F47; background: #8B6F47; color: #FFFFFF; display: inline-flex; align-items: center; justify-content: center; font-size: 11px; flex: none; }
  .tool-main { flex: 1; min-width: 0; }
  .tool-row { display: flex; align-items: center; gap: 9px; }
  .tool-name { font-size: 13px; color: #2A2622; }
  .kind { font-size: 9.5px; border-radius: 4px; padding: 1px 6px; text-transform: uppercase; letter-spacing: 0.04em; }
  .kind-read { color: #4A714A; background: #E9F0E7; }
  .kind-write { color: #976A28; background: #F7EEDD; }
  .kind-delete { color: #8A3E29; background: #F7ECE8; }
  .tool-desc { font-size: 12px; color: #7A7266; margin-top: 3px; }
  .scopes { padding: 6px 34px 20px; display: flex; align-items: center; gap: 8px; font-size: 11px; color: #9A938A; }
  .scopes .on { color: #8B6F47; }
  .actions { display: flex; gap: 12px; padding: 18px 34px; border-top: 1px solid #EDE6DB; background: #F6F1E8; }
  button { font-family: inherit; cursor: pointer; }
  .deny { flex: none; font-size: 13.5px; font-weight: 500; color: #7A7266; background: transparent; border: 1px solid #E0D9CD; border-radius: 7px; padding: 10px 18px; }
  .deny:hover { background: #EEE7DC; }
  .approve { flex: 1; font-size: 13.5px; font-weight: 500; color: #FFFFFF; background: #8B6F47; border: 1px solid #8B6F47; border-radius: 7px; padding: 10px 0; }
  .approve:hover { background: #7A6039; border-color: #7A6039; }
</style>
</head>
<body>
<form class="card" method="post" action="/authorize">
  <input type="hidden" name="client_id" value="{{.ClientID}}">
  <input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
  <input type="hidden" name="state" value="{{.State}}">
  <input type="hidden" name="scope" value="{{.Scope}}">
  <input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
  <input type="hidden" name="code_challenge_method" value="{{.CodeChallengeMethod}}">
  <input type="hidden" name="resource" value="{{.Resource}}">

  <div class="header">
    <div class="marks">
      <div class="vmark">
        <div class="tile"></div><div class="foldbg"></div><div class="fold"></div>
        <div class="v serif">v</div>
      </div>
      <span class="arrow mono">&#8592;&#8594;</span>
      <div class="agent serif">&#10035;</div>
    </div>
    <h1 class="serif">Authorize <em>{{.ClientName}}</em> to connect</h1>
    <div class="signed">Vault at <span class="host mono">{{.Host}}</span></div>
  </div>

  <div class="tools">
    <div class="tools-head">
      <div class="tools-title">Tools for this session</div>
      <div class="tools-count mono">{{.ToolCount}} of {{.ToolCount}}</div>
    </div>
    <div class="tools-sub">This connection may call the following vault tools. Deny if you did not initiate it.</div>
    {{range .Tools}}
    <div class="tool">
      <span class="check">&#10003;</span>
      <div class="tool-main">
        <div class="tool-row">
          <span class="tool-name mono">{{.Name}}</span>
          <span class="kind kind-{{.Kind}} mono">{{.Kind}}</span>
        </div>
        <div class="tool-desc">{{.Desc}}</div>
      </div>
    </div>
    {{end}}
  </div>

  <div class="scopes mono">
    <span>scope:</span>
    <span class="on">vault.read</span>
    <span class="on">vault.write</span>
    <span class="on">vault.delete</span>
  </div>

  <div class="actions">
    <button class="deny" type="submit" name="decision" value="deny">Deny</button>
    <button class="approve" type="submit" name="decision" value="approve">Authorize {{.ToolCount}} tools &middot; start session</button>
  </div>
</form>
</body>
</html>
`))

type consentData struct {
	ClientID            string
	ClientName          string
	RedirectURI         string
	State               string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
	Host                string
	Tools               []consentTool
	ToolCount           int
}

// renderConsent serves the authorize consent screen (design artboard 1b,
// static variant: all tools listed and granted; per-tool selection is a
// possible follow-up, PHY-112 marks it nice-to-have).
func (p *Provider) renderConsent(w http.ResponseWriter, params authorizeParams) {
	host := p.issuer
	if u, err := url.Parse(p.issuer); err == nil && u.Host != "" {
		host = u.Host
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = consentTmpl.Execute(w, consentData{
		ClientID:            params.clientID,
		ClientName:          "Claude",
		RedirectURI:         params.redirectURI,
		State:               params.state,
		Scope:               params.scope,
		CodeChallenge:       params.codeChallenge,
		CodeChallengeMethod: params.codeChallengeMethod,
		Resource:            params.resource,
		Host:                host,
		Tools:               consentTools,
		ToolCount:           len(consentTools),
	})
}
