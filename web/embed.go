// Package web embeds the static frontend served by the Go backend.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var static embed.FS

// FS returns the static site rooted at web/static.
func FS() fs.FS {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		panic(err) // the embed directive guarantees "static" exists
	}
	return sub
}
