package main

import (
	"embed"
)

//go:generate npm --prefix frontend install
//go:generate npm --prefix frontend run build

//go:embed all:frontend/dist
var frontendDist embed.FS
