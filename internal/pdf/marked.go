package pdf

import (
	"bytes"
	"fmt"
	"strings"
)

// PDF text strings use PDFDocEncoding or BOM-prefixed UTF-16BE, independently
// of the font encoding used by text-show operators (ISO 32000-1, 7.9.2.2).
func decodeTextString(raw pdfString) (string, error) {
	if len(raw) > maxToken {
		return "", fmt.Errorf("PDF text string exceeds limit")
	}
	if bytes.HasPrefix(raw, []byte{0xfe, 0xff}) {
		return decodeUTF16(raw[2:])
	}
	if bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}) || bytes.HasPrefix(raw, []byte{0xff, 0xfe}) {
		return "", unsupported("text-string-encoding", "PDF text strings require PDFDocEncoding or UTF-16BE")
	}
	low := []rune("˘ˇˆ˙˝˛˚˜")                         // 0x18–0x1f
	high := []rune("•†‡…—–ƒ⁄‹›−‰„“”‘’‚™ﬁﬂŁŒŠŸŽıłœšž") // 0x80–0x9e
	var out strings.Builder
	for _, b := range raw {
		ch := rune(b)
		switch {
		case b >= 0x18 && b <= 0x1f:
			ch = low[b-0x18]
		case b >= 0x80 && b <= 0x9e:
			ch = high[b-0x80]
		case b == 0xa0:
			ch = '€'
		case b == 0x7f || b == 0x9f || b == 0xad:
			return "", fmt.Errorf("undefined PDFDocEncoding byte %02x", b)
		}
		out.WriteRune(ch)
	}
	return out.String(), nil
}

type replacementText struct {
	text    string
	start   textState
	emitted bool
}

type markedContent struct {
	previous *replacementText
	own      *replacementText
}

func (r *interpreter) beginMarked(args []value, properties bool) error {
	if _, ok := args[0].(name); !ok {
		return fmt.Errorf("marked-content tag must be a name")
	}
	if len(r.marked) >= maxDepth {
		return fmt.Errorf("marked-content nesting too deep")
	}
	entry := markedContent{previous: r.replacement}
	if properties {
		raw := args[1]
		if n, ok := raw.(name); ok {
			props, err := r.d.dictionary(r.resources["Properties"])
			if err != nil {
				return err
			}
			var exists bool
			raw, exists = props[string(n)]
			if !exists {
				return fmt.Errorf("missing marked-content property /%s", n)
			}
		}
		props, err := r.d.dictionary(raw)
		if err != nil {
			return err
		}
		if raw, exists := props["ActualText"]; exists {
			raw, err = r.d.resolve(raw)
			if err != nil {
				return err
			}
			str, ok := raw.(pdfString)
			if !ok {
				return fmt.Errorf("ActualText must be a text string")
			}
			text, err := decodeTextString(str)
			if err != nil {
				return fmt.Errorf("ActualText: %w", err)
			}
			// The outer replacement substitutes the entire enclosed sequence.
			// Empty ActualText also suppresses enclosed glyph text.
			if r.replacement == nil {
				entry.own = &replacementText{text: text, start: r.state}
				r.replacement = entry.own
			}
		}
	}
	r.marked = append(r.marked, entry)
	return nil
}

func (r *interpreter) endMarked() error {
	if len(r.marked) == 0 {
		return fmt.Errorf("EMC without BMC or BDC")
	}
	entry := r.marked[len(r.marked)-1]
	r.marked = r.marked[:len(r.marked)-1]
	r.replacement = entry.previous
	if entry.own != nil && !entry.own.emitted {
		// Replacement may describe painted paths or an image, with no Tj at all.
		return r.out.emit(&entry.own.start, entry.own.text, 0)
	}
	return nil
}

// Covered glyphs still advance the text matrix, but their Unicode mappings
// are irrelevant. Font loading remains strict about supported encodings.
func (f *font) measure(raw []byte) (width float64, spaces, glyphs int, err error) {
	size := 1
	if f.composite {
		size = 2 // Only Identity-H is currently supported.
	}
	if len(raw)%size != 0 {
		return 0, 0, 0, fmt.Errorf("incomplete composite character code")
	}
	for len(raw) > 0 {
		w, ok := f.widths[codeNumber(raw[:size])]
		if !ok {
			w = f.defaultWidth
		}
		width += w
		if size == 1 && raw[0] == 32 {
			spaces++
		}
		glyphs++
		raw = raw[size:]
	}
	return
}
