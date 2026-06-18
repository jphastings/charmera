// Package orient detects the correct orientation of a photo using the
// DuarteBarbosa/deep-image-orientation-detection EfficientNetV2 ONNX model, so a
// rotated shot can be tagged with the right EXIF Orientation. Inference runs
// locally via onnxruntime (loaded from the system libonnxruntime); the package
// is isolated so this heavy, optional dependency stays out of the core tool.
package orient

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"math"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
	"golang.org/x/image/draw"
)

const (
	imageSize  = 384 // EfficientNetV2-S input, per the model's config.IMAGE_SIZE
	resizeSize = 416 // IMAGE_SIZE + 32, resized before the centre crop
)

// ImageNet normalisation, matching the model's training transforms.
var (
	mean = [3]float32{0.485, 0.456, 0.406}
	std  = [3]float32{0.229, 0.224, 0.225}

	// classToOrientation maps the model's class index to the EXIF Orientation
	// tag that makes the image display upright:
	//   0: already correct      -> 1 (normal)
	//   1: needs 90° clockwise   -> 6
	//   2: needs 180°            -> 3
	//   3: needs 90° counter-cw  -> 8
	classToOrientation = [4]int{1, 6, 3, 8}
)

// ortInit guards the process-global onnxruntime environment.
var (
	ortInitOnce sync.Once
	ortInitErr  error
)

func initRuntime(libPath string) error {
	ortInitOnce.Do(func() {
		if libPath != "" {
			ort.SetSharedLibraryPath(libPath)
		}
		ortInitErr = ort.InitializeEnvironment()
	})
	return ortInitErr
}

// Detector wraps a loaded ONNX session. It is not safe for concurrent use; the
// input/output tensors are reused across calls.
type Detector struct {
	session *ort.AdvancedSession
	input   *ort.Tensor[float32]
	output  *ort.Tensor[float32]
}

// Result describes a detection.
type Result struct {
	Orientation int     // EXIF Orientation tag: 1, 3, 6 or 8
	Confidence  float64 // softmax probability of the chosen class
	Rotated     bool    // whether a non-trivial rotation is suggested
}

// New loads the model at modelPath, using the libonnxruntime shared library at
// libPath (empty to use the system default search).
func New(modelPath, libPath string) (*Detector, error) {
	if err := initRuntime(libPath); err != nil {
		return nil, fmt.Errorf("initialising onnxruntime: %w", err)
	}

	inputs, outputs, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("reading model I/O: %w", err)
	}
	if len(inputs) == 0 || len(outputs) == 0 {
		return nil, errors.New("model has no inputs or outputs")
	}

	input, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, imageSize, imageSize))
	if err != nil {
		return nil, err
	}
	output, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(len(classToOrientation))))
	if err != nil {
		input.Destroy()
		return nil, err
	}
	session, err := ort.NewAdvancedSession(modelPath,
		[]string{inputs[0].Name}, []string{outputs[0].Name},
		[]ort.Value{input}, []ort.Value{output}, nil)
	if err != nil {
		input.Destroy()
		output.Destroy()
		return nil, fmt.Errorf("creating session: %w", err)
	}
	return &Detector{session: session, input: input, output: output}, nil
}

// Close releases the session and tensors.
func (d *Detector) Close() {
	d.session.Destroy()
	d.input.Destroy()
	d.output.Destroy()
}

// DetectJPEG decodes JPEG bytes and returns the suggested EXIF orientation.
func (d *Detector) DetectJPEG(data []byte) (Result, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("decoding image: %w", err)
	}
	return d.Detect(img)
}

// Detect runs the model on an already-decoded image.
func (d *Detector) Detect(img image.Image) (Result, error) {
	preprocess(img, d.input.GetData())
	if err := d.session.Run(); err != nil {
		return Result{}, fmt.Errorf("running model: %w", err)
	}
	idx, conf := argmaxSoftmax(d.output.GetData())
	return Result{
		Orientation: classToOrientation[idx],
		Confidence:  conf,
		Rotated:     idx != 0,
	}, nil
}

// preprocess reproduces the model's transforms: resize to 416×416 (aspect is not
// preserved, matching transforms.Resize((416,416))), centre-crop to 384×384,
// scale to [0,1], ImageNet-normalise, and write planar NCHW float32 into dst
// (which must have length 3*384*384).
func preprocess(src image.Image, dst []float32) {
	resized := image.NewRGBA(image.Rect(0, 0, resizeSize, resizeSize))
	draw.BiLinear.Scale(resized, resized.Bounds(), src, src.Bounds(), draw.Src, nil)

	const off = (resizeSize - imageSize) / 2
	plane := imageSize * imageSize
	for y := 0; y < imageSize; y++ {
		row := (y+off)*resized.Stride + off*4
		for x := 0; x < imageSize; x++ {
			p := row + x*4
			i := y*imageSize + x
			dst[i] = (float32(resized.Pix[p])/255 - mean[0]) / std[0]
			dst[plane+i] = (float32(resized.Pix[p+1])/255 - mean[1]) / std[1]
			dst[2*plane+i] = (float32(resized.Pix[p+2])/255 - mean[2]) / std[2]
		}
	}
}

// class180 (rotate 180°) is treated as impossible: a hand-held camera is
// essentially never upside-down, so excluding it avoids spurious 180° flips and
// concentrates probability on the plausible orientations.
const class180 = 2

// argmaxSoftmax returns the highest-scoring plausible class and its softmax
// probability, renormalised as if the 180° class did not exist.
func argmaxSoftmax(logits []float32) (int, float64) {
	maxIdx, maxLogit := -1, float32(0)
	for i, v := range logits {
		if i == class180 {
			continue
		}
		if maxIdx == -1 || v > maxLogit {
			maxIdx, maxLogit = i, v
		}
	}
	var sum float64
	for i, v := range logits {
		if i == class180 {
			continue
		}
		sum += math.Exp(float64(v - maxLogit))
	}
	return maxIdx, 1.0 / sum
}
