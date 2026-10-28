package pdf

import (
	"context"
	"strings"
	"testing"
)

func TestActualTextScopesAndPositioning(t *testing.T) {
	for _, tc := range []struct{ name, content, want string }{
		{"empty replacement", `(pre) Tj /Span << /ActualText () >> BDC (hidden) Tj EMC (post) Tj`, "prepost"},
		{"empty UTF16", `/Span << /ActualText <FEFF> >> BDC (hidden) Tj EMC (kept) Tj`, "kept"},
		{"outer replacement", `/Span << /ActualText (outer) >> BDC /Span << /ActualText (inner) >> BDC (x) Tj EMC (y) Tj EMC`, "outer"},
		{"ordinary marked content", `/Part BMC (kept) Tj EMC /Span << /Lang (en) >> BDC ( text) Tj EMC`, "kept text"},
		{"position after replacement", `/Span << /ActualText (replacement) >> BDC (abc) Tj (def) Tj EMC (next) Tj`, "replacementnext"},
		{"separated span", `(before) Tj 200 0 Td /Span << /ActualText (after) >> BDC (x) Tj EMC`, "before after"},
		{"painted paths", `/Span << /ActualText (diagram text) >> BDC 10 10 20 20 re f EMC`, "diagram text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := fixture(t, basicObjects(streamBody([]byte("BT /F1 12 Tf 40 700 Td "+tc.content+" ET"), "")))
			if got := extract(t, path, 1); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestActualTextAcrossContentStreams(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /F1 12 Tf /Span << /ActualText (replacement) >> BDC (x) Tj"), ""))
	objects[2] = "<< /Type /Page /Parent 2 0 R /Contents [5 0 R 6 0 R] >>"
	objects = append(objects, streamBody([]byte("(y) Tj EMC ( after) Tj ET"), ""))
	if got := extract(t, fixture(t, objects), 1); got != "replacement after" {
		t.Fatalf("got %q", got)
	}
}

func TestActualTextPreservesGlyphAdvances(t *testing.T) {
	// Courier glyphs are 600 units. At size 10, with character spacing 1,
	// word spacing 3 and horizontal scale .5, "a b" advances 12 points,
	// the -200 adjustment adds 1 point, and "c" advances 3.5 points.
	// An absolute next position at the resulting 16.5-point end must join
	// the replacement without a fabricated word break.
	objects := basicObjects(streamBody([]byte("BT /F1 10 Tf 40 700 Td 1 Tc 3 Tw 50 Tz /Span << /ActualText (word) >> BDC [(a b) -200 (c)] TJ EMC 16.5 0 Td (end) Tj ET"), ""))
	objects[3] = "<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>"
	if got := extract(t, fixture(t, objects), 1); got != "wordend" {
		t.Fatalf("covered glyph advances changed word boundaries: %q", got)
	}
}

func TestMalformedMarkedContentFailsWithoutPartialText(t *testing.T) {
	for _, tail := range []string{
		"EMC", "/Span BMC", "/Span << /ActualText (open) >> BDC",
		"(tag) BMC EMC", "/Span /Missing BDC EMC",
		"/Span << /ActualText 42 >> BDC EMC", "/Span << /ActualText <FEFF0041D800> >> BDC EMC",
		"/Span << /ActualText <FEFF00> >> BDC EMC", "/Span << /ActualText <7F> >> BDC EMC",
		strings.Repeat("/Span BMC ", maxDepth+1) + strings.Repeat("EMC ", maxDepth+1),
	} {
		e, _ := New()
		path := fixture(t, basicObjects(streamBody([]byte("BT /F1 12 Tf (before) Tj ET "+tail), "")))
		got, err := e.PageText(context.Background(), path, 1)
		e.Close()
		if err == nil || got != "" {
			t.Fatalf("accepted malformed marked content %q: %q, %v", tail, got, err)
		}
	}
}

func TestMarkedContentCannotCloseCallerScopeInForm(t *testing.T) {
	objects := basicObjects(streamBody([]byte("/Span BMC /Section Do EMC"), ""))
	objects[1] = "<< /Type /Pages /Count 1 /Kids [3 0 R] /Resources << /XObject << /Section 6 0 R >> >> >>"
	objects = append(objects, streamBody([]byte("EMC /Span BMC"), "/Type /XObject /Subtype /Form /BBox [0 0 100 100]"))
	e, _ := New()
	defer e.Close()
	got, err := e.PageText(context.Background(), fixture(t, objects), 1)
	if err == nil || got != "" || !strings.Contains(err.Error(), "EMC without") {
		t.Fatalf("crossed form scope: %q, %v", got, err)
	}
}

func TestActualTextStringEncodingsAndLimits(t *testing.T) {
	for _, tc := range []struct {
		raw  []byte
		want string
	}{
		{[]byte{0x18, 0x1f, 0x80, 0x8a, 0x93, 0x94, 0x9e, 0xa0, 0xe9}, "˘˜•−ﬁﬂž€é"},
		{[]byte{0xfe, 0xff, 0, 'A', 0xd8, 0x3d, 0xde, 0}, "A😀"},
		{nil, ""},
	} {
		got, err := decodeTextString(tc.raw)
		if err != nil || got != tc.want {
			t.Fatalf("%x: got %q, %v; want %q", tc.raw, got, err, tc.want)
		}
	}
	for _, raw := range [][]byte{{0x9f}, {0xad}, {0xff, 0xfe}, {0xef, 0xbb, 0xbf, 'A'}, {0xfe, 0xff, 0xdc, 0}, []byte(strings.Repeat("a", maxToken+1))} {
		if _, err := decodeTextString(raw); err == nil {
			t.Fatalf("accepted invalid/oversized text string (length %d)", len(raw))
		}
	}
	// Replacements share the ordinary page-output budget, including when a
	// sequence has no text-show operation and emits on EMC instead.
	r := &interpreter{out: &textOutput{}}
	r.out.text.WriteString(strings.Repeat("a", maxStream-1))
	r.marked = []markedContent{{own: &replacementText{text: "ab", start: textState{ctm: identity(), tm: identity()}}}}
	if err := r.endMarked(); err == nil || !strings.Contains(err.Error(), "text too large") {
		t.Fatalf("replacement bypassed output limit: %v", err)
	}
}
