// Command charmera imports Kodak Charmera photos and videos into the macOS
// Photos app, repairing their broken EXIF (pure Go) and converting AVI to MP4
// (via ffmpeg) along the way.
package main

import (
	"os"

	"github.com/jphastings/charmera/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
