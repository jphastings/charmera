package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jphastings/charmera/internal/scan"
)

const (
	// destNameLayout encodes the capture time into the output filename.
	destNameLayout = "20060102_150405"
	// hashKeyLen is how many hex chars of the content hash are embedded in the
	// filename. This token is what lets Photos itself act as the dedup record.
	hashKeyLen = 16
)

// destName builds the output filename for a camera file, embedding a content
// hash so the imported file's name is the dedup key. Photos keep their
// extension; videos become .mp4 after conversion.
func destName(f scan.File) string {
	prefix := "IMG"
	ext := strings.ToLower(filepath.Ext(f.Name))
	if f.Kind == scan.Video {
		prefix = "VID"
		ext = ".mp4"
	}
	return fmt.Sprintf("%s_%s_%s%s", prefix, f.ModTime.Format(destNameLayout), hashKey(f.Hash), ext)
}

// hashKey is the filename token derived from a content hash.
func hashKey(hash string) string {
	if len(hash) < hashKeyLen {
		return hash
	}
	return hash[:hashKeyLen]
}

// keyFromName extracts the embedded hash token from a filename produced by
// destName, returning false for names that don't match (e.g. files a user added
// to the album by hand).
func keyFromName(name string) (string, bool) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	i := strings.LastIndex(base, "_")
	if i < 0 {
		return "", false
	}
	tok := base[i+1:]
	if len(tok) != hashKeyLen || !isHex(tok) {
		return "", false
	}
	return tok, true
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// uniquePath returns dir/name, or dir/name_N if that already exists.
func uniquePath(dir, name string) string {
	candidate := filepath.Join(dir, name)
	if !exists(candidate) {
		return candidate
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s_%d%s", stem, i, ext))
		if !exists(candidate) {
			return candidate
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
