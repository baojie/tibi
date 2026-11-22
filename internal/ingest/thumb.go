package ingest

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"

	"github.com/gen2brain/webp"
	xdraw "golang.org/x/image/draw"
)

// ThumbWidth is the width of generated thumbnails.
const ThumbWidth = 400

// maxThumbBytes guards against decompression bombs: images whose decoded
// pixels would take more memory than this get no thumbnail (they are still
// stored and served). A tiny PNG can claim 10000×10000 at 16 bits per
// channel, which decodes to 800 MB; counting bytes, not pixels, catches it.
// The limit admits a 48-megapixel baseline phone JPEG (3 bytes per pixel)
// and a 12-megapixel progressive one, but not an RGBA image beyond ~50 MP.
const maxThumbBytes = 192 << 20

// thumbSlots bounds how many images are decoded at once, so parallel
// uploads cannot multiply that memory.
var thumbSlots = make(chan struct{}, 1)

// decodedBytes estimates the memory image.Decode needs for an image.
func decodedBytes(cfg image.Config, progressiveJPEG bool) int64 {
	bpp := int64(4)
	switch cfg.ColorModel {
	case color.RGBA64Model, color.NRGBA64Model:
		bpp = 8
	case color.Gray16Model:
		bpp = 2
	case color.GrayModel, color.AlphaModel:
		bpp = 1
	case color.YCbCrModel, color.CMYKModel:
		bpp = 3
	}
	if progressiveJPEG {
		bpp += 12 // Go keeps every coefficient as int32: 4 B/px for each of 3 components (4:4:4)
	}
	return int64(cfg.Width) * int64(cfg.Height) * bpp
}

// isProgressiveJPEG reports whether a JPEG uses progressive coding (an SOF2
// marker before the first scan).
func isProgressiveJPEG(b []byte) bool {
	for i := 2; i+3 < len(b); {
		if b[i] != 0xFF {
			return false
		}
		m := b[i+1]
		switch {
		case m == 0xC2:
			return true
		case m == 0xDA: // start of scan: no SOF2 before it
			return false
		case m == 0xFF:
			i++
			continue
		}
		i += 2 + (int(b[i+2])<<8 | int(b[i+3])) // parenthesized: | binds like +
	}
	return false
}

// errNoThumb means no thumbnail can be made for this image.
var errNoThumb = errors.New("no thumbnail for this image")

// ErrNoThumb reports that MakeThumb cannot make a thumbnail for an image
// (its type has no decoder, or it is too large to decode safely).
func ErrNoThumb(err error) bool { return errors.Is(err, errNoThumb) }

// MaxThumbSource is the largest image a thumbnail is made from.
const MaxThumbSource = 100 << 20

// Thumbable reports whether a thumbnail can be made for an image of this
// type and size (decoding may still refuse it, see maxThumbBytes).
func Thumbable(mt string, size int64) bool {
	switch mt {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return size <= MaxThumbSource
	}
	return false // e.g. AVIF: no pure-Go decoder
}

// MakeThumb renders a WebP thumbnail at most ThumbWidth wide, applying the
// JPEG EXIF orientation so it displays upright.
func MakeThumb(data []byte, mt string) ([]byte, error) {
	if !Thumbable(mt, int64(len(data))) {
		return nil, errNoThumb
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || decodedBytes(cfg, mt == "image/jpeg" && isProgressiveJPEG(data)) > maxThumbBytes {
		return nil, errNoThumb
	}
	thumbSlots <- struct{}{}
	defer func() { <-thumbSlots }()
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
