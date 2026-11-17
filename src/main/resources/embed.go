// Package resources embeds the bundled map data into the Go binary. The files stay where the Kotlin
// build reads them, so both implementations use the same single copy.
package resources

import "embed"

//go:embed terrain.json transport-network.json bridge-profiles.json road-access.json
var FS embed.FS
