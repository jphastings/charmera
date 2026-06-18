// Package cli implements the charmera command-line interface.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jphastings/charmera/internal/config"
	"github.com/jphastings/charmera/internal/launchagent"
	"github.com/jphastings/charmera/internal/photos"
	"github.com/jphastings/charmera/internal/pipeline"
	"github.com/jphastings/charmera/internal/scan"
	"github.com/jphastings/charmera/internal/video"
	"github.com/jphastings/charmera/internal/volume"
)

const usage = `charmera — import Kodak Charmera photos & videos into macOS Photos,
fixing their broken EXIF and converting AVI to MP4 along the way.

Usage:
  charmera [run] [flags]    Detect the camera and process its media (default)
  charmera install [flags]  Auto-run when the camera is plugged in (LaunchAgent)
  charmera uninstall        Remove the LaunchAgent
  charmera version          Print the version
  charmera help             Show this help

Run flags:
  --volume NAME    Pin to a specific volume name (default: auto-detect)
  --album NAME     Photos album to import into (default "Kodak Charmera")
  --out DIR        Write fixed/converted files to DIR instead of importing
  --dry-run        Show what would happen without changing anything
  --auto           Non-interactive; exit quietly if no camera is mounted
  --no-auto-rotate Disable content-based orientation detection (on when
                   onnxruntime + the model are available)
  --no-unmount     Leave the camera mounted when finished (it unmounts by default)

Install flags:
  --volume NAME   Pin the watch to a specific volume (default: watch /Volumes)
`

// Build information, set from main at build time (injected by GoReleaser).
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Main is the program entry point. It returns a process exit code.
func Main(args []string) int {
	cmd := "run"
	rest := args
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, rest = args[0], args[1:]
	}

	switch cmd {
	case "run":
		return runCmd(rest)
	case "install":
		return installCmd(rest)
	case "uninstall":
		return uninstallCmd()
	case "version", "--version":
		fmt.Println(versionString())
		return 0
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

func versionString() string {
	s := "charmera " + Version
	if Commit != "" {
		s += " (" + Commit + ")"
	}
	if Date != "" {
		s += ", built " + Date
	}
	return s
}

func runCmd(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		volumeName   = fs.String("volume", "", "pin to a specific volume name")
		album        = fs.String("album", "", "Photos album to import into")
		out          = fs.String("out", "", "write fixed files to DIR instead of importing")
		dryRun       = fs.Bool("dry-run", false, "preview without changing anything")
		auto         = fs.Bool("auto", false, "non-interactive; exit quietly if no camera")
		noAutoRotate = fs.Bool("no-auto-rotate", false, "disable content-based orientation detection")
		noUnmount    = fs.Bool("no-unmount", false, "don't unmount the camera when finished")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg := config.Default()
	if *volumeName != "" {
		cfg.VolumeName = *volumeName
	}
	if *album != "" {
		cfg.Album = *album
	}

	volumePath, ok := scan.FindCamera(cfg)
	if !ok {
		if *auto {
			return 0 // launchd may have fired on some other mount/unmount
		}
		fmt.Fprintf(os.Stderr, "No Kodak Charmera detected under %s.\nPlug in the camera, or pass --volume NAME if the card has been renamed.\n", cfg.VolumesDir)
		return 1
	}

	opts := pipeline.Options{
		OutDir:        *out,
		DryRun:        *dryRun,
		Observer:      textObserver(os.Stdout),
		MinConfidence: cfg.OrientationMinConfidence,
	}
	if !*noAutoRotate && !*dryRun {
		if detector, closeFn := newDetector(cfg); detector != nil {
			opts.Detector = detector
			defer closeFn()
		}
	}

	p := pipeline.New(cfg, volumePath, video.New(cfg), photos.New(cfg.Album), opts)

	sum, err := p.Run(context.Background())
	printSummary(os.Stdout, sum, *dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	// Unmount the camera once we're done reading from it (real runs only).
	if !*dryRun && !*noUnmount {
		if uerr := volume.Unmount(volumePath); uerr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not unmount %s: %v\n", volumePath, uerr)
		} else {
			fmt.Printf("Unmounted %s\n", volumePath)
		}
	}

	if sum.Failed > 0 {
		return 1
	}
	return 0
}

func installCmd(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	volume := fs.String("volume", "", "camera volume name to watch")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg := config.Default()
	if *volume != "" {
		cfg.VolumeName = *volume
	}

	// Watch /Volumes (not a specific name) so the agent fires on any mount; a
	// pinned --volume narrows the watch to that path.
	watchPath := cfg.VolumesDir
	if cfg.VolumeName != "" {
		watchPath = cfg.VolumePath()
	}

	path, err := launchagent.Install(watchPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "install failed: %v\n", err)
		return 1
	}
	fmt.Printf("LaunchAgent installed: %s\n", path)
	fmt.Printf("charmera will run automatically when the camera is plugged in (watching %s).\n", watchPath)
	return 0
}

func uninstallCmd() int {
	path, err := launchagent.Uninstall()
	if err == os.ErrNotExist {
		fmt.Println("LaunchAgent not installed.")
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "uninstall failed: %v\n", err)
		return 1
	}
	fmt.Printf("LaunchAgent removed: %s\n", path)
	return 0
}

// textObserver prints human-readable progress lines.
func textObserver(w io.Writer) pipeline.Observer {
	return func(e pipeline.Event) {
		switch e.Phase {
		case "scan":
			fmt.Fprintf(w, "Scanning %s …\n", e.Detail)
		case "skip":
			fmt.Fprintf(w, "  skip     %s (%s)\n", e.Name, e.Detail)
		case "fix":
			fmt.Fprintf(w, "  fix      %s  %s\n", e.Name, e.Detail)
		case "convert":
			fmt.Fprintf(w, "  convert  %s\n", e.Name)
		case "import":
			if e.Name == "" {
				fmt.Fprintf(w, "Importing %s …\n", e.Detail)
			} else {
				fmt.Fprintf(w, "  imported %s\n", e.Name)
			}
		case "warn":
			fmt.Fprintf(w, "  warning: %s%s\n", e.Name, prefixDetail(e))
		case "error":
			name := e.Name
			if name == "" {
				name = e.Detail
			}
			fmt.Fprintf(w, "  error    %s: %v\n", name, e.Err)
		}
	}
}

func prefixDetail(e pipeline.Event) string {
	if e.Name != "" && e.Detail != "" {
		return ": " + e.Detail
	}
	return e.Detail
}

func printSummary(w io.Writer, s pipeline.Summary, dryRun bool) {
	verb := "Done"
	if dryRun {
		verb = "Dry run"
	}
	fmt.Fprintf(w, "%s: %d scanned, %d skipped, %d photo(s) fixed, %d video(s) converted, %d imported, %d failed\n",
		verb, s.Scanned, s.Skipped, s.PhotosFixed, s.VideosConverted, s.Imported, s.Failed)
}
