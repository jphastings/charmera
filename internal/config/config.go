// Package config holds the runtime configuration for the charmera tool and its
// defaults. Defaults target a Kodak Charmera mounted on macOS.
package config

import (
	"os"
	"path/filepath"
)

// Config controls how the camera is found and how its media is processed.
type Config struct {
	// VolumesDir is the directory mounted volumes appear under (macOS: /Volumes).
	// Overridable mainly so tests can point at a temporary directory.
	VolumesDir string
	// VolumeName, when set, pins the camera to a specific volume under
	// VolumesDir (a manual override). When empty, the camera is auto-detected by
	// content signature, so a renamed card is still found.
	VolumeName string
	// DCIMSubdir is the directory on the camera that holds the media files.
	DCIMSubdir string
	// StateDir holds the tool's temporary staging area used while importing.
	// Fixed files are imported into Photos and not otherwise kept on disk; dedup
	// state lives in Photos itself, not here.
	StateDir string

	// PhotoExts and VideoExts are the lower-cased extensions to process.
	PhotoExts []string
	VideoExts []string

	// Album is the Photos album imported media is added to (created if absent).
	Album string

	// OrientationMinConfidence is the softmax probability the orientation model
	// must reach before charmera rewrites a photo's Orientation tag. 0.5 means a
	// rotation is applied only when it wins an outright majority over "already
	// upright" — high enough to avoid flipping correct photos, low enough to act
	// on the model's (less confident, but correct) calls on lo-fi camera images.
	OrientationMinConfidence float64

	// ffmpeg transcode settings, used when an AVI is converted to MP4.
	FFmpegVideoCodec   string
	FFmpegAudioCodec   string
	FFmpegCRF          int
	FFmpegPreset       string
	FFmpegAudioBitrate string
}

// Default returns the standard configuration. StateDir defaults to
// ~/Library/Application Support/charmera.
func Default() Config {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return Config{
		VolumesDir:         "/Volumes",
		VolumeName:         "", // empty = auto-detect by signature
		DCIMSubdir:         "DCIM",
		StateDir:           filepath.Join(home, "Library", "Application Support", "charmera"),
		PhotoExts:          []string{".jpg", ".jpeg"},
		VideoExts:          []string{".avi"},
		Album:              "Kodak Charmera",
		FFmpegVideoCodec:   "libx264",
		FFmpegAudioCodec:   "aac",
		FFmpegCRF:          26,
		FFmpegPreset:       "medium",
		FFmpegAudioBitrate: "128k",

		OrientationMinConfidence: 0.50,
	}
}

// ModelDir is where downloaded ML models are cached.
func (c Config) ModelDir() string {
	return filepath.Join(c.StateDir, "models")
}

// VolumePath is the absolute mount path of the camera.
func (c Config) VolumePath() string {
	base := c.VolumesDir
	if base == "" {
		base = "/Volumes"
	}
	return filepath.Join(base, c.VolumeName)
}

// DCIMPath is the absolute path to the camera's media directory.
func (c Config) DCIMPath() string {
	return filepath.Join(c.VolumePath(), c.DCIMSubdir)
}

// StagingDir is the temporary work area where files are fixed/converted before
// import; it is cleaned out after each run.
func (c Config) StagingDir() string {
	return filepath.Join(c.StateDir, "staging")
}
