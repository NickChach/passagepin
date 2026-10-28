package pdf

import (
	"bytes"
	"context"
	"encoding/ascii85"
	"strings"
	"testing"
)

func TestInlineImagesPreserveSurroundingText(t *testing.T) {
	raw := []byte(" EI BT (must not become text) Tj ET ")
	var encoded bytes.Buffer
	encoder := ascii85.NewEncoder(&encoded)
	encoder.Write(raw)
	encoder.Close()
	for _, tc := range []struct {
		name, options string
		data          []byte
	}{
		{"binary EI is not a terminator", "/W 37 /H 1 /CS /G /BPC 8", append(raw, bytes.Repeat([]byte{0}, 37-len(raw))...)},
		{"Flate", "/W 1 /H 1 /CS /G /BPC 8 /F /Fl", flate(t, raw)},
		{"ASCII85 filter array", "/F [/A85 /Fl]", append(encoded.Bytes(), '~', '>')},
		{"ASCIIHex", "/F /AHx", []byte("45 49 20>")},
		{"RunLength", "/F /RL", append(append([]byte{byte(len(raw) - 1)}, raw...), 128)},
		{"mask with row padding", "/W 3 /H 2 /IM true", []byte{0, 255}},
		{"RGB abbreviation", "/W 1 /H 1 /CS /RGB /BPC 8", []byte{0, 1, 2}},
		{"JPEG marker in APP segment", "/F /DCT", []byte{0xff, 0xd8, 0xff, 0xe0, 0, 4, 0xff, 0xd9, 0xff, 0xd9}},
		{"JPEG stuffed entropy", "/F /DCT", []byte{0xff, 0xd8, 0xff, 0xda, 0, 2, 1, 0xff, 0, 0xd9, 0xff, 0xd0, 2, 0xff, 0xd9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := []byte("BT /F1 12 Tf 40 700 Td (before) Tj ET q BI " + tc.options + " ID\r\n")
			content = append(content, tc.data...)
			content = append(content, []byte(" EI Q BT /F1 12 Tf 40 680 Td (after) Tj ET")...)
			if got := extract(t, fixture(t, basicObjects(streamBody(content, ""))), 1); got != "before after" {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestInlineImagesInsideFormsAndNamedColourSpaces(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /F1 12 Tf (before) Tj ET /Picture Do BT /F1 12 Tf 40 680 Td (after) Tj ET"), ""))
	objects[1] = "<< /Type /Pages /Count 1 /Kids [3 0 R] /Resources << /Font << /F1 4 0 R >> /XObject << /Picture 6 0 R >> >> >>"
	objects = append(objects, streamBody([]byte("q BI /W 1 /H 1 /CS /Pixels /BPC 8 ID abc EI Q"), "/Type /XObject /Subtype /Form /BBox [0 0 10 10] /Resources << /ColorSpace << /Pixels /DeviceRGB >> >>"))
	if got := extract(t, fixture(t, objects), 1); got != "before after" {
		t.Fatalf("got %q", got)
	}
}

func TestMultipleInlineImagesAcrossPageStreams(t *testing.T) {
	objects := basicObjects(streamBody([]byte("q BI /W 1 /H 1 /CS /G /BPC 8 ID x EI Q BT /F1 12 Tf (first) Tj ET"), ""))
	objects[2] = "<< /Type /Page /Parent 2 0 R /Contents [5 0 R 6 0 R] >>"
	objects = append(objects, streamBody([]byte("q BI /W 1 /H 1 /CS /G /BPC 8 ID y EI BI /W 1 /H 1 /CS /G /BPC 8 ID z EI Q BT /F1 12 Tf 40 680 Td (last) Tj ET"), ""))
	if got := extract(t, fixture(t, objects), 1); got != "first last" {
		t.Fatalf("got %q", got)
	}
}

func TestMalformedInlineImagesNeverReturnPartialText(t *testing.T) {
	for _, image := range []string{
		"BI /W 8 /H 1 /CS /G /BPC 8 ID x EI",
		"BI /W 1 /H 1 /CS /G /BPC 8 ID x garbage EI",
		"BI /W 1 /H 1 /CS /G /BPC 8 ID x EIx",
		"BI /W 1 /H 1 /CS /G /BPC 8 ID x",
		"BI /W 1 /H 1 /CS /G /BPC 3 ID x EI",
		"BI /W 999999999 /H 999999999 /CS /RGB /BPC 16 ID x EI",
		"BI /F /Fl ID not-zlib EI",
		"BI /F /A85 ID abc EI",
		"BI /F /AHx ID xyz> EI",
		"BI /F /DCT ID not-jpeg EI",
		"BI /F /UnsupportedDecode ID x EI",
		"BI /W 1 /H 1 /CS /Missing /BPC 8 ID x EI",
		"BI /W 1 /H 1 /CS /Recursive /BPC 8 ID x EI",
	} {
		t.Run(image, func(t *testing.T) {
			objects := basicObjects(streamBody([]byte("BT /F1 12 Tf (before) Tj ET "+image), ""))
			objects[1] = "<< /Type /Pages /Count 1 /Kids [3 0 R] /Resources << /Font << /F1 4 0 R >> /ColorSpace << /Recursive /Recursive >> >> >>"
			e, _ := New()
			defer e.Close()
			got, err := e.PageText(context.Background(), fixture(t, objects), 1)
			if err == nil || got != "" {
				t.Fatalf("accepted malformed image: %q, %v", got, err)
			}
		})
	}
}

func TestInlineImageDecodedLimitAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &interpreter{d: &document{ctx: ctx}}
	if _, err := r.inlineImageLength(dict{"F": name("Fl")}, flate(t, []byte("pixels"))); err != context.Canceled {
		t.Fatalf("lost cancellation: %v", err)
	}
	r.d.ctx = context.Background()
	if err := r.discardImage(strings.NewReader(strings.Repeat("x", maxStream+1))); err == nil {
		t.Fatal("accepted oversized decoded image")
	}
}
