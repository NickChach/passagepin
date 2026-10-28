// Package testpdf creates tiny text-only PDF fixtures without external tools.
package testpdf

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

func Write(path string, pages []string) error {
	return write(path, pages, "")
}

// WriteWithCMap maps the page strings' character codes through a ToUnicode CMap.
func WriteWithCMap(path string, pages []string, cmap string) error {
	return write(path, pages, cmap)
}

func write(path string, pages []string, cmap string) error {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	object := func(id int, body string) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", id, body)
	}
	object(1, "<< /Type /Catalog /Pages 2 0 R >>")
	var kids []string
	for i := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", 4+i*2))
	}
	object(2, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)))
	cmapID := 4 + 2*len(pages)
	font := "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica"
	if cmap != "" {
		font += fmt.Sprintf(" /ToUnicode %d 0 R", cmapID)
	}
	object(3, font+" >>")
	escape := strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)")
	for i, page := range pages {
		id := 4 + i*2
		object(id, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", id+1))
		var content strings.Builder
		content.WriteString("BT /F1 10 Tf 14 TL 40 750 Td\n")
		for _, line := range strings.Split(page, "\n") {
			fmt.Fprintf(&content, "(%s) Tj T*\n", escape.Replace(line))
		}
		content.WriteString("ET\n")
		object(id+1, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", content.Len(), content.String()))
	}
	if cmap != "" {
		object(cmapID, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(cmap), cmap))
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return os.WriteFile(path, buf.Bytes(), 0600)
}
