// Package adminweb embeds the built admin frontend (web/admin/dist).
package adminweb

import "embed"

//go:embed all:dist
var Dist embed.FS
