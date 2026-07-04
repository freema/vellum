package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/freema/vellum/internal/activity"
)

// mcpRecord wraps the /mcp handler to populate the activity recorder: every
// authorized request touches a session (so the Connections panel is live) and
// every tools/call is logged. It reads and restores the request body, so the
// downstream MCP handler is unaffected.
func mcpRecord(rec *activity.Recorder, next http.Handler) http.Handler {
	if rec == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.Body != nil {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err == nil {
				r.Body = io.NopCloser(bytes.NewReader(body))
				key := sessionKey(r)
				name, kind := clientIdentity(r.UserAgent())
				method, tool, target := parseRPC(body)
				if method == "tools/call" && tool != "" {
					rec.Touch(key, name, kind, tool)
					rec.Record(activity.Event{
						Source: "mcp", Actor: name, Kind: toolKind(tool),
						Target: target, Detail: tool,
					})
				} else {
					rec.Touch(key, name, kind, "")
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// sessionKey derives a stable, non-secret handle for a client from its bearer
// token (hashed) or, without auth, its remote address.
func sessionKey(r *http.Request) string {
	if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && tok != "" {
		sum := sha256.Sum256([]byte(tok))
		return "sk-" + hex.EncodeToString(sum[:])[:8]
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		host = "local"
	}
	sum := sha256.Sum256([]byte("ip:" + host))
	return "sk-" + hex.EncodeToString(sum[:])[:8]
}

// clientIdentity best-effort names a client from its User-Agent.
func clientIdentity(ua string) (name, kind string) {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "claude-code") || strings.Contains(l, "claude code"):
		return "Claude Code", "CLI · stdio→http"
	case strings.Contains(l, "claude") && strings.Contains(l, "desktop"):
		return "Claude Desktop", "Desktop app"
	case strings.Contains(l, "claude"):
		return "claude.ai", "Web · connector"
	case strings.Contains(l, "cursor"):
		return "Cursor", "Editor"
	case strings.Contains(l, "chatgpt") || strings.Contains(l, "openai"):
		return "ChatGPT", "Web · connector"
	case strings.Contains(l, "python"):
		return "Python client", "SDK"
	case strings.Contains(l, "node"):
		return "Node client", "SDK"
	case ua == "":
		return "MCP client", "Streamable HTTP"
	default:
		if len(ua) > 24 {
			ua = ua[:24]
		}
		return ua, "Streamable HTTP"
	}
}

// parseRPC pulls the method, tool name and a target from a JSON-RPC body
// (single object or the first element of a batch). Best-effort; unknown
// shapes return empty strings.
func parseRPC(body []byte) (method, tool, target string) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var arr []json.RawMessage
		if json.Unmarshal(trimmed, &arr) == nil && len(arr) > 0 {
			trimmed = arr[0]
		}
	}
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Name      string `json:"name"`
			Arguments struct {
				Path  string `json:"path"`
				From  string `json:"from"`
				Query string `json:"query"`
			} `json:"arguments"`
		} `json:"params"`
	}
	if json.Unmarshal(trimmed, &msg) != nil {
		return "", "", ""
	}
	target = msg.Params.Arguments.Path
	if target == "" {
		target = msg.Params.Arguments.From
	}
	if target == "" && msg.Params.Arguments.Query != "" {
		target = "“" + msg.Params.Arguments.Query + "”"
	}
	return msg.Method, msg.Params.Name, target
}

var toolKinds = map[string]string{
	"read_note": "read", "list_notes": "read", "list_tags": "read",
	"get_backlinks": "read", "list_tasks": "read",
	"search_notes": "search",
	"write_note":   "write", "patch_note": "write", "append_to_note": "write", "prepend_to_note": "write",
	"move_note": "move", "delete_note": "delete",
	"add_tags": "tag", "remove_tags": "tag", "set_status": "organize",
}

func toolKind(tool string) string {
	if k, ok := toolKinds[tool]; ok {
		return k
	}
	return "read"
}

var kindVerb = map[string]string{
	"read": "read", "write": "wrote", "search": "searched", "move": "moved",
	"delete": "deleted", "tag": "tagged", "link": "linked",
	"organize": "organized", "archive": "archived", "summary": "summarized",
	"error": "hit an error in",
}

func kindToVerb(kind string) string {
	if v, ok := kindVerb[kind]; ok {
		return v
	}
	return kind
}

// ---- endpoint handlers ----

func (a *API) handleConnections(w http.ResponseWriter, r *http.Request) {
	type conn struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Mono     string `json:"mono"`
		Status   string `json:"status"`
		Since    string `json:"since"`
		LastTool string `json:"lastTool,omitempty"`
		LastAgo  string `json:"lastAgo"`
		Calls    int    `json:"calls"`
	}
	now := time.Now()
	sessions := a.Activity.Sessions()
	conns := make([]conn, 0, len(sessions))
	active := 0
	for _, s := range sessions {
		if s.Status == "active" {
			active++
		}
		conns = append(conns, conn{
			ID: s.ID, Name: s.Name, Kind: s.Kind, Mono: monogram(s.Name),
			Status: s.Status, Since: durSince(now.Sub(s.FirstSeen)),
			LastTool: s.LastTool, LastAgo: reltime(now.Sub(s.LastSeen)), Calls: s.Calls,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"endpoint":    a.Endpoint,
		"activeCount": active,
		"totalCalls":  a.Activity.TotalCalls(),
		"connections": conns,
	})
}

func (a *API) handleRevoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !a.Activity.Revoke(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such session"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"revoked": id})
}

func (a *API) handleActivity(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	filter := r.URL.Query().Get("filter")
	all := a.Activity.Events("all")

	type ev struct {
		ID      string `json:"id"`
		Source  string `json:"source"`
		Actor   string `json:"actor"`
		Kind    string `json:"kind"`
		Verb    string `json:"verb"`
		Target  string `json:"target"`
		Detail  string `json:"detail"`
		Pending bool   `json:"pending,omitempty"`
		Time    string `json:"time"`
		// error fields (only for kind == "error")
		IsError bool   `json:"isError,omitempty"`
		Level   string `json:"level,omitempty"`
		Tool    string `json:"tool,omitempty"`
		Status  int    `json:"status,omitempty"`
		Session string `json:"session,omitempty"`
	}

	errorCount := 0
	out := []ev{}
	lastRun := "never"
	for _, e := range all {
		isErr := e.Kind == "error"
		if isErr {
			errorCount++
		}
		if e.Source == "curator" && lastRun == "never" {
			lastRun = reltime(now.Sub(e.At))
		}
		switch filter {
		case "", "all":
		case "errors":
			if !isErr {
				continue
			}
		default: // mcp | curator | user
			if e.Source != filter {
				continue
			}
		}
		item := ev{
			ID: e.ID, Source: e.Source, Actor: e.Actor, Kind: e.Kind, Verb: kindToVerb(e.Kind),
			Target: a.prettyTarget(e.Target), Detail: e.Detail, Pending: e.Pending,
			Time: reltime(now.Sub(e.At)),
		}
		if isErr {
			item.IsError = true
			item.Level = "error"
			item.Tool = e.Target
			item.Status = http.StatusInternalServerError
			item.Session = e.Actor
		}
		out = append(out, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"curator": map[string]any{
			"enabled":  a.Curator,
			"changes":  a.Activity.CuratorChangesSince(24 * time.Hour),
			"watching": a.Index.Len(),
			"lastRun":  lastRun,
		},
		"events":     out,
		"errorCount": errorCount,
	})
}

// prettyTarget resolves a vault path to its note title where possible.
func (a *API) prettyTarget(target string) string {
	if target == "" {
		return target
	}
	if e, ok := a.Index.Get(target); ok && e.Title != "" {
		return e.Title
	}
	return target
}

func (a *API) handleNotifications(w http.ResponseWriter, r *http.Request) {
	type notif struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Title string `json:"title"`
		Body  string `json:"body"`
		Time  string `json:"time"`
		Read  bool   `json:"read"`
	}
	var items []notif
	now := time.Now()

	if a.Curator {
		if untagged := a.Index.FindUntagged(); len(untagged) > 0 {
			items = append(items, notif{
				ID: "nt-untagged", Kind: "curator",
				Title: plural(len(untagged), "note needs tags", "notes need tags"),
				Body:  "The curator can suggest tags from content.", Time: "now",
			})
		}
		if stale := a.Index.FindInboxStale(a.Structure.Inbox, 14*24*time.Hour); len(stale) > 0 {
			items = append(items, notif{
				ID: "nt-stale", Kind: "curator",
				Title: plural(len(stale), "inbox note is getting stale", "inbox notes are getting stale"),
				Body:  "Sitting in the inbox for over two weeks.", Time: "now",
			})
		}
	}
	if inprog := a.Index.ListTasks("in-progress", ""); len(inprog) > 0 {
		items = append(items, notif{
			ID: "nt-tasks", Kind: "task",
			Title: plural(len(inprog), "task in progress", "tasks in progress"),
			Body:  "Open work across the vault.", Time: "now",
		})
	}
	for _, s := range a.Activity.Sessions() {
		if s.Status == "active" {
			items = append(items, notif{
				ID: "nt-conn-" + s.ID, Kind: "mcp",
				Title: s.Name + " connected", Body: "Active MCP session",
				Time: reltime(now.Sub(s.FirstSeen)),
			})
		}
	}

	unread := 0
	for _, n := range items {
		if !n.Read {
			unread++
		}
	}
	if items == nil {
		items = []notif{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": items, "unread": unread})
}

func (a *API) handleCuratorRun(w http.ResponseWriter, r *http.Request) {
	if !a.Curator {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "changes": 0})
		return
	}
	changes := 0
	record := func(kind, target, detail string) {
		a.Activity.Record(activity.Event{
			Source: "curator", Actor: "Curator", Kind: kind,
			Target: target, Detail: detail, Pending: true,
		})
		changes++
	}
	untagged := a.Index.FindUntagged()
	if len(untagged) > 0 {
		n := untagged[0]
		record("tag", n.Path, "could infer tags from content")
	}
	if orphans := a.Index.FindOrphans(); len(orphans) > 0 {
		record("link", orphans[0].Path, "no links in or out — suggest connections")
	}
	if stale := a.Index.FindInboxStale(a.Structure.Inbox, 14*24*time.Hour); len(stale) > 0 {
		record("organize", stale[0].Path, "stale in the inbox — suggest a folder")
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "changes": changes})
}

func (a *API) handleListFolders(w http.ResponseWriter, r *http.Request) {
	dirs, err := a.Vault.ListDirs()
	if err != nil {
		apiError(w, err)
		return
	}
	if dirs == nil {
		dirs = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": dirs})
}

func (a *API) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := a.Vault.CreateDir(req.Path); err != nil {
		apiError(w, err)
		return
	}
	a.recordUser("organize", strings.Trim(req.Path, "/"), "created folder")
	writeJSON(w, http.StatusOK, map[string]string{"created": strings.Trim(req.Path, "/")})
}

// recordUser logs an action taken through the SPA (source "user").
func (a *API) recordUser(kind, target, detail string) {
	if a.Activity == nil {
		return
	}
	a.Activity.Record(activity.Event{Source: "user", Actor: "You", Kind: kind, Target: target, Detail: detail})
}

// ---- small formatters ----

func monogram(name string) string {
	fields := strings.FieldsFunc(name, func(r rune) bool { return r == ' ' || r == '.' || r == '-' })
	switch {
	case len(fields) >= 2:
		return strings.ToUpper(fields[0][:1] + fields[1][:1])
	case len(fields) == 1 && len(fields[0]) >= 2:
		return strings.ToUpper(fields[0][:2])
	case len(fields) == 1:
		return strings.ToUpper(fields[0])
	default:
		return "··"
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// reltime renders a short "ago" label: 4s, 12m, 2h, 5d, 1w, 1y.
func reltime(d time.Duration) string {
	switch {
	case d < time.Minute:
		s := int(d.Seconds())
		if s < 1 {
			s = 1
		}
		return itoa(s) + "s"
	case d < time.Hour:
		return itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return itoa(int(d.Hours())) + "h"
	case d < 7*24*time.Hour:
		return itoa(int(d.Hours()/24)) + "d"
	case d < 365*24*time.Hour:
		return itoa(int(d.Hours()/(24*7))) + "w"
	default:
		return itoa(int(d.Hours()/(24*365))) + "y"
	}
}

// durSince renders an uptime like "2h 14m", "18m" or "8s".
func durSince(d time.Duration) string {
	if d < time.Minute {
		return itoa(int(d.Seconds())) + "s"
	}
	if d < time.Hour {
		return itoa(int(d.Minutes())) + "m"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	mm := itoa(m)
	if m < 10 {
		mm = "0" + mm
	}
	return itoa(h) + "h " + mm + "m"
}
