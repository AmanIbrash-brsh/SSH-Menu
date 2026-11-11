// ssh-menu is a keyboard-driven SSH hub on top of ~/.ssh/config.
package main

import (
	"os"

	"github.com/AmanIbrash-brsh/SSH-Menu/internal/app"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() { os.Exit(app.Main(version, os.Args[1:])) }
