// Package portalweb embeds the built portal frontend (web/portal/dist).
package portalweb

import "embed"

//go:embed all:dist
var Dist embed.FS
