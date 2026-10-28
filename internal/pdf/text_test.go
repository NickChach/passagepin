package pdf

import (
	"fmt"
	"testing"
)

func TestTextSpacingAcrossTransforms(t *testing.T) {
	for _, tc := range []struct{ name, transform string }{
		{"upright", "1 0 0 1 200 200 Tm"},
		{"rotated 90", "0 1 -1 0 200 200 Tm"},
		{"rotated 180", "-1 0 0 -1 200 200 Tm"},
		{"rotated 270", "0 -1 1 0 200 200 Tm"},
		{"scaled down", "0.1 0 0 0.1 200 200 Tm"},
		{"scaled up", "10 0 0 10 200 200 Tm"},
		{"nonuniform scale", "0.1 0 0 3 200 200 Tm"},
		{"sheared", "1 0.5 0.25 1 200 200 Tm"},
		{"mirrored", "-1 0 0 1 200 200 Tm"},
		{"negative horizontal scale", "-100 Tz 1 0 0 1 200 200 Tm"},
		{"small horizontal scale", "10 Tz 1 0 0 1 200 200 Tm"},
		{"small font with compensating matrix", "/F1 1.2 Tf 10 0 0 10 200 200 Tm"},
		{"negative font size", "/F1 -12 Tf 1 0 0 1 200 200 Tm"},
		{"combined graphics and text matrices", "0 0.1 -0.1 0 0 0 cm -1 0 0 -1 200 200 Tm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, text := range []struct{ name, content, want string }{
				{"word gap", "[(hello) -250 (world)] TJ", "hello world"},
				{"joined fragments", "[(hel) (lo)] TJ", "hello"},
				{"small kerning gap", "[(hel) -50 (lo)] TJ", "hello"},
				{"next line", "(hello) Tj 0 -14 Td (world) Tj", "hello world"},
				{"backward line reset", "(hello) Tj 1 0 0 1 0 0 Tm (world) Tj", "hello world"},
			} {
				t.Run(text.name, func(t *testing.T) {
					content := fmt.Sprintf("BT /F1 12 Tf %s %s ET", tc.transform, text.content)
					if got := extract(t, fixture(t, basicObjects(streamBody([]byte(content), ""))), 1); got != text.want {
						t.Fatalf("got %q, want %q", got, text.want)
					}
				})
			}
		})
	}
}

func TestTextSpacingInsideTransformedForm(t *testing.T) {
	objects := basicObjects(streamBody([]byte("/Rotated Do"), ""))
	objects[1] = "<< /Type /Pages /Count 1 /Kids [3 0 R] /Resources << /XObject << /Rotated 6 0 R >> >> >>"
	objects = append(objects, streamBody([]byte("BT /F1 12 Tf 20 20 Td [(hello) -250 (world)] TJ ET"),
		"/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Matrix [0 0.1 -0.1 0 100 100] /Resources << /Font << /F1 4 0 R >> >>"))
	if got := extract(t, fixture(t, objects), 1); got != "hello world" {
		t.Fatalf("got %q, want %q", got, "hello world")
	}
}
