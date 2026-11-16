package server

import (
	"embed"
	"io/fs"
)

// Web holds the built phone page (phone/dist copied into web/ at build time).
//
//go:embed all:web
var Web embed.FS

// WebFS returns the embedded phone page rooted at web/.
func WebFS() fs.FS {
	sub, err := fs.Sub(Web, "web")
	if err != nil {
		panic(err) // "web" is a constant, valid path
	}
	return sub
}
