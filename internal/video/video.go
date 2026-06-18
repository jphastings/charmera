// Package video converts the Charmera's uncompressed Motion-JPEG/PCM AVI files
// to H.264/AAC MP4, setting a correct creation_time (the camera hard-codes a
// wrong date). It shells out to system ffmpeg/ffprobe.
package video

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/jphastings/charmera/internal/config"
)

// creationTimeLayout is ffmpeg's expected ISO-8601-ish metadata time format.
const creationTimeLayout = "2006-01-02T15:04:05"

// Converter wraps the ffmpeg/ffprobe binaries.
type Converter struct {
	FFmpeg  string
	FFprobe string
	cfg     config.Config
}

// New returns a Converter using ffmpeg/ffprobe from PATH.
func New(cfg config.Config) *Converter {
	return &Converter{FFmpeg: "ffmpeg", FFprobe: "ffprobe", cfg: cfg}
}

// Available reports whether both ffmpeg and ffprobe can be found. The returned
// error explains what is missing and how to install it.
func (c *Converter) Available() error {
	var missing []string
	if _, err := exec.LookPath(c.FFmpeg); err != nil {
		missing = append(missing, "ffmpeg")
	}
	if _, err := exec.LookPath(c.FFprobe); err != nil {
		missing = append(missing, "ffprobe")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s not found on PATH; install with `brew install ffmpeg`", strings.Join(missing, " and "))
	}
	return nil
}

// buildArgs assembles the ffmpeg arguments for a single conversion.
func (c *Converter) buildArgs(input, output string, creationTime time.Time) []string {
	return []string{
		"-y",
		"-i", input,
		"-c:v", c.cfg.FFmpegVideoCodec,
		"-crf", strconv.Itoa(c.cfg.FFmpegCRF),
		"-preset", c.cfg.FFmpegPreset,
		"-c:a", c.cfg.FFmpegAudioCodec,
		"-b:a", c.cfg.FFmpegAudioBitrate,
		"-ar", "44100",
		"-metadata", "creation_time=" + creationTime.Format(creationTimeLayout),
		"-progress", "pipe:1",
		output,
	}
}

// Convert transcodes input to output. onProgress, if non-nil, is called with a
// 0..100 percentage as the encode proceeds.
func (c *Converter) Convert(ctx context.Context, input, output string, creationTime time.Time, onProgress func(pct float64)) error {
	duration, _ := c.probeDuration(ctx, input) // best-effort; progress is optional

	cmd := exec.CommandContext(ctx, c.FFmpeg, c.buildArgs(input, output, creationTime)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if onProgress == nil || duration <= 0 {
			continue
		}
		line := scanner.Text()
		if v, ok := strings.CutPrefix(line, "out_time_us="); ok {
			if us, err := strconv.ParseFloat(v, 64); err == nil {
				pct := (us / 1_000_000) / duration * 100
				if pct > 100 {
					pct = 100
				}
				onProgress(pct)
			}
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w\n%s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// probeDuration returns the media duration in seconds.
func (c *Converter) probeDuration(ctx context.Context, input string) (float64, error) {
	out, err := exec.CommandContext(ctx, c.FFprobe,
		"-v", "quiet",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		input,
	).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}
