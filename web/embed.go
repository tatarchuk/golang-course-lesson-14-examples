// Package web embeds the browser pages of the examples: a demo login, the chat of
// slide 40 and the live dashboard of slide 41 over WebSocket and over SSE (slide 30).
// Plain HTML and one script, no framework (slide 41).
package web

import "embed"

// FS holds the pages, the script and the stylesheet.
//
//go:embed *.html *.js *.css
var FS embed.FS
