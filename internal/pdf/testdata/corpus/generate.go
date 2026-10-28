//go:build ignore

// Regenerate from the repository root:
// go run ./internal/pdf/testdata/corpus/generate.go
// All source strings and PDF objects here are original MIT-licensed fixtures.
package main

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const directory = "internal/pdf/testdata/corpus"

type page struct {
	Text       string `json:"text"`
	Error      string `json:"error_contains,omitempty"`
	Feature    string `json:"unsupported_feature,omitempty"`
	Limitation string `json:"known_limitation,omitempty"`
}
type example struct {
	File     string   `json:"file"`
	Features []string `json:"features"`
	Pages    []page   `json:"pages"`
}

func stream(data, options string) string {
	return fmt.Sprintf("<< /Length %d %s >>\nstream\n%sendstream", len(data), options, data)
}
func basic(content string) []string {
	return []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [3 0 R] /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> >>",
		"<< /Type /Page /Parent 2 0 R /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
		stream(content, ""),
	}
}
func text(content string) string { return "BT /F1 12 Tf 40 740 Td " + content + " ET" }

func write(objects []string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	offsets := []int{0}
	for i, body := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, off := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return b.Bytes()
}

func main() {
	manifest := struct {
		Source  string    `json:"source"`
		License string    `json:"license"`
		Cases   []example `json:"cases"`
	}{Source: "Self-authored fixtures; regenerate with generate.go. No external documents or font programs.", License: "MIT"}
	add := func(file string, objects []string, features []string, pages ...page) {
		if err := os.WriteFile(filepath.Join(directory, file), write(objects), 0644); err != nil {
			panic(err)
		}
		manifest.Cases = append(manifest.Cases, example{file, features, pages})
	}

	objects := basic(text(`(Hello Caf\351) Tj`))
	var compressed bytes.Buffer
	w := zlib.NewWriter(&compressed)
	if _, err := w.Write([]byte(text(`(Hello Caf\351) Tj`))); err != nil {
		panic(err)
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	objects[4] = stream(compressed.String(), "/Filter /FlateDecode")
	add("flate-inherited.pdf", objects, []string{"FlateDecode", "inherited-resources", "WinAnsi", "Type1"}, page{Text: "Hello Café"})

	add("actualtext-inline.pdf", basic(text(`(o) Tj /Span << /ActualText (ffi) >> BDC [(X) 0 (X)] TJ EMC (ce work) Tj`)), []string{"ActualText", "ligature", "TJ", "word-boundaries"}, page{Text: "office work"})

	objects = basic(text(`/Span /Replacement BDC (wrong) Tj EMC`))
	objects[1] = "<< /Type /Pages /Count 1 /Kids [3 0 R] /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> /Properties << /Replacement 6 0 R >> >> >>"
	objects = append(objects, `<< /ActualText (Cost \24010 \200 caf\351) >>`)
	add("actualtext-properties.pdf", objects, []string{"ActualText", "indirect-property-list", "PDFDocEncoding"}, page{Text: "Cost €10 • café"})

	add("actualtext-utf16.pdf", basic(text(`/Span << /ActualText <FEFF039103BBD83DDE00> >> BDC (wrong) Tj EMC`)), []string{"ActualText", "UTF-16BE", "surrogate-pair"}, page{Text: "Αλ😀"})

	objects = basic(`/Span << /ActualText (outer replacement) >> BDC /Section Do EMC ` + text(`( next) Tj`))
	objects[1] = "<< /Type /Pages /Count 1 /Kids [3 0 R] /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> /XObject << /Section 6 0 R >> >> >>"
	objects = append(objects, stream(text(`/Span << /ActualText (inner) >> BDC (wrong) Tj EMC /Part BMC (still wrong) Tj EMC`), "/Type /XObject /Subtype /Form /BBox [0 0 612 792]"))
	add("actualtext-nested-form.pdf", objects, []string{"ActualText", "nested-marked-content", "Form-XObject"}, page{Text: "outer replacement next"})

	objects = basic(text(`<000100020003> Tj`))
	objects[3] = "<< /Type /Font /Subtype /Type0 /BaseFont /Example /Encoding /Identity-H /DescendantFonts [6 0 R] /ToUnicode 7 0 R >>"
	objects = append(objects, "<< /Type /Font /Subtype /CIDFontType2 /BaseFont /Example /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /DW 600 >>", stream("1 begincodespacerange <0000> <FFFF> endcodespacerange 3 beginbfchar <0001> <0041> <0002> <00660069> <0003> <D83DDE00> endbfchar", ""))
	add("cid-unicode.pdf", objects, []string{"Identity-H", "CIDFontType2", "ToUnicode", "ligature", "surrogate-pair"}, page{Text: "Afi😀"})

	objects[4] = stream(text(`/Span << /ActualText (decoded) >> BDC <0004> Tj EMC <0001> Tj`), "")
	add("actualtext-unmapped-cid.pdf", objects, []string{"ActualText", "Identity-H", "missing-glyph-mapping"}, page{Text: "decodedA"})

	add("columns.pdf", basic(text(`(Left one) Tj 270 0 Td (Right one) Tj -270 -20 Td (Left two) Tj 270 0 Td (Right two) Tj`)), []string{"two-columns", "row-major-content-order"}, page{Text: "Left one Left two Right one Right two", Limitation: "Column reconstruction is not implemented; assert text retention, record target column order."})

	objects = basic(`/Picture Do`)
	objects[1] = "<< /Type /Pages /Count 1 /Kids [3 0 R] /MediaBox [0 0 612 792] /Resources << /XObject << /Picture 6 0 R >> >> >>"
	objects = append(objects, stream("\x00", "/Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8"))
	add("image-only.pdf", objects, []string{"Image-XObject", "no-text-layer"}, page{Text: ""})

	add("inline-image.pdf", basic("q BI /W 1 /H 1 /CS /G /BPC 8 ID \x00 EI Q "+text("(Text after image) Tj")), []string{"inline-image", "text-after-image"}, page{Text: "Text after image"})

	objects = basic(text(`(visible) Tj`))
	objects[3] = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /ToUnicode 6 0 R >>"
	objects = append(objects, stream("1 beginbfchar <76> <0076> endbfchar", "/UseCMap 7 0 R"), stream("1 beginbfchar <69> <0069> endbfchar", ""))
	add("inherited-tounicode.pdf", objects, []string{"ToUnicode", "UseCMap", "unsupported"}, page{Error: "UseCMap inheritance is not supported", Feature: "tounicode-inheritance"})

	objects = basic(text(`(healthy first page) Tj`))
	objects[1] = "<< /Type /Pages /Count 2 /Kids [3 0 R 6 0 R] /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> >>"
	objects = append(objects, "<< /Type /Page /Parent 2 0 R /Contents 7 0 R >>", stream(text("(broken) Tj"), "/Filter /UnsupportedDecode"))
	add("late-page-failure.pdf", objects, []string{"multi-page", "unsupported-filter", "failure-isolation"}, page{Text: "healthy first page"}, page{Error: "unsupported PDF stream filter", Feature: "stream-filter"})

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), append(data, '\n'), 0644); err != nil {
		panic(err)
	}
}
