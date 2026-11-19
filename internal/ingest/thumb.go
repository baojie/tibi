package ingest

import (
	"bytes"
	"errors"
	"image"
	"image/draw"

	"github.com/gen2brain/webp"
	xdraw "golang.org/x/image/draw"
)

// ThumbWidth is the width of generated thumbnails.
const ThumbWidth = 400

// maxThumbPixels guards against decompression bombs: images larger than
// this are not decoded for thumbnails.
const maxThumbPixels = 100_000_000

// errNoThumb means no thumbnail can be made for this image type.
var errNoThumb = errors.New("no thumbnail for this image")

// MakeThumb renders a WebP thumbnail at most ThumbWidth wide, applying the
// JPEG EXIF orientation so it displays upright.
func MakeThumb(data []byte, mt string) ([]byte, error) {
	switch mt {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return nil, errNoThumb // e.g. AVIF: no pure-Go decoder
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width*cfg.Height > maxThumbPixels || cfg.Width == 0 || cfg.Height == 0 {
		return nil, errNoThumb
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	o := 1
	if mt == "image/jpeg" {
		o = JPEGOrientation(data)
	}
	// Scale first, then rotate the small image: orient is per-pixel.
	b := src.Bounds()
	dw, dh := b.Dx(), b.Dy() // displayed size
	if o >= 5 {
		dw, dh = dh, dw
	}
	tw, th := dw, dh
	if tw > ThumbWidth {
		th = max(1, dh*ThumbWidth/dw)
		tw = ThumbWidth
	}
	sw, sh := tw, th // size in source orientation
	if o >= 5 {
		sw, sh = th, tw
	}
	scaled := image.NewNRGBA(image.Rect(0, 0, sw, sh))
	if sw == b.Dx() && sh == b.Dy() {
		draw.Draw(scaled, scaled.Bounds(), src, b.Min, draw.Src)
	} else {
		xdraw.CatmullRom.Scale(scaled, scaled.Bounds(), src, b, draw.Src, nil)
	}
	dst := orient(scaled, o)
	var out bytes.Buffer
	if err := webp.Encode(&out, dst, webp.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// orient applies an EXIF orientation (1–8) to img.
func orient(img image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	ow, oh := w, h
	if o >= 5 {
		ow, oh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2: // mirror horizontal
				dx, dy = w-1-x, y
			case 3: // rotate 180
				dx, dy = w-1-x, h-1-y
			case 4: // mirror vertical
				dx, dy = x, h-1-y
			case 5: // transpose
				dx, dy = y, x
			case 6: // rotate 90 CW
				dx, dy = h-1-y, x
			case 7: // transverse
				dx, dy = h-1-y, w-1-x
			case 8: // rotate 90 CCW
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
