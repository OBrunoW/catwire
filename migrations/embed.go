package migrations

import "embed"

//go:embed V*__*.sql
var Files embed.FS
