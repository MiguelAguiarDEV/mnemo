package dashboard

import "embed"

// StaticFS embeds the static/ directory (htmx.min.js, pico.min.css, styles.css)
// into the binary so no external files are needed at runtime.
//
//go:embed static
var StaticFS embed.FS
