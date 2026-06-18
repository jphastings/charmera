// Command charmera imports Kodak Charmera photos and videos into the macOS
// Photos app, repairing their broken EXIF (pure Go) and converting AVI to MP4
// (via ffmpeg) along the way.
package main

import (
	"os"

	"github.com/jphastings/charmera/internal/cli"
)

// Build information, overridden at release time via -ldflags by GoReleaser.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	cli.Version, cli.Commit, cli.Date = version, commit, date
	os.Exit(cli.Main(os.Args[1:]))
}
