// Package web embeds the HTML templates and static assets.
package web

import "embed"

// Templates holds html/template sources.
//
//go:embed templates/*.html
var Templates embed.FS

// Static holds files served under /static/.
//
//go:embed static
var Static embed.FS
