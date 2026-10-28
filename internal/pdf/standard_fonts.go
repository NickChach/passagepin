package pdf

import (
	_ "embed"
	"strconv"
	"strings"
)

// Horizontal metrics from Adobe's 14 PDF core fonts. Only character codes,
// glyph names and widths are retained; see the notices in the data file.
//
//go:embed standard-fonts.txt
var standardFontData string

type standardMetrics struct {
	codes   map[int]float64
	unicode map[string]float64
}

var standardFonts = loadStandardFonts()

func loadStandardFonts() map[string]*standardMetrics {
	fonts := map[string]*standardMetrics{}
	var metrics *standardMetrics
	for _, line := range strings.Split(standardFontData, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "@" {
			metrics = &standardMetrics{codes: map[int]float64{}, unicode: map[string]float64{}}
			for _, font := range fields[1:] {
				fonts[font] = metrics
			}
			continue
		}
		if len(fields) != 3 || metrics == nil {
			panic("invalid embedded standard font metrics")
		}
		code, err := strconv.Atoi(fields[0])
		width, widthErr := strconv.Atoi(fields[2])
		if err != nil || widthErr != nil {
			panic("invalid embedded standard font width")
		}
		if code >= 0 {
			metrics.codes[code] = float64(width)
		}
		if text, ok := glyphUnicode(fields[1]); ok {
			metrics.unicode[text] = float64(width)
		}
	}
	return fonts
}

func baseFontName(base string) string {
	// Subset font names prefix the PostScript name with six uppercase letters.
	if len(base) > 7 && base[6] == '+' && strings.Trim(base[:6], "ABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" {
		base = base[7:]
	}
	return base
}

func (f *font) useStandardWidths(builtInEncoding bool) {
	base := baseFontName(f.label)
	metrics := standardFonts[base]
	if metrics == nil {
		return
	}
	if builtInEncoding && (base == "Symbol" || base == "ZapfDingbats") {
		for code, width := range metrics.codes {
			f.widths[code] = width
		}
		return
	}
	// Re-encoded fonts use the metrics for the encoded glyph, including
	// Differences. ToUnicode describes extracted text and can differ from it.
	for code, text := range f.encoding {
		switch text {
		case "\u00a0":
			text = " "
		case "\u00ad":
			text = "-"
		}
		if width, ok := metrics.unicode[text]; ok {
			f.widths[int(code)] = width
		}
	}
}
