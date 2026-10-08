package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png" // register decoder for cover.png
	"os"
	"time"
)

// PeakBins is the number of min/max pairs in a peaks file.
const PeakBins = 1000

// Peaks decodes audio with ffmpeg and returns PeakBins min/max pairs as int8,
// interleaved: min0, max0, min1, max1, ...
func Peaks(path string) ([]byte, error) {
	if !HaveFFmpeg() {
		return nil, errors.New("ffmpeg not available")
	}
	pcm, err := runTimeout(2*time.Minute, ffmpegBin, "-nostdin", "-v", "error", "-i", path,
		"-vn", "-ac", "1", "-ar", "8000", "-f", "s16le", "-")
	if err != nil {
		return nil, err
	}
	return PeaksFromPCM(pcm)
}

// PeaksFromPCM reduces little-endian signed 16-bit mono PCM to peaks.
func PeaksFromPCM(pcm []byte) ([]byte, error) {
	n := len(pcm) / 2
	if n < PeakBins {
		return nil, errors.New("audio too short for peaks")
	}
	out := make([]byte, PeakBins*2)
	for b := 0; b < PeakBins; b++ {
		lo, hi := b*n/PeakBins, (b+1)*n/PeakBins
		mn, mx := int16(0), int16(0)
		for i := lo; i < hi; i++ {
			s := int16(binary.LittleEndian.Uint16(pcm[2*i:]))
			if s < mn {
				mn = s
			}
			if s > mx {
				mx = s
			}
		}
		out[2*b] = byte(int8(mn >> 8))
		out[2*b+1] = byte(int8(mx >> 8))
	}
	return out, nil
}

// VideoFrame grabs a still from a video as JPEG, for tracks without a cover.
func VideoFrame(path string) ([]byte, error) {
	if !HaveFFmpeg() {
		return nil, errors.New("ffmpeg not available")
	}
	args := func(seek bool) []string {
		a := []string{"-nostdin", "-v", "error"}
		if seek {
			a = append(a, "-ss", "1")
		}
		return append(a, "-i", path, "-frames:v", "1", "-q:v", "3", "-f", "image2", "-c:v", "mjpeg", "-")
	}
	out, err := runTimeout(time.Minute, ffmpegBin, args(true)...)
	if err != nil || len(out) == 0 {
		out, err = runTimeout(time.Minute, ffmpegBin, args(false)...)
	}
	if err == nil && len(out) == 0 {
		err = errors.New("ffmpeg produced no frame")
	}
	return out, err
}

// Thumbnail decodes a JPEG or PNG and downscales it so that its longest side
// is at most size, returning JPEG bytes and the final dimensions.
func Thumbnail(src []byte, size int) ([]byte, int, int, error) {
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, 0, 0, err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 || w > 20000 || h > 20000 {
		return nil, 0, 0, errors.New("unreasonable image size")
	}
	dw, dh := w, h
	if w >= h && w > size {
		dw, dh = size, max(1, h*size/w)
	} else if h > w && h > size {
		dw, dh = max(1, w*size/h), size
	}
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	dst := boxResize(rgba, dw, dh)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), dw, dh, nil
}

// boxResize downsamples by averaging the source pixels that fall in each
// destination pixel. Good quality for downscaling, which is all we do.
func boxResize(src *image.RGBA, dw, dh int) *image.RGBA {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	if sw == dw && sh == dh {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		y0, y1 := y*sh/dh, max((y+1)*sh/dh, y*sh/dh+1)
		for x := 0; x < dw; x++ {
			x0, x1 := x*sw/dw, max((x+1)*sw/dw, x*sw/dw+1)
			var r, g, bl, a, n uint32
			for sy := y0; sy < y1; sy++ {
				row := src.Pix[sy*src.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					r += uint32(p[0])
					g += uint32(p[1])
					bl += uint32(p[2])
					a += uint32(p[3])
					n++
				}
			}
			d := dst.Pix[y*dst.Stride+x*4:]
			d[0], d[1], d[2], d[3] = uint8(r/n), uint8(g/n), uint8(bl/n), uint8(a/n)
		}
	}
	return dst
}

// WriteFileAtomic writes via a temp file and rename.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
