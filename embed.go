// Package archive holds the embedded web frontend. It lives at the module
// root because go:embed cannot reach outside a package directory and the
// Vite build lands in frontend/dist; cmd/server serves it as the SPA.
package archive

import "embed"

// FrontendFS is the built web app (frontend/dist). The committed
// frontend/dist/index.html placeholder keeps `go build` working without a
// frontend build; `npm run build` overwrites it locally and in the image.
//
//go:embed all:frontend/dist
var FrontendFS embed.FS
