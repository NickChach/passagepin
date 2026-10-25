package pdf

import (
	_ "embed"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Adobe's glyph-name -> Unicode data, distributed under the BSD license printed
// in the file. This is mapping data, not an external parser or executable.
// Source: https://github.com/adobe-type-tools/agl-aglfn/blob/master/glyphlist.txt
//
//go:embed glyphlist.txt
var adobeGlyphData string
var adobeGlyphs = loadAdobeGlyphs()

func loadAdobeGlyphs() map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(adobeGlyphData, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		glyph, raw, ok := strings.Cut(line, ";")
		if !ok {
			continue
		}
		var text strings.Builder
		valid := true
		for _, code := range strings.Fields(raw) {
			n, err := strconv.ParseUint(code, 16, 32)
			if err != nil || !utf8.ValidRune(rune(n)) {
				valid = false
				break
			}
			text.WriteRune(rune(n))
		}
		if valid && text.Len() > 0 {
			result[glyph] = text.String()
		}
	}
	return result
}
