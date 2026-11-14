// Package migrations holds the schema, applied in file-name order.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
