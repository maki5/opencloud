//go:build enable_vips

package svc

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"io"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/kovidgoyal/imaging"
)

func init() {
	processImage = processImageVips
}

// processImageVips resizes the input using libvips. The operation selects the
// resize/crop mode: fill (center-crop to the box), fit-in (fit within the box
// without cropping, never upscaling), or stretch (resize to the exact box).
func processImageVips(r io.Reader, width, height int, operation string) (any, image.Rectangle, error) {
	if isGifReader(r) {
		g, err := gif.DecodeAll(r)
		if err == nil && len(g.Image) > 0 {
			srcBounds := g.Image[0].Bounds()
			return resizeGIFVips(g, width, height, operation), srcBounds, nil
		}
	}

	imgData, err := io.ReadAll(r)
	if err != nil {
		return nil, image.Rectangle{}, err
	}

	m, err := vips.NewImageFromBuffer(imgData)
	if err != nil {
		return nil, image.Rectangle{}, err
	}
	defer m.Close()

	srcBounds := image.Rect(0, 0, m.Width(), m.Height())

	switch operation {
	case OpStretch:
		// Resize to the exact box without preserving aspect ratio.
		hScale := float64(width) / float64(m.Width())
		vScale := float64(height) / float64(m.Height())
		if err := m.ResizeWithVScale(hScale, vScale, vips.KernelLanczos3); err != nil {
			return nil, image.Rectangle{}, err
		}
	case OpFitIn:
		// Fit within the box without cropping and never upscale (SizeDown).
		if err := m.ThumbnailWithSize(width, height, vips.InterestingNone, vips.SizeDown); err != nil {
			return nil, image.Rectangle{}, err
		}
	default: // OpFill
		// Center-crop to fill the box exactly.
		if err := m.ThumbnailWithSize(width, height, vips.InterestingAttention, vips.SizeBoth); err != nil {
			return nil, image.Rectangle{}, err
		}
	}

	if err := m.RemoveMetadata(); err != nil {
		return nil, image.Rectangle{}, err
	}

	img, err := vipsToImage(m)
	if err != nil {
		return nil, image.Rectangle{}, err
	}
	return img, srcBounds, nil
}

// resizeGIFVips resizes every frame of an animated gif while preserving the
// animation, compositing each frame onto a running canvas honoring the gif
// disposal method and re-palletting with Floyd-Steinberg dithering.
func resizeGIFVips(m *gif.GIF, width, height int, operation string) *gif.GIF {
	srcX, srcY := m.Config.Width, m.Config.Height
	b := image.Rect(0, 0, srcX, srcY)
	tmp := image.NewRGBA(b)

	for i, frame := range m.Image {
		frameBounds := frame.Bounds()
		prev := tmp
		draw.Draw(tmp, frameBounds, frame, frameBounds.Min, draw.Over)

		var processed image.Image
		switch operation {
		case OpStretch:
			processed = imaging.Resize(tmp, width, height, imaging.Lanczos)
		case OpFitIn:
			if srcX > width || srcY > height {
				processed = imaging.Fit(tmp, width, height, imaging.Lanczos)
			} else {
				processed = tmp
			}
		default: // OpFill
			processed = imaging.Fill(tmp, width, height, imaging.Center, imaging.Lanczos)
		}

		m.Image[i] = imageToPalettedVips(processed, frame.Palette)

		switch m.Disposal[i] {
		case gif.DisposalBackground:
			tmp = image.NewRGBA(b)
		case gif.DisposalPrevious:
			tmp = prev
		}
	}

	m.Config.Width = width
	m.Config.Height = height

	return m
}

func imageToPalettedVips(img image.Image, p color.Palette) *image.Paletted {
	b := img.Bounds()
	pm := image.NewPaletted(b, p)
	draw.FloydSteinberg.Draw(pm, b, img, image.Point{})
	return pm
}

// vipsToImage converts a *vips.ImageRef to an image.Image by exporting as PNG and decoding.
func vipsToImage(m *vips.ImageRef) (image.Image, error) {
	data, _, err := m.ExportPng(vips.NewPngExportParams())
	if err != nil {
		return nil, err
	}
	return imaging.Decode(bytes.NewReader(data))
}
