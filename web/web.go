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

// I18n holds the UI string catalogs, i18n/<locale>.toml.
//
//go:embed i18n/*.toml
var I18n embed.FS
