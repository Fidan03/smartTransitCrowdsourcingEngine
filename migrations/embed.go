// Package migrations embeds the versioned SQL schema files so the binary can
// apply them without needing the files on disk at runtime.
package migrations

import "embed"

// FS holds every *.sql migration, applied in lexical filename order.
//
//go:embed *.sql
var FS embed.FS
