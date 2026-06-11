//go:build ui

package ui

import "embed"

//go:embed all:dist
var distFS embed.FS

func init() {
	available = true
	assets = distFS
}
