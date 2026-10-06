// Package lotus holds the assets compiled into the binary. The drive frontend
// is embedded so a fresh server works with nothing but the executable; a
// web/ folder in the data directory still overrides it, which is how the 0x0
// deployment keeps its own frontend.
package lotus

import "embed"

//go:embed all:frontends/drive
var DriveFS embed.FS

// DriveRoot is the path inside DriveFS where the frontend lives.
const DriveRoot = "frontends/drive"
