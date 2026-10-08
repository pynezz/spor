// Package media extracts what spor needs from media files without
// dependencies: duration from MP3 frame headers and MP4 boxes, video
// dimensions, cover thumbnails, and (optionally, via ffmpeg) waveform peaks.
package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Info is what a probe learns about a file.
type Info struct {
	Duration float64 `json:"duration,omitempty"` // seconds
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
}

// Probe returns duration and dimensions. It parses MP3 and MP4 natively and
// falls back to ffprobe for other containers when available.
func Probe(path, ext string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Info{}, err
	}
	var info Info
	switch ext {
	case "mp3":
		info.Duration, err = mp3Duration(f, st.Size())
	case "mp4", "m4a", "m4v", "aac":
		info, err = mp4Info(f, st.Size())
	default:
		err = errors.New("no native parser")
	}
	if (err != nil || info.Duration <= 0) && ffprobePath() != "" {
		if d, perr := ffprobeDuration(path); perr == nil {
			info.Duration, err = d, nil
		}
	}
	return info, err
}

var (
	mp3Bitrates = [2][3][16]int{
		{ // MPEG-1: layer I, II, III
			{0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448, 0},
			{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 0},
			{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0},
		},
		{ // MPEG-2 and 2.5
			{0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256, 0},
			{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
			{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
		},
	}
	mp3Rates = map[int][3]int{3: {44100, 48000, 32000}, 2: {22050, 24000, 16000}, 0: {11025, 12000, 8000}}
)

type mp3Frame struct {
	version, layer        int // version: 3=MPEG1, 2=MPEG2, 0=MPEG2.5; layer 1..3
	bitrate, rate, length int
	samples               int
	mono                  bool
}

func parseMP3Header(h []byte) (mp3Frame, bool) {
	if len(h) < 4 || h[0] != 0xFF || h[1]&0xE0 != 0xE0 {
		return mp3Frame{}, false
	}
	var fr mp3Frame
	fr.version = int(h[1]>>3) & 3
	layerBits := int(h[1]>>1) & 3
	if fr.version == 1 || layerBits == 0 {
		return fr, false
	}
	fr.layer = 4 - layerBits
	bi := int(h[2] >> 4)
	si := int(h[2]>>2) & 3
	if bi == 0 || bi == 15 || si == 3 {
		return fr, false
	}
	v := 0
	if fr.version != 3 {
		v = 1
	}
	fr.bitrate = mp3Bitrates[v][fr.layer-1][bi] * 1000
	fr.rate = mp3Rates[fr.version][si]
	pad := int(h[2]>>1) & 1
	fr.mono = h[3]>>6 == 3
	switch {
	case fr.layer == 1:
		fr.samples = 384
		fr.length = (12*fr.bitrate/fr.rate + pad) * 4
	case fr.layer == 3 && fr.version != 3:
		fr.samples = 576
		fr.length = 72*fr.bitrate/fr.rate + pad
	default:
		fr.samples = 1152
		fr.length = 144*fr.bitrate/fr.rate + pad
	}
	return fr, fr.length > 4
}

func mp3Duration(r io.ReaderAt, size int64) (float64, error) {
	var off int64
	hdr := make([]byte, 10)
	if _, err := r.ReadAt(hdr, 0); err == nil && string(hdr[:3]) == "ID3" {
		sz := int64(hdr[6]&0x7f)<<21 | int64(hdr[7]&0x7f)<<14 | int64(hdr[8]&0x7f)<<7 | int64(hdr[9]&0x7f)
		off = 10 + sz
		if hdr[5]&0x10 != 0 {
			off += 10
		}
	}
	// Scan for the first frame whose successor is also a valid frame.
	buf := make([]byte, 64*1024)
	n, _ := r.ReadAt(buf, off)
	buf = buf[:n]
	for i := 0; i+4 <= len(buf); i++ {
		fr, ok := parseMP3Header(buf[i:])
		if !ok {
			continue
		}
		if j := i + fr.length; j+4 <= len(buf) {
			if _, ok2 := parseMP3Header(buf[j:]); !ok2 {
				continue
			}
		}
		start := off + int64(i)
		// Xing/Info or VBRI header in the first frame gives the frame count.
		var side int
		switch {
		case fr.version == 3 && !fr.mono:
			side = 32
		case fr.version == 3 || !fr.mono:
			side = 17
		default:
			side = 9
		}
		if x := i + 4 + side; x+12 <= len(buf) {
			tag := string(buf[x : x+4])
			if tag == "Xing" || tag == "Info" {
				flags := binary.BigEndian.Uint32(buf[x+4:])
				if flags&1 != 0 {
					frames := binary.BigEndian.Uint32(buf[x+8:])
					return float64(frames) * float64(fr.samples) / float64(fr.rate), nil
				}
			}
		}
		if x := i + 4 + 32; x+18 <= len(buf) && string(buf[x:x+4]) == "VBRI" {
			frames := binary.BigEndian.Uint32(buf[x+14:])
			return float64(frames) * float64(fr.samples) / float64(fr.rate), nil
		}
		audio := size - start
		tail := make([]byte, 3)
		if _, err := r.ReadAt(tail, size-128); err == nil && string(tail) == "TAG" {
			audio -= 128
		}
		return float64(audio) * 8 / float64(fr.bitrate), nil
	}
	return 0, errors.New("no mp3 frame found")
}

type box struct {
	typ        string
	start, end int64 // payload range
}

func readBoxes(r io.ReaderAt, start, end int64) ([]box, error) {
	var out []box
	h := make([]byte, 16)
	for pos := start; pos+8 <= end; {
		if _, err := r.ReadAt(h[:8], pos); err != nil {
			return out, err
		}
		size := int64(binary.BigEndian.Uint32(h))
		typ := string(h[4:8])
		hl := int64(8)
		switch size {
		case 1:
			if _, err := r.ReadAt(h[8:16], pos+8); err != nil {
				return out, err
			}
			size = int64(binary.BigEndian.Uint64(h[8:]))
			hl = 16
		case 0:
			size = end - pos
		}
		if size < hl || pos+size > end {
			return out, errors.New("malformed mp4 box")
		}
		out = append(out, box{typ, pos + hl, pos + size})
		pos += size
		if len(out) > 10000 {
			return out, errors.New("too many boxes")
		}
	}
	return out, nil
}

func findBox(bs []box, typ string) (box, bool) {
	for _, b := range bs {
		if b.typ == typ {
			return b, true
		}
	}
	return box{}, false
}

func mp4Info(r io.ReaderAt, size int64) (Info, error) {
	var info Info
	top, err := readBoxes(r, 0, size)
	moov, ok := findBox(top, "moov")
	if !ok {
		if err == nil {
			err = errors.New("no moov box")
		}
		return info, err
	}
	kids, err := readBoxes(r, moov.start, moov.end)
	if err != nil {
		return info, err
	}
	if mvhd, ok := findBox(kids, "mvhd"); ok && mvhd.end-mvhd.start >= 32 {
		p := make([]byte, 32)
		if _, err := r.ReadAt(p, mvhd.start); err != nil {
			return info, err
		}
		if p[0] == 1 {
			ts := binary.BigEndian.Uint32(p[20:])
			d := binary.BigEndian.Uint64(p[24:])
			if ts > 0 {
				info.Duration = float64(d) / float64(ts)
			}
		} else {
			ts := binary.BigEndian.Uint32(p[12:])
			d := binary.BigEndian.Uint32(p[16:])
			if ts > 0 && d != 0xFFFFFFFF {
				info.Duration = float64(d) / float64(ts)
			}
		}
	}
	for _, k := range kids {
		if k.typ != "trak" {
			continue
		}
		tk, err := readBoxes(r, k.start, k.end)
		if err != nil {
			continue
		}
		tkhd, ok := findBox(tk, "tkhd")
		if !ok || tkhd.end-tkhd.start < 84 {
			continue
		}
		p := make([]byte, 8)
		if _, err := r.ReadAt(p, tkhd.end-8); err != nil {
			continue
		}
		w := int(binary.BigEndian.Uint32(p) >> 16)
		hgt := int(binary.BigEndian.Uint32(p[4:]) >> 16)
		if w > 0 && hgt > 0 {
			info.Width, info.Height = w, hgt
			break
		}
	}
	if info.Duration <= 0 {
		return info, errors.New("no duration in mvhd")
	}
	return info, nil
}

var (
	ffmpegOnce  sync.Once
	ffmpegBin   string
	ffprobeBin  string
	ffmpegDisab bool
)

// DisableFFmpeg turns off all ffmpeg use (for tests and minimal servers).
func DisableFFmpeg() { ffmpegDisab = true }

func lookFF() {
	ffmpegOnce.Do(func() {
		if ffmpegDisab {
			return
		}
		ffmpegBin, _ = exec.LookPath("ffmpeg")
		ffprobeBin, _ = exec.LookPath("ffprobe")
	})
}

// HaveFFmpeg reports whether ffmpeg is on PATH.
func HaveFFmpeg() bool { lookFF(); return ffmpegBin != "" }

func ffprobePath() string { lookFF(); return ffprobeBin }

func ffprobeDuration(path string) (float64, error) {
	out, err := runTimeout(30*time.Second, ffprobeBin, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", "--", path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}

func runTimeout(d time.Duration, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			msg := strings.TrimSpace(errb.String())
			if len(msg) > 300 {
				msg = msg[:300]
			}
			return nil, errors.New(name + ": " + err.Error() + ": " + msg)
		}
		return out.Bytes(), nil
	case <-time.After(d):
		_ = cmd.Process.Kill()
		<-done
		return nil, errors.New(name + ": timed out")
	}
}
