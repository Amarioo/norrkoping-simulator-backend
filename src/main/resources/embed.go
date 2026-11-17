// Package resources embeds the bundled map data into the Go binary. The files stay in the
// directory the UI repository's importers write to.
package resources

import "embed"

//go:embed terrain.json transport-network.json bridge-profiles.json road-access.json
var FS embed.FS
