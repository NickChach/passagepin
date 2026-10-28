package pdf

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPublisherGlyphMappingsAreFontSpecific(t *testing.T) {
	for _, tc := range []struct{ family, glyph, want string }{
		{"ZapfDingbats", "a73", "■"},
		{"ZapfDingbats", "a1", "✁"},
		{"MathematicalPi-Three", "H20848", "∫"},
		{"Universal-GreekwithMathPi", "H11001", "+"},
		{"Universal-GreekwithMathPi", "H11002", "−"},
		{"Universal-GreekwithMathPi", "H11005", "="},
		{"Universal-GreekwithMathPi", "H11032", "′"},
		{"AdvPSSym", "C211", "☆"},
		{"AdvP4C4E74", "C3", "*"},
	} {
		for _, family := range []string{tc.family, "ABCDEF+" + tc.family, "Unrelated"} {
			t.Run(family+"/"+tc.glyph, func(t *testing.T) {
				objects := basicObjects(streamBody([]byte("BT /F1 12 Tf (before ) Tj <02> Tj ( after) Tj ET"), ""))
				objects[3] = fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /%s /Encoding << /Differences [2 /%s] >> >>", family, tc.glyph)
				e, _ := New()
				defer e.Close()
				got, err := e.PageText(context.Background(), fixture(t, objects), 1)
				if family == "Unrelated" {
					var unsupported *UnsupportedFeatureError
					if got != "" || !errors.As(err, &unsupported) || unsupported.Feature != "missing-unicode-map" {
						t.Fatalf("guessed unrelated glyph: %q, %v", got, err)
					}
					return
				}
				if err != nil || got != "before "+tc.want+" after" {
					t.Fatalf("text=%q err=%v", got, err)
				}
			})
		}
	}
}

func TestPublisherGlyphToUnicodeTakesPrecedence(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /F1 12 Tf <02> Tj ET"), ""))
	objects[3] = "<< /Type /Font /Subtype /Type1 /BaseFont /ABCDEF+ZapfDingbats /Encoding << /Differences [2 /a73] >> /ToUnicode 6 0 R >>"
	objects = append(objects, streamBody([]byte("1 beginbfchar <02> <0058> endbfchar"), ""))
	if got := extract(t, fixture(t, objects), 1); got != "X" {
		t.Fatalf("overrode ToUnicode: %q", got)
	}
}

func TestPartialToUnicodeFallsBackToEmbeddedType1Encoding(t *testing.T) {
	program := "/Encoding 256 array 0 1 255 {1 index exch /.notdef put} for dup 65 /B put dup 66 /C put dup 67 /unknownGlyph put readonly def currentfile eexec"
	for _, tc := range []struct {
		codes, want string
		fail        bool
	}{
		{"410042", "A�C", false}, // ToUnicode wins; explicit .notdef survives; built-in C is recovered.
		{"43", "", true},         // A named but unknown glyph is not an undefined slot.
	} {
		objects := basicObjects(streamBody([]byte("BT /F1 12 Tf <"+tc.codes+"> Tj ET"), ""))
		objects[3] = "<< /Type /Font /Subtype /Type1 /BaseFont /Example /ToUnicode 6 0 R /FontDescriptor 7 0 R >>"
		objects = append(objects, streamBody([]byte("1 beginbfchar <41> <0041> endbfchar"), ""), "<< /Type /FontDescriptor /FontFile 8 0 R >>", streamBody([]byte(program), ""))
		e, _ := New()
		defer e.Close()
		got, err := e.PageText(context.Background(), fixture(t, objects), 1)
		if tc.fail {
			if got != "" || err == nil {
				t.Fatalf("unknown glyph: %q, %v", got, err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("text=%q want=%q err=%v", got, tc.want, err)
		}
	}
}

func TestType1EncodingWithoutDefaultInitializer(t *testing.T) {
	enc, err := type1Encoding([]byte("/Encoding 256 array dup 65 /A put readonly def currentfile eexec"))
	if err != nil || enc[65] != "A" {
		t.Fatalf("encoding=%v err=%v", enc, err)
	}
	if _, ok := enc[0]; ok {
		t.Fatal("invented an undefined slot without the initializer")
	}
}

func TestType1FallbackIsLazy(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /F1 12 Tf (A) Tj ET"), ""))
	objects[3] = "<< /Type /Font /Subtype /Type1 /ToUnicode 6 0 R /FontDescriptor 7 0 R >>"
	objects = append(objects, streamBody([]byte("1 beginbfchar <41> <0041> endbfchar"), ""), "<< /FontFile (not a stream) >>")
	if got := extract(t, fixture(t, objects), 1); got != "A" {
		t.Fatalf("text=%q", got)
	}
	objects[4] = streamBody([]byte("BT /F1 12 Tf (B) Tj ET"), "")
	e, _ := New()
	defer e.Close()
	if got, err := e.PageText(context.Background(), fixture(t, objects), 1); got != "" || err == nil || !strings.Contains(err.Error(), "FontFile is not a stream") {
		t.Fatalf("text=%q err=%v", got, err)
	}
}

func TestCompleteToUnicodeDoesNotReadBrokenFontDescriptor(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /F1 12 Tf (A) Tj ET"), ""))
	objects[3] = "<< /Type /Font /Subtype /Type1 /ToUnicode 6 0 R /FontDescriptor 7 0 R >>"
	objects = append(objects, streamBody([]byte("1 beginbfchar <41> <0041> endbfchar"), ""), "(not a dictionary)")
	if got := extract(t, fixture(t, objects), 1); got != "A" {
		t.Fatalf("text=%q", got)
	}
}

func TestPartialToUnicodeWithoutType1ProgramRetainsStandardFallback(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /F1 12 Tf (AB) Tj ET"), ""))
	objects[3] = "<< /Type /Font /Subtype /Type1 /ToUnicode 6 0 R /FontDescriptor 7 0 R >>"
	objects = append(objects, streamBody([]byte("1 beginbfchar <41> <0058> endbfchar"), ""), "<< /FontName /Example >>")
	if got := extract(t, fixture(t, objects), 1); got != "XB" {
		t.Fatalf("text=%q", got)
	}
}

func TestLazyType1FallbackPreservesStandardWidths(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /F1 12 Tf 40 700 Td (W) Tj 1 0 0 1 51.328 700 Tm (W) Tj ET"), ""))
	objects[3] = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /ToUnicode 6 0 R /FontDescriptor 7 0 R >>"
	objects = append(objects, streamBody([]byte("1 beginbfchar <57> <0057> endbfchar"), ""), "<< /FontName /Helvetica >>")
	if got := extract(t, fixture(t, objects), 1); got != "WW" {
		t.Fatalf("lost standard widths: %q", got)
	}
}

func TestRedundantEndTextDoesNotDiscardReadableText(t *testing.T) {
	for _, content := range []string{
		"ET BT /F1 12 Tf (before) Tj ET ET BT /F1 12 Tf 0 -20 Td (after) Tj ET ET",
		"q BT /F1 12 Tf (before) Tj ET Q ET BT /F1 12 Tf 0 -20 Td (after) Tj ET",
	} {
		if got := extract(t, fixture(t, basicObjects(streamBody([]byte(content), ""))), 1); got != "before after" {
			t.Fatalf("text=%q", got)
		}
	}
	for _, content := range []string{"ET /F1 12 Tf (outside) Tj", "BT /F1 12 Tf (before) Tj ET ET (outside) Tj", "BT BT /F1 12 Tf (nested) Tj ET"} {
		e, _ := New()
		got, err := e.PageText(context.Background(), fixture(t, basicObjects(streamBody([]byte(content), ""))), 1)
		e.Close()
		if got != "" || err == nil {
			t.Fatalf("accepted invalid text object: %q, %v", got, err)
		}
	}
}
