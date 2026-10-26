package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestDecodedTextBudget(t *testing.T) {
	mapping := strings.Repeat("A", 512)
	f := &font{unicode: &unicodeMap{
		codes:  map[string]string{"A": mapping},
		ranges: []codeRange{{[]byte{0}, []byte{255}}},
	}}
	for _, tc := range []struct {
		raw    string
		budget int
		valid  bool
	}{
		{"AA", 1024, true}, {"AAA", 1024, false},
		{"A", 511, false}, {"A", 0, false},
	} {
		got, _, _, _, err := f.decode([]byte(tc.raw), tc.budget)
		if tc.valid {
			if err != nil || got != strings.Repeat(mapping, len(tc.raw)) {
				t.Fatalf("exact budget: len=%d err=%v", len(got), err)
			}
		} else if err == nil || got != "" {
			t.Fatalf("accepted %d bytes with budget %d: len=%d err=%v", len(tc.raw)*512, tc.budget, len(got), err)
		}
	}
}

func TestPageTextBudgetAcrossShowOperations(t *testing.T) {
	cmap := []byte("1 beginbfchar <41> <" + strings.Repeat("0041", 512) + "> endbfchar")
	// Each show operation fits on its own, but the final TJ item would put
	// their combined expansion beyond the page's 64 MiB limit.
	content := "BT /F1 12 Tf (" + strings.Repeat("A", maxStream/512-1) + ") Tj [(AA)] TJ ET"
	objects := basicObjects(streamBody([]byte(content), ""))
	objects[3] = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /ToUnicode 6 0 R >>"
	objects = append(objects, streamBody(cmap, ""))
	e, _ := New()
	defer e.Close()
	if _, err := e.PageText(context.Background(), fixture(t, objects), 1); err == nil || !strings.Contains(err.Error(), "text too large") {
		t.Fatalf("expected cumulative text limit, got %v", err)
	}
}

func TestTruncatedPageContentFailsExtraction(t *testing.T) {
	for _, tail := range []string{"[(after)", "<< /ActualText (after)"} {
		t.Run(tail, func(t *testing.T) {
			objects := basicObjects(streamBody([]byte("BT /F1 12 Tf (before) Tj ET "+tail), ""))
			e, _ := New()
			defer e.Close()
			text, err := e.PageText(context.Background(), fixture(t, objects), 1)
			if !errors.Is(err, io.ErrUnexpectedEOF) || text != "" {
				t.Fatalf("accepted truncated content: text=%q err=%v", text, err)
			}
		})
	}
}

func TestSimpleFontIndirectWidths(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /F1 12 Tf 40 700 Td (AB) Tj ET"), ""))
	objects[3] = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding /FirstChar 8 0 R /LastChar 66 /Widths 7 0 R >>"
	objects = append(objects, "667", "[6 0 R 9 0 R]", "65", "700.5")
	path := fixture(t, objects)
	if got := extract(t, path, 1); got != "AB" {
		t.Fatalf("indirect widths: got %q", got)
	}
	e, _ := New()
	defer e.Close()
	d, err := e.open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := d.loadFont(reference{4, 0})
	if err != nil || f.widths[65] != 667 || f.widths[66] != 700.5 {
		t.Fatalf("resolved widths: font=%+v err=%v", f, err)
	}
	objects[8] = "(not a width)"
	if _, err := e.PageText(context.Background(), fixture(t, objects), 1); err == nil {
		t.Fatal("accepted a nonnumeric indirect width")
	}
}

func TestStandardFontPositionedGlyphs(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, i int
	}{
		{"Helvetica", 944, 278}, {"Helvetica-Oblique", 944, 278},
		{"Helvetica-Bold", 944, 278}, {"Helvetica-BoldOblique", 944, 278},
		{"Times-Roman", 944, 333}, {"Times-Bold", 1000, 389},
		{"Times-Italic", 833, 333}, {"Times-BoldItalic", 889, 389},
		{"Courier", 600, 600}, {"Courier-Bold", 600, 600},
		{"Courier-Oblique", 600, 600}, {"Courier-BoldOblique", 600, 600},
		{"ABCDEF+Helvetica", 944, 278},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var content bytes.Buffer
			content.WriteString("BT /F1 12 Tf ")
			x := 40.0
			for _, glyph := range "WWW III" {
				if glyph == ' ' {
					x += 8
					continue
				}
				fmt.Fprintf(&content, "1 0 0 1 %.3f 700 Tm (%c) Tj ", x, glyph)
				width := tc.w
				if glyph == 'I' {
					width = tc.i
				}
				x += float64(width) * .012
			}
			content.WriteString("ET")
			objects := basicObjects(streamBody(content.Bytes(), ""))
			objects[3] = fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /%s /Encoding /WinAnsiEncoding >>", tc.name)
			if got := extract(t, fixture(t, objects), 1); got != "WWW III" {
				t.Fatalf("positioned text: got %q", got)
			}
		})
	}
}

func TestStandardFontReencodingAndExplicitWidths(t *testing.T) {
	for _, tc := range []struct {
		font, content, want string
	}{
		{
			"/BaseFont /Helvetica /Encoding << /BaseEncoding /WinAnsiEncoding /Differences [65 /W] >>",
			"BT /F1 12 Tf 40 700 Td (A) Tj 1 0 0 1 51.328 700 Tm (A) Tj ET", "WW",
		},
		{
			"/BaseFont /Helvetica /FirstChar 87 /LastChar 87 /Widths [1200]",
			"BT /F1 12 Tf 40 700 Td (W) Tj 1 0 0 1 54.4 700 Tm (W) Tj ET", "WW",
		},
		{
			"/BaseFont /Helvetica /Encoding /WinAnsiEncoding",
			"BT /F1 12 Tf 40 700 Td <E9> Tj 1 0 0 1 46.672 700 Tm <E9> Tj ET", "éé",
		},
	} {
		objects := basicObjects(streamBody([]byte(tc.content), ""))
		objects[3] = "<< /Type /Font /Subtype /Type1 " + tc.font + " >>"
		if got := extract(t, fixture(t, objects), 1); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.font, got, tc.want)
		}
	}
}

func TestStandardSymbolicFontWidths(t *testing.T) {
	for _, tc := range []struct {
		font, unicode string
		code, width   int
	}{
		{"Symbol", "0391", 65, 722},
		{"ZapfDingbats", "2701", 33, 974},
	} {
		objects := basicObjects(streamBody([]byte(fmt.Sprintf("BT /F1 12 Tf 40 700 Td <%02x> Tj 1 0 0 1 %.3f 700 Tm <%02x> Tj ET", tc.code, 40+float64(tc.width)*.012, tc.code)), ""))
		objects[3] = fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /%s /ToUnicode 6 0 R >>", tc.font)
		objects = append(objects, streamBody([]byte(fmt.Sprintf("1 beginbfchar <%02x> <%s> endbfchar", tc.code, tc.unicode)), ""))
		got := extract(t, fixture(t, objects), 1)
		if strings.Contains(got, " ") || got == "" {
			t.Fatalf("%s adjacent glyphs split: %q", tc.font, got)
		}
	}
}
