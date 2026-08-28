package svc

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// gifMagic is the leading signature of a GIF file (GIF87a or GIF89a).
var gifMagic = []byte("GIF8")

// Operations understood by the push endpoint. They map to imagor's resize/crop
// modes so a real imagor deployment can replace this service.
const (
	OpFill    = "fill"
	OpFitIn   = "fit-in"
	OpStretch = "stretch"
)

// errInvalid mirrors imagor's ErrInvalid: a syntactically invalid request.
var errInvalid = fmt.Errorf("invalid")

// pushHandler handles POST requests for the imagor-like push-based thumbnail endpoint.
// Path patterns (operation is optional; its absence is the default center-crop fill):
//   - /unsafe/{operation}/{W}x{H}/filters:no_upscale()/filters:format({ext})/
//   - /unsafe/{W}x{H}/filters:format({ext})/
//
// It returns only imagor's defined status codes: 400 for invalid requests or a
// file exceeding the max size, 422 for an image exceeding the max resolution.
func (s Thumbnails) pushHandler(w http.ResponseWriter, r *http.Request) {
	width, err := parseDim(r, "width")
	if err != nil {
		writeInvalid(w, "invalid width")
		return
	}
	height, err := parseDim(r, "height")
	if err != nil {
		writeInvalid(w, "invalid height")
		return
	}

	operation := chi.URLParam(r, "operation")
	if operation == "" {
		operation = OpFill
	}
	switch operation {
	case OpFill, OpFitIn, OpStretch:
	default:
		writeInvalid(w, fmt.Sprintf("unsupported operation: %s", operation))
		return
	}

	format := chi.URLParam(r, "format")
	ext := mapFormatToExt(format)
	switch ext {
	case "jpg", "png", "gif":
	default:
		writeInvalid(w, fmt.Sprintf("unsupported output format: %s", format))
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeInvalid(w, "failed to parse multipart form")
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		writeInvalid(w, "missing image field in form data")
		return
	}
	defer file.Close()

	imgData, err := io.ReadAll(file)
	if err != nil {
		writeInvalid(w, "failed to read image")
		return
	}

	// The box and operation are chosen by webdav (the sizing brain); this service
	// is a dumb, imagor-like executor that just fits within the given box.
	processed, srcBounds, err := processImage(bytes.NewReader(imgData), width, height, operation)
	if err != nil {
		writeInvalid(w, "failed to process image")
		return
	}

	// imagor ErrMaxResolutionExceeded: the decoded input exceeds the configured
	// maximum width/height. Checked against the source (pre-resize) bounds because
	// this service owns the image; webdav translates the resulting 422 into its
	// legacy 403 response.
	if s.maxWidth > 0 || s.maxHeight > 0 {
		if (s.maxWidth == 0 || srcBounds.Dx() > s.maxWidth) && (s.maxHeight == 0 || srcBounds.Dy() > s.maxHeight) {
			writeMaxResolution(w)
			return
		}
	}

	var (
		buf         bytes.Buffer
		contentType string
		encErr      error
	)
	switch ext {
	case "jpg":
		contentType = "image/jpeg"
		img, ok := processed.(image.Image)
		if !ok {
			writeInvalid(w, "cannot encode gif as jpeg")
			return
		}
		encErr = jpeg.Encode(&buf, img, nil)
	case "png":
		contentType = "image/png"
		img, ok := processed.(image.Image)
		if !ok {
			writeInvalid(w, "cannot encode gif as png")
			return
		}
		encErr = png.Encode(&buf, img)
	default:
		contentType = "image/gif"
		g, ok := processed.(*gif.GIF)
		if !ok {
			writeInvalid(w, "processed image is not a gif")
			return
		}
		encErr = gif.EncodeAll(&buf, g)
	}
	if encErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "failed to encode image")
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	buf.WriteTo(w)
}

func parseDim(r *http.Request, name string) (int, error) {
	v := chi.URLParam(r, name)
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid %s: %s", name, v)
	}
	return n, nil
}

// writeInvalid responds with imagor's ErrInvalid status (400 Bad Request).
func writeInvalid(w http.ResponseWriter, msg string) {
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintln(w, msg)
}

// writeMaxResolution responds with imagor's ErrMaxResolutionExceeded status
// (422 Unprocessable Entity).
func writeMaxResolution(w http.ResponseWriter) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	fmt.Fprintln(w, "maximum resolution exceeded")
}

// mapFormatToExt maps imagor format names to the internal extension used by encoders.
func mapFormatToExt(format string) string {
	switch strings.ToLower(format) {
	case "jpeg":
		return "jpg"
	default:
		return strings.ToLower(format)
	}
}

// isGifReader reports whether the reader holds GIF data, based on the GIF magic
// bytes. It peeks at the first 4 bytes and rewinds the stream so it can be read
// again. This guard is required because gif.DecodeAll panics (nil dereference)
// when handed a non-GIF image instead of returning an error.
func isGifReader(r io.Reader) bool {
	readSeeker, ok := r.(io.ReadSeeker)
	if !ok {
		return false
	}

	saved, err := readSeeker.Seek(0, io.SeekCurrent)
	if err != nil {
		return false
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(readSeeker, buf); err != nil {
		return false
	}
	_, _ = readSeeker.Seek(saved, io.SeekStart)

	return bytes.HasPrefix(buf, gifMagic)
}

// Backend-specific function set in init() by push_imaging.go or push_vips.go.
// For gif input it returns a *gif.GIF with every frame resized (preserving
// animation); for other inputs it returns a single image.Image. The operation
// selects the resize/crop mode (fill, fit-in, stretch). The
// second return value is the decoded source's pixel bounds, reported before any
// resize so the max-resolution limit can be enforced against the input rather
// than the output.
var processImage func(io.Reader, int, int, string) (any, image.Rectangle, error)
