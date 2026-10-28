package pdf

import (
	"bytes"
	"compress/lzw"
	"compress/zlib"
	"context"
	"encoding/ascii85"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, objects []string) string {
	t.Helper()
	var out bytes.Buffer
	out.WriteString("%PDF-1.7\n")
	offsets := make([]int, len(objects)+1)
	for i, body := range objects {
		offsets[i+1] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	path := filepath.Join(t.TempDir(), "fixture.pdf")
	if err := os.WriteFile(path, out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func streamBody(data []byte, options string) string {
	return fmt.Sprintf("<< /Length %d %s >>\nstream\n%sendstream", len(data), options, data)
}
func flate(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func basicObjects(content string) []string {
	return []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [3 0 R] /Resources << /Font << /F1 4 0 R >> >> >>",
		"<< /Type /Page /Parent 2 0 R /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
		content,
	}
}
func extract(t *testing.T, path string, page int) string {
	t.Helper()
	e, _ := New()
	defer e.Close()
	got, err := e.PageText(context.Background(), path, page)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func TestCompressedTextOperatorsAndInheritedResources(t *testing.T) {
	content := []byte("q 1 0 0 1 0 0 cm BT /F1 10 Tf 12 TL 40 750 Td [(Hello) -500 (world)] TJ T* (Caf\\351) Tj ( and \\(tea\\)) Tj (next) ' 0 0 (last) \" ET Q")
	compressed := flate(t, content)
	encoded := make([]byte, ascii85.MaxEncodedLen(len(compressed)))
	n := ascii85.Encode(encoded, compressed)
	encoded = append(encoded[:n], '~', '>')
	path := fixture(t, basicObjects(streamBody(encoded, "/Filter [/ASCII85Decode /FlateDecode]")))
	if got := extract(t, path, 1); got != "Hello world Café and (tea) next last" {
		t.Fatalf("got %q", got)
	}
}
func TestCIDFontUnicodeRangesLigaturesAndSurrogates(t *testing.T) {
	cmap := []byte("/CIDInit /ProcSet findresource begin 12 dict begin begincmap 1 begincodespacerange <0000> <FFFF> endcodespacerange 1 beginbfrange <0001> <0003> <0041> endbfrange 2 beginbfchar <0004> <00660069> <0005> <D83DDE00> endbfchar endcmap end end")
	objects := basicObjects(streamBody([]byte("BT /F1 12 Tf 40 700 Td <00010002000300040005> Tj ET"), ""))
	objects[3] = "<< /Type /Font /Subtype /Type0 /BaseFont /Example /Encoding /Identity-H /DescendantFonts [6 0 R] /ToUnicode 7 0 R >>"
	objects = append(objects, "<< /Type /Font /Subtype /CIDFontType2 /DW 600 /W [1 [500 600] 3 5 700] >>", streamBody(flate(t, cmap), "/Filter /FlateDecode"))
	if got := extract(t, fixture(t, objects), 1); got != "ABCfi😀" {
		t.Fatalf("got %q", got)
	}
}

func TestCIDFontIndirectWidths(t *testing.T) {
	for _, tc := range []struct{ name, widths string }{
		{"indirect nested array", "[1 9 0 R 3 4 700]"},
		{"indirect range operands", "[10 0 R 9 0 R 11 0 R 12 0 R 13 0 R]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmap := []byte("1 begincodespacerange <0000> <FFFF> endcodespacerange 1 beginbfrange <0001> <0005> <0041> endbfrange")
			objects := basicObjects(streamBody([]byte("BT /F1 10 Tf <00010002000300040005> Tj ET"), ""))
			objects[3] = "<< /Type /Font /Subtype /Type0 /Encoding /Identity-H /DescendantFonts [6 0 R] /ToUnicode 7 0 R >>"
			objects = append(objects,
				"<< /Type /Font /Subtype /CIDFontType2 /DW 14 0 R /W 8 0 R >>",
				streamBody(cmap, ""), tc.widths, "[500 15 0 R]", "1", "3", "4", "700", "900", "600")
			path := fixture(t, objects)
			if got := extract(t, path, 1); got != "ABCDE" {
				t.Fatalf("got %q", got)
			}
			e, _ := New()
			defer e.Close()
			d, err := e.open(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			f, err := d.loadFont(reference{4, 0})
			if err != nil {
				t.Fatal(err)
			}
			for code, want := range map[int]float64{1: 500, 2: 600, 3: 700, 4: 700} {
				if got := f.widths[code]; got != want {
					t.Errorf("CID %d width = %v, want %v", code, got, want)
				}
			}
			if f.defaultWidth != 900 {
				t.Errorf("default width = %v, want 900", f.defaultWidth)
			}
		})
	}
}
func TestCIDFontUnusedInvalidUnicodeMappings(t *testing.T) {
	cmap := []byte("1 begincodespacerange <0000> <FFFF> endcodespacerange 3 beginbfrange <0041> <0043> <0041> <D800> <DFFF> <D800> <E000> <E001> [<D83DDE00> <00660069>] endbfrange")
	objects := basicObjects(streamBody([]byte("BT /F1 10 Tf <004100420043E000E001> Tj ET"), ""))
	objects[3] = "<< /Type /Font /Subtype /Type0 /Encoding /Identity-H /DescendantFonts [6 0 R] /ToUnicode 7 0 R >>"
	objects = append(objects, "<< /Type /Font /Subtype /CIDFontType2 >>", streamBody(cmap, ""))
	if got := extract(t, fixture(t, objects), 1); got != "ABC😀fi" {
		t.Fatalf("got %q", got)
	}
	// A code that is actually used still needs a valid Unicode destination.
	for _, code := range []string{"D800", "DC00"} {
		objects[4] = streamBody([]byte("BT /F1 10 Tf <"+code+"> Tj ET"), "")
		e, _ := New()
		_, err := e.PageText(context.Background(), fixture(t, objects), 1)
		e.Close()
		if err == nil || !strings.Contains(err.Error(), "surrogate") {
			t.Fatalf("code %s: expected Unicode error, got %v", code, err)
		}
	}
}

func TestEncodingDifferencesAndFormXObjects(t *testing.T) {
	objects := basicObjects(streamBody([]byte("q /Picture Do /Section Do Q"), ""))
	objects[1] = "<< /Type /Pages /Count 1 /Kids [3 0 R] /Resources << /Font << /F1 4 0 R >> /XObject << /Section 6 0 R /Picture 7 0 R >> >> >>"
	objects[3] = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding << /BaseEncoding /WinAnsiEncoding /Differences [128 /eacute /fi /uni0041] >> >>"
	objects = append(objects, streamBody([]byte("BT /F1 10 Tf 40 750 Td <4361668020812082> Tj ET"), "/Type /XObject /Subtype /Form /Matrix [1 0 0 1 0 0]"), streamBody([]byte("not a real jpeg and must not be decoded"), "/Type /XObject /Subtype /Image /Filter /DCTDecode"))
	if got := extract(t, fixture(t, objects), 1); got != "Café fi A" {
		t.Fatalf("got %q", got)
	}
}
func TestContentsArrayAndIndirectLength(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /F1 10 Tf 40 700 Td (first)"), ""))
	objects[2] = "<< /Type /Page /Parent 2 0 R /Contents [5 0 R 6 0 R] >>"
	content := " Tj ( second) Tj ET"
	objects = append(objects, fmt.Sprintf("<< /Length 7 0 R >>\nstream\n%sendstream", content), fmt.Sprint(len(content)))
	if got := extract(t, fixture(t, objects), 1); got != "first second" {
		t.Fatalf("got %q", got)
	}
}
func TestPageContentIsLazyAndErrorsAreExplicit(t *testing.T) {
	objects := basicObjects(streamBody([]byte("BT /F1 10 Tf (healthy page) Tj ET"), ""))
	objects[1] = "<< /Type /Pages /Count 2 /Kids [3 0 R 6 0 R] /Resources << /Font << /F1 4 0 R >> >> >>"
	objects = append(objects, "<< /Type /Page /Parent 2 0 R /Contents 7 0 R >>", streamBody([]byte("broken stream"), "/Filter /UnsupportedDecode"))
	path := fixture(t, objects)
	e, _ := New()
	defer e.Close()
	if n, err := e.PageCount(context.Background(), path); err != nil || n != 2 {
		t.Fatalf("metadata eagerly decoded page: n=%d err=%v", n, err)
	}
	if got, err := e.PageText(context.Background(), path, 1); err != nil || got != "healthy page" {
		t.Fatalf("first page: %q %v", got, err)
	}
	if _, err := e.PageText(context.Background(), path, 2); err == nil || !strings.Contains(err.Error(), "unsupported PDF stream filter") {
		t.Fatalf("missing filter error: %v", err)
	}
	if _, err := e.PageText(context.Background(), path, 0); err == nil {
		t.Fatal("accepted page zero")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.PageText(ctx, path, 1); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestCrossReferenceStreamAndCompressedObjects(t *testing.T) {
	// Catalog, page tree and page dictionary live in an ObjStm, rather than
	// ordinary objects. Xref rows use PNG Up prediction and Flate compression.
	bodies := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Count 1 /Kids [3 0 R] /Resources << /Font << /F1 4 0 R >> >> >>", "<< /Type /Page /Parent 2 0 R /Contents 5 0 R >>"}
	var header, body strings.Builder
	for i, v := range bodies {
		fmt.Fprintf(&header, "%d %d ", i+1, body.Len())
		body.WriteString(v + " ")
	}
	first := header.Len()
	objectData := []byte(header.String() + body.String())
	var out bytes.Buffer
	out.WriteString("%PDF-1.7\n")
	offsets := map[int]int{}
	write := func(id int, body string) {
		offsets[id] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", id, body)
	}
	write(4, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	write(5, streamBody([]byte("BT /F1 12 Tf (compressed objects work) Tj ET"), ""))
	write(6, streamBody(flate(t, objectData), fmt.Sprintf("/Type /ObjStm /N 3 /First %d /Filter /FlateDecode", first)))
	offsets[7] = out.Len()
	var rows []byte
	for id := 0; id < 8; id++ {
		row := make([]byte, 7)
		if id == 0 {
			binary.BigEndian.PutUint16(row[5:], 65535)
		} else if id <= 3 {
			row[0] = 2
			binary.BigEndian.PutUint32(row[1:], 6)
			binary.BigEndian.PutUint16(row[5:], uint16(id-1))
		} else {
			row[0] = 1
			binary.BigEndian.PutUint32(row[1:], uint32(offsets[id]))
		}
		rows = append(rows, row...)
	}
	var predicted []byte
	previous := make([]byte, 7)
	for i := 0; i < len(rows); i += 7 {
		predicted = append(predicted, 2)
		for j := 0; j < 7; j++ {
			predicted = append(predicted, rows[i+j]-previous[j])
		}
		copy(previous, rows[i:i+7])
	}
	write(7, streamBody(flate(t, predicted), "/Type /XRef /Size 8 /Root 1 0 R /W [1 4 2] /Filter /FlateDecode /DecodeParms << /Predictor 12 /Columns 7 >>"))
	fmt.Fprintf(&out, "startxref\n%d\n%%%%EOF\n", offsets[7])
	path := filepath.Join(t.TempDir(), "modern.pdf")
	if err := os.WriteFile(path, out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if got := extract(t, path, 1); got != "compressed objects work" {
		t.Fatalf("got %q", got)
	}
}
func TestIncrementalCrossReferences(t *testing.T) {
	path := fixture(t, basicObjects(streamBody([]byte("BT /F1 10 Tf (old) Tj ET"), "")))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.LastIndex(data, []byte("startxref"))
	var prev int
	if _, err := fmt.Sscanf(string(data[at:]), "startxref\n%d", &prev); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	out.Write(data)
	offset := out.Len()
	fmt.Fprintf(&out, "5 0 obj\n%s\nendobj\n", streamBody([]byte("BT /F1 10 Tf (new revision) Tj ET"), ""))
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n5 1\n%010d 00000 n \ntrailer\n<< /Size 6 /Root 1 0 R /Prev %d >>\nstartxref\n%d\n%%%%EOF\n", offset, prev, xref)
	if err := os.WriteFile(path, out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if got := extract(t, path, 1); got != "new revision" {
		t.Fatalf("got %q", got)
	}
}
func TestFilterDecoders(t *testing.T) {
	input := bytes.Repeat([]byte("ABC text with repetition abcdefghijklmnopqrstuvwxyz 0123456789\n"), 400)
	var compressed bytes.Buffer
	writer := lzw.NewWriter(&compressed, lzw.MSB, 8)
	if _, err := writer.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := decodeLZW(compressed.Bytes(), 0)
	if err != nil || !bytes.Equal(got, input) {
		t.Fatalf("LZW EarlyChange=0: got %d bytes, err=%v", len(got), err)
	}
	got, err = runLength([]byte{2, 'a', 'b', 'c', 254, 'x', 128})
	if err != nil || string(got) != "abcxxx" {
		t.Fatalf("RunLength: %q %v", got, err)
	}
	// EarlyChange=1, including clear/end codes, with no dictionary width change.
	codes := []int{256, 65, 66, 258, 257}
	bits := 0
	var packed []byte
	for _, code := range codes {
		for j := 8; j >= 0; j-- {
			if bits%8 == 0 {
				packed = append(packed, 0)
			}
			packed[len(packed)-1] |= byte((code>>j)&1) << uint(7-bits%8)
			bits++
		}
	}
	got, err = decodeLZW(packed, 1)
	if err != nil || string(got) != "ABAB" {
		t.Fatalf("LZW EarlyChange=1: %q %v", got, err)
	}
}
func TestMalformedAndUnsupportedPDFs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]string) []string
		want   string
	}{
		{"cyclic page tree", func(o []string) []string { o[1] = "<< /Type /Pages /Count 1 /Kids [2 0 R] >>"; return o }, "cyclic"},
		{"missing Unicode", func(o []string) []string {
			o[3] = "<< /Type /Font /Subtype /Type0 /Encoding /Identity-H /DescendantFonts [6 0 R] >>"
			o[4] = streamBody([]byte("BT /F1 10 Tf <0001> Tj ET"), "")
			return append(o, "<< /Type /Font /Subtype /CIDFontType2 >>")
		}, "no Unicode mapping"},
		{"unknown glyph", func(o []string) []string {
			o[3] = "<< /Type /Font /Subtype /Type1 /Encoding << /Differences [65 /unknownGlyph] >> >>"
			return o
		}, "no Unicode mapping"},
		{"inline image", func(o []string) []string { o[4] = streamBody([]byte("BI /W 1 /H 1 ID x EI"), ""); return o }, "inline image colour space"},
		{"stream length", func(o []string) []string { o[4] = "<< /Length 1 >>\nstream\nnot one byte\nendstream"; return o }, "Length"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := fixture(t, tc.change(basicObjects(streamBody([]byte("BT /F1 10 Tf (A) Tj ET"), ""))))
			e, _ := New()
			defer e.Close()
			_, err := e.PageText(context.Background(), path, 1)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
	path := fixture(t, basicObjects(streamBody(nil, "")))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("/Root 1 0 R"), []byte("/Root 1 0 R /Encrypt 9 0 R"), 1)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	e, _ := New()
	defer e.Close()
	if _, err := e.PageCount(context.Background(), path); err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("expected encryption error: %v", err)
	}
}

// Optional compatibility check against a local PDF not generated by our fixtures.
func TestExternalPDF(t *testing.T) {
	path := os.Getenv("PDFSEARCH_TEST_PDF")
	if path == "" {
		t.Skip("set PDFSEARCH_TEST_PDF to check an independent PDF")
	}
	e, _ := New()
	defer e.Close()
	n, err := e.PageCount(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for page := 1; page <= n; page++ {
		text, err := e.PageText(context.Background(), path, page)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		total += len(text)
		if page == 1 {
			t.Logf("First page: %.180s", text)
		}
	}
	if total == 0 {
		t.Fatal("document yielded no text")
	}
	t.Logf("Extracted %d pages, %d UTF-8 bytes", n, total)
}
