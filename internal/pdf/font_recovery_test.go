package pdf

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSimpleFontUsesOneByteCodesWithMalformedCMapCodespace(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /F1 12 Tf (he Pr f) Tj ET"), ""))
	objects[3] = "<< /Type /Font /Subtype /TrueType /BaseFont /Example /Encoding /WinAnsiEncoding /ToUnicode 6 0 R >>"
	objects = append(objects, streamBody([]byte("1 begincodespacerange <0000> <FFFF> endcodespacerange 2 beginbfchar <68> <0068> <66> <00660069> endbfchar"), ""))
	if got := extract(t, fixture(t, objects), 1); got != "he Pr fi" {
		t.Fatalf("got %q", got)
	}
}

func TestSelectingAnUnusedUnmappableFontDoesNotFail(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /Broken 12 Tf ET BT /F1 12 Tf (readable) Tj ET"), ""))
	objects[1] = "<< /Type /Pages /Count 1 /Kids [3 0 R] /Resources << /Font << /F1 4 0 R /Broken 6 0 R >> >> >>"
	objects = append(objects, "<< /Type /Font /Subtype /Type0 /Encoding /Identity-H /DescendantFonts [7 0 R] /ToUnicode 8 0 R >>", "<< /Type /Font /Subtype /CIDFontType2 >>", streamBody([]byte("1 begincodespacerange <0000> <FFFF> endcodespacerange"), ""))
	if got := extract(t, fixture(t, objects), 1); got != "readable" {
		t.Fatalf("got %q", got)
	}
	objects[4] = streamBody([]byte("BT /Broken 12 Tf <0001> Tj ET"), "")
	e, _ := New()
	defer e.Close()
	if got, err := e.PageText(context.Background(), fixture(t, objects), 1); err == nil || got != "" || !strings.Contains(err.Error(), "no Unicode mapping") {
		t.Fatalf("guessed a CID: %q, %v", got, err)
	}
	objects[4] = streamBody([]byte("BT /Broken 12 Tf /Span << /ActualText (replacement) >> BDC <0001> Tj EMC ET"), "")
	if got := extract(t, fixture(t, objects), 1); got != "replacement" {
		t.Fatalf("ActualText: %q", got)
	}
}

func TestEmptySimpleUnicodeMapFallsBackToExplicitEncoding(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /F1 12 Tf (readable) Tj ET"), ""))
	objects[3] = "<< /Type /Font /Subtype /TrueType /BaseFont /Example /Encoding /WinAnsiEncoding /ToUnicode 6 0 R >>"
	objects = append(objects, streamBody([]byte("1 begincodespacerange <0000> <FFFF> endcodespacerange"), ""))
	if got := extract(t, fixture(t, objects), 1); got != "readable" {
		t.Fatalf("got %q", got)
	}
}

func TestMathematicalPiGlyphAliasIsFontSpecific(t *testing.T) {
	for _, fontName := range []string{"MathematicalPi-Three", "CCPNGE+MathematicalPi-Three", "Unrelated"} {
		objects := basicObjects(streamBody([]byte("BT /F1 12 Tf <02> Tj (100%) Tj ET"), ""))
		objects[3] = fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /%s /Encoding << /Differences [2 /H11011] >> >>", fontName)
		e, _ := New()
		got, err := e.PageText(context.Background(), fixture(t, objects), 1)
		e.Close()
		if fontName == "Unrelated" {
			if err == nil || got != "" {
				t.Fatalf("interpreted unrelated symbol: %q", got)
			}
			continue
		}
		if err != nil || got != "~100%" {
			t.Fatalf("%s: %q, %v", fontName, got, err)
		}
	}
}

func TestUnresolvedCrossReferenceAfterRecoveryInvalidatesDocument(t *testing.T) {
	path := fixture(t, basicObjects(streamBody([]byte("BT /F1 12 Tf (text) Tj ET"), "")))
	e, _ := New()
	defer e.Close()
	if _, err := e.PageCount(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	e.doc.recovery = "earlier recovery"
	e.doc.xref[5] = xrefEntry{kind: 1, offset: 0}
	text, err := e.PageText(context.Background(), path, 1)
	var structural *DocumentError
	if text != "" || !errors.As(err, &structural) {
		t.Fatalf("structural error was treated as page-local: %q, %v", text, err)
	}
}
