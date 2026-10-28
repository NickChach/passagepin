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

// Adobe publishes dingbat names separately from the general glyph list.
// These names are meaningful only in the ZapfDingbats font family.
//
//go:embed zapfdingbats.txt
var zapfGlyphData string

var adobeGlyphs = loadGlyphList(adobeGlyphData)
var zapfGlyphs = loadGlyphList(zapfGlyphData)

func loadGlyphList(data string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(data, "\n") {
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
