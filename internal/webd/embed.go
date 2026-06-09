//go:build dist

package webd

import "embed"

//go:embed all:dist
var staticFiles embed.FS
