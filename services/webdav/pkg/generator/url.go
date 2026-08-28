package generator

import (
	"fmt"
	"strings"
)

// Operations are the imagor resize/crop operations the generator understands.
const (
	// OpFill center-crops to fill the box exactly (imagor default resize).
	OpFill = "fill"
	// OpFitIn fits within the box without cropping and never upscales (imagor
	// fit-in), preserving the source aspect ratio.
	OpFitIn = "fit-in"
	// OpStretch resizes to the exact box without preserving aspect ratio.
	OpStretch = "stretch"
)

// operationSegment maps an operation to its imagor URL path segment. The fill
// operation is the bare WxH resize (imagor's default), so it contributes no
// leading segment.
func operationSegment(op string) string {
	switch op {
	case OpFitIn:
		return "fit-in"
	case OpStretch:
		return "stretch"
	default: // OpFill and anything unrecognized fall back to the default resize
		return ""
	}
}

// BuildURL constructs the thumbnail generator processing URL for a matched box
// and operation. The image is never upscaled beyond its source size (no_upscale)
// and is re-encoded to the requested output format.
func BuildURL(baseURL string, width, height int32, operation, outputExt string) string {
	base := strings.TrimRight(baseURL, "/")

	box := fmt.Sprintf("%dx%d", width, height)
	noUpscale := "filters:no_upscale()"
	formatFilter := fmt.Sprintf("filters:format(%s)", outputExt)

	segment := operationSegment(operation)
	if segment == "" {
		return fmt.Sprintf("%s/unsafe/%s/%s/%s/", base, box, noUpscale, formatFilter)
	}
	return fmt.Sprintf("%s/unsafe/%s/%s/%s/%s/", base, segment, box, noUpscale, formatFilter)
}
