package cli

import (
	"fmt"
	"os"

	"github.com/jphastings/charmera/internal/config"
	"github.com/jphastings/charmera/internal/orient"
	"github.com/jphastings/charmera/internal/pipeline"
)

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
