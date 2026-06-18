package exiffix

import "encoding/binary"

// TIFF field types used by the encoder.
const (
	typeASCII    = 2
	typeShort    = 3
	typeLong     = 4
	typeRational = 5
	typeUndef    = 7
)

// exifData is the corrected metadata to embed. Zero/empty optional fields are
// omitted from the output.
type exifData struct {
	make        string
	model       string
	orientation int
	width       int
	height      int
	dateTime    string // standard EXIF form: "YYYY:MM:DD HH:MM:SS"

	// Fixed, known camera/lens facts (optional).
	lensMake      string
	lensModel     string
	fNumberNum    uint32 // FNumber numerator; 0 (with den 0) omits the tag
	fNumberDen    uint32
	focalLength35 uint16 // FocalLengthIn35mmFilm in mm; 0 omits the tag
}

// ifd0 + ExifIFD tags
const (
	tagMake          = 0x010F
	tagModel         = 0x0110
	tagOrientation   = 0x0112
	tagDateTime      = 0x0132
	tagYCbCrPos      = 0x0213
	tagExifIFD       = 0x8769
	tagFNumber       = 0x829D
	tagExifVersion   = 0x9000
	tagDateTimeOrig  = 0x9003
	tagDateTimeDigi  = 0x9004
	tagComponentsCfg = 0x9101
	tagFlashpixVer   = 0xA000
	tagColorSpace    = 0xA001
	tagPixelXDim     = 0xA002
	tagPixelYDim     = 0xA003
	tagFocalLen35    = 0xA405
	tagLensMake      = 0xA433
	tagLensModel     = 0xA434
)

// entry is one IFD directory entry. If external is non-nil the value is stored
// in the data area and inline is overwritten with its offset during encoding.
type entry struct {
	tag      uint16
	typ      uint16
	count    uint32
	inline   [4]byte
	external []byte
}

func asciiEntry(tag uint16, s string) entry {
	b := append([]byte(s), 0) // NUL terminator
	e := entry{tag: tag, typ: typeASCII, count: uint32(len(b))}
	if len(b) <= 4 {
		copy(e.inline[:], b)
	} else {
		if len(b)%2 == 1 {
			b = append(b, 0) // word-align external values
		}
		e.external = b
	}
	return e
}

func shortEntry(tag uint16, v uint16) entry {
	e := entry{tag: tag, typ: typeShort, count: 1}
	binary.LittleEndian.PutUint16(e.inline[:], v)
	return e
}

func longEntry(tag uint16, v uint32) entry {
	e := entry{tag: tag, typ: typeLong, count: 1}
	binary.LittleEndian.PutUint32(e.inline[:], v)
	return e
}

func undefEntry(tag uint16, b []byte) entry {
	e := entry{tag: tag, typ: typeUndef, count: uint32(len(b))}
	copy(e.inline[:], b) // assumes len(b) <= 4
	return e
}

// rationalEntry stores a single RATIONAL (two uint32s); always external since
// the value is 8 bytes.
func rationalEntry(tag uint16, num, den uint32) entry {
	ext := make([]byte, 8)
	binary.LittleEndian.PutUint32(ext[0:4], num)
	binary.LittleEndian.PutUint32(ext[4:8], den)
	return entry{tag: tag, typ: typeRational, count: 1, external: ext}
}

// ifdSize is the on-disk byte size of an IFD with n entries.
func ifdSize(n int) int { return 2 + 12*n + 4 }

// encodeEXIF builds a complete APP1 payload: the "Exif\0\0" identifier followed
// by a fresh little-endian TIFF holding only the corrected tags. Building from
// scratch is what lets us drop the camera's corrupt MakerNote/IFD entirely.
func encodeEXIF(d exifData) []byte {
	ifd0 := []entry{}
	if d.make != "" {
		ifd0 = append(ifd0, asciiEntry(tagMake, d.make))
	}
	if d.model != "" {
		ifd0 = append(ifd0, asciiEntry(tagModel, d.model))
	}
	orientation := d.orientation
	if orientation < 1 {
		orientation = 1
	}
	ifd0 = append(ifd0,
		shortEntry(tagOrientation, uint16(orientation)),
		asciiEntry(tagDateTime, d.dateTime),
		shortEntry(tagYCbCrPos, 1), // Centered
		longEntry(tagExifIFD, 0),   // offset patched below
	)

	// Entries must be in ascending tag order. ComponentsConfiguration,
	// FlashpixVersion and ColorSpace are mandatory Exif IFD tags; including them
	// keeps the EXIF spec-complete and warning-free in strict validators.
	exifIFD := []entry{}
	if d.fNumberDen != 0 {
		exifIFD = append(exifIFD, rationalEntry(tagFNumber, d.fNumberNum, d.fNumberDen))
	}
	exifIFD = append(exifIFD,
		undefEntry(tagExifVersion, []byte("0230")),
		asciiEntry(tagDateTimeOrig, d.dateTime),
		asciiEntry(tagDateTimeDigi, d.dateTime),
		undefEntry(tagComponentsCfg, []byte{1, 2, 3, 0}), // Y, Cb, Cr, -
		undefEntry(tagFlashpixVer, []byte("0100")),
		shortEntry(tagColorSpace, 1), // sRGB
		longEntry(tagPixelXDim, uint32(d.width)),
		longEntry(tagPixelYDim, uint32(d.height)),
	)
	if d.focalLength35 != 0 {
		exifIFD = append(exifIFD, shortEntry(tagFocalLen35, d.focalLength35))
	}
	if d.lensMake != "" {
		exifIFD = append(exifIFD, asciiEntry(tagLensMake, d.lensMake))
	}
	if d.lensModel != "" {
		exifIFD = append(exifIFD, asciiEntry(tagLensModel, d.lensModel))
	}

	const headerSize = 8
	exifIFDOffset := headerSize + ifdSize(len(ifd0))
	extBase := exifIFDOffset + ifdSize(len(exifIFD))

	// Patch the ExifIFD pointer (last entry of ifd0).
	binary.LittleEndian.PutUint32(ifd0[len(ifd0)-1].inline[:], uint32(exifIFDOffset))

	// Assign external-data offsets in serialization order.
	var ext []byte
	assign := func(entries []entry) {
		for i := range entries {
			if entries[i].external == nil {
				continue
			}
			off := extBase + len(ext)
			binary.LittleEndian.PutUint32(entries[i].inline[:], uint32(off))
			ext = append(ext, entries[i].external...)
		}
	}
	assign(ifd0)
	assign(exifIFD)

	out := make([]byte, 0, extBase+len(ext)+6)
	out = append(out, 'E', 'x', 'i', 'f', 0, 0)
	tiffStart := len(out)

	out = append(out, 'I', 'I', 0x2A, 0x00)
	out = appendU32(out, uint32(headerSize)) // IFD0 immediately follows the header

	writeIFD := func(entries []entry, next uint32) {
		out = appendU16(out, uint16(len(entries)))
		for _, e := range entries {
			out = appendU16(out, e.tag)
			out = appendU16(out, e.typ)
			out = appendU32(out, e.count)
			out = append(out, e.inline[:]...)
		}
		out = appendU32(out, next)
	}
	writeIFD(ifd0, 0)
	writeIFD(exifIFD, 0)
	out = append(out, ext...)

	_ = tiffStart
	return out
}

func appendU16(b []byte, v uint16) []byte {
	return append(b, byte(v), byte(v>>8))
}

func appendU32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}
