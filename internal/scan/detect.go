package scan

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jphastings/charmera/internal/config"
)

const (
	// spiDirName is a sibling of DCIM that Generalplus cameras create.
	spiDirName = "SPIDCIM"
	// gpEncoderSignature is the JPEG comment Generalplus chipsets embed.
	gpEncoderSignature = "GPEncoder"
	// signatureSampleSize is how much of a JPEG header to scan for the marker.
	signatureSampleSize = 4096
	// maxSignatureFiles caps how many JPEGs we sniff per volume.
	maxSignatureFiles = 5
)

// FindCamera locates a connected Charmera and returns its mount path.
//
// When cfg.VolumeName is set it is used directly (a manual override). Otherwise
// every volume under cfg.VolumesDir is checked by content signature, so the
// camera is found no matter what the card has been renamed to.
func FindCamera(cfg config.Config) (string, bool) {
	if cfg.VolumeName != "" {
		p := cfg.VolumePath()
		if isDir(filepath.Join(p, cfg.DCIMSubdir)) {
			return p, true
		}
		return "", false
	}

	entries, err := os.ReadDir(cfg.VolumesDir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		p := filepath.Join(cfg.VolumesDir, e.Name())
		if IsCharmera(p, cfg.DCIMSubdir) {
			return p, true
		}
	}
	return "", false
}

// IsCharmera reports whether the volume at volPath is a Kodak Charmera, judged
// by structure and content rather than the (user-renameable) volume name: it
// must have a DCIM directory plus either a sibling SPIDCIM directory or a JPEG
// bearing the Generalplus "GPEncoder" comment.
func IsCharmera(volPath, dcimSubdir string) bool {
	if !isDir(filepath.Join(volPath, dcimSubdir)) {
		return false
	}
	if isDir(filepath.Join(volPath, spiDirName)) {
		return true
	}
	return hasGPEncoderSignature(filepath.Join(volPath, dcimSubdir))
}

func hasGPEncoderSignature(dcim string) bool {
	entries, err := os.ReadDir(dcim)
	if err != nil {
		return false
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), "._") {
			continue
		}
		if ext := strings.ToLower(filepath.Ext(e.Name())); ext != ".jpg" && ext != ".jpeg" {
			continue
		}
		if headContains(filepath.Join(dcim, e.Name()), gpEncoderSignature) {
			return true
		}
		if checked++; checked >= maxSignatureFiles {
			break
		}
	}
	return false
}

func headContains(path, needle string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, signatureSampleSize)
	n, _ := io.ReadFull(f, buf) // short reads are fine
	return bytes.Contains(buf[:n], []byte(needle))
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
