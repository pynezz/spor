// Package web holds the embedded UI: templates, static assets and strings.
package web

import "embed"

//go:embed templates/*.html static strings/*.json sw.js
var FS embed.FS
