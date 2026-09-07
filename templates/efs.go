package templates

import (
	"embed"
)

// Files embeds HTML templates and static assets for runtime rendering.
//
//go:embed "html" "static"
var Files embed.FS
