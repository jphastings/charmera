// Package scan finds and classifies the media files on a mounted Charmera,
// and computes the content hashes used for deduplication. It never modifies the
// camera; it only reads.
package scan

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jphastings/charmera/internal/config"
)

// Kind distinguishes photos from videos.
type Kind string

const (
	Photo Kind = "photo"
	Video Kind = "video"
)

// File is one media file discovered on the camera.
type File struct {
	SourcePath string
	Name       string
	Kind       Kind
	Size       int64
	ModTime    time.Time
	Hash       string // SHA-256 of the file contents, hex-encoded
}

// Scan lists supported media in the camera's DCIM directory, classifying each
// file and computing its content hash. volumePath is the camera's mount path
// (as returned by FindCamera). Results are sorted by name for stable ordering
// and previews.
func Scan(cfg config.Config, volumePath string) ([]File, error) {
	dcim := filepath.Join(volumePath, cfg.DCIMSubdir)
	entries, err := os.ReadDir(dcim)
	if err != nil {
		return nil, err
	}

	photoExt := extSet(cfg.PhotoExts)
	videoExt := extSet(cfg.VideoExts)

	var files []File
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if strings.HasPrefix(name, "._") {
			continue // macOS AppleDouble metadata
		}

		ext := strings.ToLower(filepath.Ext(name))
		var kind Kind
		switch {
		case photoExt[ext]:
			kind = Photo
		case videoExt[ext]:
			kind = Video
		default:
			continue
		}

		path := filepath.Join(dcim, name)
		info, err := de.Info()
		if err != nil {
			return nil, err
		}
		hash, err := hashFile(path)
		if err != nil {
			return nil, err
		}

		files = append(files, File{
			SourcePath: path,
			Name:       name,
			Kind:       kind,
			Size:       info.Size(),
			ModTime:    info.ModTime(),
			Hash:       hash,
		})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, nil
}

func extSet(exts []string) map[string]bool {
	m := make(map[string]bool, len(exts))
	for _, e := range exts {
		m[strings.ToLower(e)] = true
	}
	return m
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
