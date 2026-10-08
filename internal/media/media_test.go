package media

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestMP3CBRDuration(t *testing.T) {
	// 128 kbps, 44.1 kHz MPEG-1 Layer III, no padding: 417-byte frames.
	frame := make([]byte, 417)
	copy(frame, []byte{0xFF, 0xFB, 0x90, 0x00})
	var buf bytes.Buffer
	for i := 0; i < 1000; i++ {
		buf.Write(frame)
	}
	d, err := mp3Duration(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if d < 26 || d > 26.2 { // 1000 * 1152 / 44100 = 26.12 s
		t.Fatalf("duration %v", d)
	}
}

func TestPeaks(t *testing.T) {
	pcm := make([]byte, 2*8000)
	for i := 0; i < 8000; i++ {
		v := int16(1000)
		if i%2 == 1 {
			v = -2000
		}
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(v))
	}
	p, err := PeaksFromPCM(pcm)
	if err != nil || len(p) != 2*PeakBins {
		t.Fatal(err, len(p))
	}
	if int8(p[0]) != -2000>>8 || int8(p[1]) != 1000>>8 {
		t.Fatalf("peaks %d %d", int8(p[0]), int8(p[1]))
	}
}
