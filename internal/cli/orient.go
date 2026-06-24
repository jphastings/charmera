package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/jphastings/charmera/internal/config"
	"github.com/jphastings/charmera/internal/orient"
	"github.com/jphastings/charmera/internal/photos"
	"github.com/jphastings/charmera/internal/pipeline"
	"github.com/jphastings/charmera/internal/video"
)

// daemonImporter adapts the pipeline to daemon.Importer, building a fresh
// pipeline per camera while sharing the (expensive) orientation detector. Plan
// runs silently; only Execute carries an observer for progress.
type daemonImporter struct {
	cfg      config.Config
	detector pipeline.OrientationDetector
}

func (di daemonImporter) pipeline(volumePath string, obs pipeline.Observer) *pipeline.Pipeline {
	return pipeline.New(di.cfg, volumePath, video.New(di.cfg), photos.New(di.cfg.Album), pipeline.Options{
		Observer:      obs,
		Detector:      di.detector,
		MinConfidence: di.cfg.OrientationMinConfidence,
	})
}

func (di daemonImporter) Plan(ctx context.Context, volumePath string) (pipeline.Plan, error) {
	return di.pipeline(volumePath, nil).Plan(ctx)
}

func (di daemonImporter) Execute(ctx context.Context, volumePath string, plan pipeline.Plan, obs pipeline.Observer) (pipeline.Summary, error) {
	return di.pipeline(volumePath, obs).Execute(ctx, plan)
}

// orientAdapter adapts *orient.Detector to pipeline.OrientationDetector.
type orientAdapter struct{ d *orient.Detector }

func (a orientAdapter) DetectJPEG(data []byte) (int, float64, error) {
	r, err := a.d.DetectJPEG(data)
	return r.Orientation, r.Confidence, err
}

// newDetector builds an orientation detector when onnxruntime and the model are
// available. It returns (nil, nil) when the runtime isn't installed — detection
// is simply off in that case (run `brew install onnxruntime` to enable it).
func newDetector(cfg config.Config) (pipeline.OrientationDetector, func()) {
	lib := orient.LibraryPath()
	if lib == "" {
		return nil, nil // onnxruntime not installed; auto-rotate disabled
	}

	model, err := orient.EnsureModel(cfg.ModelDir(), func(msg string) {
		fmt.Println(msg + " …")
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "orientation detection disabled: %v\n", err)
		return nil, nil
	}

	d, err := orient.New(model, lib)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orientation detection disabled: %v\n", err)
		return nil, nil
	}
	return orientAdapter{d}, d.Close
}
