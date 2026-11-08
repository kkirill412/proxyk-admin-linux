package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

func Files() (fs.FS, error) { return fs.Sub(assets, "dist") }
