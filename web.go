package offsitecalculator

import (
	"embed"
	"io/fs"
)

//go:embed web
var embedded embed.FS

// Web is the static front end, served by the Go binary for self-hosting and
// published to <slug>.app.nz as static files when deployed there.
func Web() fs.FS {
	sub, err := fs.Sub(embedded, "web")
	if err != nil {
		panic(err)
	}
	return sub
}
