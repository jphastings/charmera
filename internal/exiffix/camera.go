package exiffix

// Known, published facts about the Kodak Charmera, stamped into the rebuilt
// EXIF. Sources: manufacturer spec sheet and Wikipedia — a 1/4" CMOS sensor
// behind a fixed 35 mm-equivalent f/2.4 lens. We deliberately do not invent an
// actual focal length (only the 35 mm-equivalent is published).
const (
	cameraMake  = "Kodak"
	cameraModel = "Charmera"

	lensMake  = "Kodak"
	lensModel = "Charmera fixed lens (35mm-equiv f/2.4)"

	// FNumber f/2.4 as a RATIONAL.
	fNumberNum = 24
	fNumberDen = 10

	focalLength35mm = 35
)

// orDefault returns v if non-empty, else fallback.
func orDefault(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
