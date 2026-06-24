// Package pipeline orchestrates the full Charmera workflow: scan the camera,
// skip anything already in the Photos album, fix EXIF / convert video, import
// into Photos, then discard the staged copy. Photos itself is the dedup record
// (via a content hash embedded in each imported filename) — there is no
// separate database.
//
// The camera is treated as strictly read-only; all writes happen in a temporary
// staging area that is cleared after each run.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jphastings/charmera/internal/config"
	"github.com/jphastings/charmera/internal/exiffix"
	"github.com/jphastings/charmera/internal/scan"
)

// VideoConverter converts an AVI to MP4. Satisfied by *video.Converter.
type VideoConverter interface {
	Available() error
	Convert(ctx context.Context, input, output string, creationTime time.Time, onProgress func(float64)) error
}

// PhotoImporter imports files into Photos and reports what the album already
// contains. Satisfied by *photos.Importer.
type PhotoImporter interface {
	Available() error
	AlbumFilenames(ctx context.Context) ([]string, error)
	Import(ctx context.Context, paths []string) error
}

// OrientationDetector suggests an EXIF Orientation (1, 3, 6 or 8) for a photo
// from its pixels, with a confidence. Optional; nil disables auto-rotation.
type OrientationDetector interface {
	DetectJPEG(data []byte) (orientation int, confidence float64, err error)
}

// Event is a progress notification emitted during a run.
type Event struct {
	Phase  string // scan, skip, fix, convert, import, warn, error, done
	Name   string
	Detail string
	Err    error
}

// Observer receives progress events. It may be nil.
type Observer func(Event)

// Summary tallies the outcome of a run.
type Summary struct {
	Scanned         int
	Skipped         int // already present in the Photos album
	PhotosFixed     int
	VideosConverted int
	Imported        int
	Failed          int
}

// Options tune a run.
type Options struct {
	// OutDir, when set, writes the fixed/converted files to that directory and
	// skips the Photos import. When empty (the default), the files are imported
	// into Photos and not kept on disk.
	OutDir   string
	DryRun   bool     // report planned actions without changing anything
	Observer Observer // progress sink

	// Detector, when non-nil, suggests an EXIF Orientation per photo. A
	// suggestion is applied only when it differs from "normal" and its
	// confidence reaches MinConfidence.
	Detector      OrientationDetector
	MinConfidence float64
}

// importing reports whether this run imports into Photos (vs. writing to OutDir).
func (o Options) importing() bool { return o.OutDir == "" }

// Pipeline holds the configured dependencies for a run.
type Pipeline struct {
	cfg        config.Config
	volumePath string
	converter  VideoConverter
	importer   PhotoImporter
	opts       Options
}

// New builds a Pipeline. volumePath is the camera's mount path (from
// scan.FindCamera).
func New(cfg config.Config, volumePath string, converter VideoConverter, importer PhotoImporter, opts Options) *Pipeline {
	return &Pipeline{cfg: cfg, volumePath: volumePath, converter: converter, importer: importer, opts: opts}
}

func (p *Pipeline) emit(e Event) {
	if p.opts.Observer != nil {
		p.opts.Observer(e)
	}
}

// produced is a file that has been fixed/converted and is awaiting import.
type produced struct {
	file scan.File
	path string // location in the target directory
}

// ErrImporterUnavailable means Photos can't be driven right now (e.g. the app
// isn't reachable / not yet authorised). It signals "nothing to do this time",
// not a hard failure — callers can retry later.
var ErrImporterUnavailable = errors.New("Photos import unavailable")

// Plan is the set of new files for one camera, after deduping against the Photos
// album. It is produced without changing anything, so it is safe to compute even
// when paused (to show "N items to import").
type Plan struct {
	Files   []scan.File // new files that will be processed, in scan order
	Scanned int         // total media files on the camera
	Skipped int         // already in the album (or duplicate content within the scan)
	Photos  int
	Videos  int
}

// Total is the number of files that would be imported.
func (p Plan) Total() int { return len(p.Files) }

// Plan scans the camera and works out which files are new (not already in the
// album), changing nothing.
func (p *Pipeline) Plan(ctx context.Context) (Plan, error) {
	var plan Plan

	p.emit(Event{Phase: "scan", Detail: p.volumePath})
	files, err := scan.Scan(p.cfg, p.volumePath)
	if err != nil {
		return plan, fmt.Errorf("scanning camera: %w", err)
	}
	plan.Scanned = len(files)

	// Dedup is driven by Photos itself: the album's existing filenames carry the
	// content-hash key. Deleting a photo from Photos therefore re-enables import.
	seen := map[string]bool{}
	if p.opts.importing() {
		if err := p.importer.Available(); err != nil {
			if !p.opts.DryRun {
				return plan, fmt.Errorf("%w: %v", ErrImporterUnavailable, err)
			}
			p.emit(Event{Phase: "warn", Detail: "Photos unavailable; dedup disabled in preview"})
		} else {
			names, err := p.importer.AlbumFilenames(ctx)
			if err != nil {
				return plan, err
			}
			for _, n := range names {
				if k, ok := keyFromName(n); ok {
					seen[k] = true
				}
			}
		}
	}

	ffmpegOK := p.converter.Available() == nil
	for _, f := range files {
		if p.opts.importing() {
			key := hashKey(f.Hash)
			if seen[key] {
				plan.Skipped++
				p.emit(Event{Phase: "skip", Name: f.Name, Detail: "already imported"})
				continue
			}
			seen[key] = true // also dedups identical content within this scan
		}
		if f.Kind == scan.Video && !ffmpegOK {
			p.emit(Event{Phase: "warn", Name: f.Name, Detail: "ffmpeg unavailable; skipping video"})
			continue
		}
		plan.Files = append(plan.Files, f)
		if f.Kind == scan.Video {
			plan.Videos++
		} else {
			plan.Photos++
		}
	}
	return plan, nil
}

// Run plans and then executes the workflow, returning a summary.
func (p *Pipeline) Run(ctx context.Context) (Summary, error) {
	plan, err := p.Plan(ctx)
	if errors.Is(err, ErrImporterUnavailable) {
		p.emit(Event{Phase: "warn", Detail: err.Error()})
		return Summary{Scanned: plan.Scanned}, nil
	}
	if err != nil {
		return Summary{Scanned: plan.Scanned, Skipped: plan.Skipped}, err
	}

	if p.opts.DryRun {
		sum := Summary{Scanned: plan.Scanned, Skipped: plan.Skipped}
		for _, f := range plan.Files {
			p.emit(Event{Phase: planPhase(f.Kind), Name: f.Name, Detail: "would " + planPhase(f.Kind)})
			countProduced(&sum, f.Kind)
		}
		p.emit(Event{Phase: "done"})
		return sum, nil
	}

	return p.Execute(ctx, plan)
}

// Execute fixes/converts the planned files into the staging area (or OutDir) and
// imports them into Photos (unless writing to OutDir). The camera is only read.
// It emits a "fix"/"convert" event per file as progress.
func (p *Pipeline) Execute(ctx context.Context, plan Plan) (Summary, error) {
	sum := Summary{Scanned: plan.Scanned, Skipped: plan.Skipped}

	// A clean staging area when importing (removed afterwards), or the user's
	// OutDir otherwise.
	targetDir := p.opts.OutDir
	if p.opts.importing() {
		targetDir = p.cfg.StagingDir()
		os.RemoveAll(targetDir) // discard any leftovers from a prior crash
		defer os.RemoveAll(targetDir)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return sum, err
	}

	// Phase 1: fix/convert each new file into the target directory.
	var items []produced
	for _, f := range plan.Files {
		path, err := p.process(ctx, f, targetDir)
		if err != nil {
			sum.Failed++
			p.emit(Event{Phase: "error", Name: f.Name, Err: err})
			continue
		}
		countProduced(&sum, f.Kind)
		items = append(items, produced{file: f, path: path})
	}

	// OutDir mode: the fixed files stay where they were written; nothing is
	// imported.
	if !p.opts.importing() {
		p.emit(Event{Phase: "warn", Detail: fmt.Sprintf("import skipped; %d file(s) written to %s", len(items), targetDir)})
		p.emit(Event{Phase: "done"})
		return sum, nil
	}
	if len(items) == 0 {
		p.emit(Event{Phase: "done"})
		return sum, nil
	}

	// Phase 2: import everything in one batch. The staged files are discarded by
	// the deferred cleanup — Photos now holds the only copy, and the imported
	// filenames are the dedup record for next time.
	paths := make([]string, len(items))
	for i, it := range items {
		paths[i] = it.path
	}
	p.emit(Event{Phase: "import", Detail: fmt.Sprintf("%d file(s) into %q", len(paths), p.cfg.Album)})
	if err := p.importer.Import(ctx, paths); err != nil {
		p.emit(Event{Phase: "error", Detail: "import", Err: err})
		return sum, err
	}
	sum.Imported = len(items)
	for _, it := range items {
		p.emit(Event{Phase: "import", Name: it.file.Name, Detail: "imported"})
	}

	p.emit(Event{Phase: "done"})
	return sum, nil
}

// process fixes a photo or converts a video into targetDir with a date-based
// name, returning the output path. The camera is only read.
func (p *Pipeline) process(ctx context.Context, f scan.File, targetDir string) (string, error) {
	switch f.Kind {
	case scan.Photo:
		return p.fixPhoto(f, targetDir)
	case scan.Video:
		return p.convertVideo(ctx, f, targetDir)
	default:
		return "", fmt.Errorf("unknown kind %q", f.Kind)
	}
}

func (p *Pipeline) fixPhoto(f scan.File, targetDir string) (string, error) {
	data, err := os.ReadFile(f.SourcePath)
	if err != nil {
		return "", err
	}

	orientation := p.detectOrientation(data, f.Name)

	fixed, res, err := exiffix.FixWithOrientation(data, f.ModTime, orientation)
	if err != nil {
		return "", err
	}
	out := uniquePath(targetDir, destName(f))
	if err := os.WriteFile(out, fixed, 0o644); err != nil {
		return "", err
	}
	if err := os.Chtimes(out, f.ModTime, f.ModTime); err != nil {
		return "", err
	}
	p.emit(Event{Phase: "fix", Name: f.Name, Detail: fmt.Sprintf("%dx%d date=%s(%s) orient=%d", res.Width, res.Height, res.DateTime, res.DateSource, res.Orientation)})
	return out, nil
}

// detectOrientation returns an EXIF Orientation override (0 = none) from the
// detector, applied only when it confidently suggests a rotation.
func (p *Pipeline) detectOrientation(data []byte, name string) int {
	if p.opts.Detector == nil {
		return 0
	}
	orientation, confidence, err := p.opts.Detector.DetectJPEG(data)
	if err != nil {
		p.emit(Event{Phase: "warn", Name: name, Detail: "orientation detection failed: " + err.Error()})
		return 0
	}
	if orientation == 1 || confidence < p.opts.MinConfidence {
		return 0 // already upright, or not confident enough to rotate
	}
	return orientation
}

func (p *Pipeline) convertVideo(ctx context.Context, f scan.File, targetDir string) (string, error) {
	out := uniquePath(targetDir, destName(f))
	p.emit(Event{Phase: "convert", Name: f.Name})
	if err := p.converter.Convert(ctx, f.SourcePath, out, f.ModTime, nil); err != nil {
		return "", err
	}
	if err := os.Chtimes(out, f.ModTime, f.ModTime); err != nil {
		return "", err
	}
	return out, nil
}

func planPhase(k scan.Kind) string {
	if k == scan.Video {
		return "convert"
	}
	return "fix"
}

func countProduced(sum *Summary, k scan.Kind) {
	if k == scan.Video {
		sum.VideosConverted++
	} else {
		sum.PhotosFixed++
	}
}
