package mcpserver

import "encoding/base64"

// vellumIconSVG is the folded-leaf mark (from design/Vellum-Logo.dc.html and
// web/public/favicon.svg). It is advertised as a self-contained data URI in
// the MCP server info so clients (Inspector, claude.ai, …) show an icon
// without fetching anything from the server origin.
const vellumIconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><path d="M6 0 H21 L32 11 V26 A6 6 0 0 1 26 32 H6 A6 6 0 0 1 0 26 V6 A6 6 0 0 1 6 0 Z" fill="#8B6F47"/><path d="M21 0 L32 11 L21 11 Z" fill="#6F5836"/><text x="16" y="25" text-anchor="middle" font-family="Georgia, 'Times New Roman', serif" font-size="20" font-weight="500" fill="#FBF7F0">v</text></svg>`

// vellumIconDataURI returns the icon as a base64 data URI.
func vellumIconDataURI() string {
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(vellumIconSVG))
}
