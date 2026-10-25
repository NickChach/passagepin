package pdf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/unicode/norm"
)

type codeRange struct{ low, high []byte }
type unicodeMap struct {
	codes   map[string]string
	ranges  []codeRange
	invalid map[string]error
}
type font struct {
	label        string
	unicode      *unicodeMap
	encoding     map[byte]string
	widths       map[int]float64
	defaultWidth float64
}

func unicodeString(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw)%2 != 0 || len(raw) > 1024 {
		return "", fmt.Errorf("ToUnicode destination must be UTF-16BE")
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = binary.BigEndian.Uint16(raw[i*2:])
	}
	for i := 0; i < len(units); i++ {
		if units[i] >= 0xd800 && units[i] <= 0xdbff {
			if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
				return "", fmt.Errorf("invalid UTF-16 surrogate pair")
			}
			i++
		} else if units[i] >= 0xdc00 && units[i] <= 0xdfff {
			return "", fmt.Errorf("unpaired UTF-16 surrogate")
		}
	}
	return string(utf16.Decode(units)), nil
}
func codeNumber(raw []byte) int {
	n := 0
	for _, b := range raw {
		n = n<<8 | int(b)
	}
	return n
}
func parseCMap(data []byte) (*unicodeMap, error) {
	result := &unicodeMap{codes: map[string]string{}}
	mappedBytes := 0
	put := func(code, raw []byte) error {
		text, decodeErr := unicodeString(raw)
		if decodeErr != nil {
			// Broad identity maps can cover reserved surrogate code points.
			// Validate their destinations only when page text uses that code;
			// an unused invalid entry must not make the entire font unusable.
			if result.invalid == nil {
				result.invalid = map[string]error{}
			}
			result.invalid[string(code)] = decodeErr
		} else {
			delete(result.invalid, string(code))
		}
		mappedBytes += len(text) - len(result.codes[string(code)])
		if mappedBytes > maxToken || len(result.codes) >= 65536 {
			return fmt.Errorf("ToUnicode mappings exceed size limit")
		}
		result.codes[string(code)] = text
		return nil
	}
	p := newSyntax(bytes.NewReader(data), 0)
	var previous value
	for tokens := 0; ; tokens++ {
		if tokens > maxObjects {
			return nil, fmt.Errorf("ToUnicode CMap too large")
		}
		v, err := p.value(0)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		op, ok := v.(keyword)
		if !ok {
			previous = v
			continue
		}
		if op == "usecmap" {
			return nil, fmt.Errorf("ToUnicode usecmap inheritance is not supported")
		}
		if op != "beginbfchar" && op != "beginbfrange" && op != "begincodespacerange" {
			previous = v
			continue
		}
		count, ok := integer(previous)
		if !ok || count < 0 || count > maxObjects {
			return nil, fmt.Errorf("invalid ToUnicode mapping count")
		}
		for i := int64(0); i < count; i++ {
			from, err := p.value(0)
			if err != nil {
				return nil, err
			}
			to, err := p.value(0)
			if err != nil {
				return nil, err
			}
			first, ok := from.(pdfString)
			if !ok || len(first) < 1 || len(first) > 4 {
				return nil, fmt.Errorf("invalid ToUnicode source code")
			}
			if op == "beginbfchar" {
				dest, ok := to.(pdfString)
				if !ok {
					return nil, fmt.Errorf("invalid ToUnicode destination")
				}
				if err := put(first, dest); err != nil {
					return nil, err
				}
				continue
			}
			last, ok := to.(pdfString)
			if !ok || len(last) != len(first) || bytes.Compare(last, first) < 0 {
				return nil, fmt.Errorf("invalid ToUnicode range")
			}
			if op == "begincodespacerange" {
				if len(result.ranges) >= 1024 {
					return nil, fmt.Errorf("too many ToUnicode codespace ranges")
				}
				result.ranges = append(result.ranges, codeRange{first, last})
				continue
			}
			dest, err := p.value(0)
			if err != nil {
				return nil, err
			}
			span := codeNumber(last) - codeNumber(first) + 1
			if span > maxObjects || len(result.codes) > maxObjects-span {
				return nil, fmt.Errorf("ToUnicode range too large")
			}
			list, isList := dest.(array)
			base, isBytes := dest.(pdfString)
			if (!isList && !isBytes) || (isList && len(list) != span) {
				return nil, fmt.Errorf("invalid ToUnicode range destination")
			}
			target := append([]byte(nil), base...)
			for j := 0; j < span; j++ {
				code := make([]byte, len(first))
				n := codeNumber(first) + j
				for k := len(code) - 1; k >= 0; k-- {
					code[k] = byte(n)
					n >>= 8
				}
				raw := target
				if isList {
					var ok bool
					raw, ok = list[j].(pdfString)
					if !ok {
						return nil, fmt.Errorf("ToUnicode range item is not a string")
					}
				}
				if err := put(code, raw); err != nil {
					return nil, err
				}
				if isBytes {
					for k := len(target) - 1; k >= 0; k-- {
						target[k]++
						if target[k] != 0 {
							break
						}
					}
				}
			}
		}
		expected := keyword("end" + strings.TrimPrefix(string(op), "begin"))
		end, err := p.value(0)
		if err != nil || end != expected {
			return nil, fmt.Errorf("ToUnicode section missing %s", expected)
		}
		previous = nil
	}
	if len(result.codes) == 0 {
		return nil, fmt.Errorf("ToUnicode CMap has no Unicode mappings")
	}
	if len(result.ranges) == 0 {
		// Some producers omit codespacerange; infer widths from explicit mappings.
		sizes := map[int]bool{}
		for code := range result.codes {
			sizes[len(code)] = true
		}
		for size := range sizes {
			result.ranges = append(result.ranges, codeRange{make([]byte, size), bytes.Repeat([]byte{255}, size)})
		}
	}
	sort.Slice(result.ranges, func(i, j int) bool { return len(result.ranges[i].low) < len(result.ranges[j].low) })
	return result, nil
}
func (d *document) loadFont(v value) (*font, error) {
	fd, err := d.dictionary(v)
	if err != nil {
		return nil, err
	}
	f := &font{label: fmt.Sprint(fd["BaseFont"]), widths: map[int]float64{}, defaultWidth: 500}
	if raw := fd["ToUnicode"]; raw != nil {
		resolved, err := d.resolve(raw)
		if err != nil {
			return nil, err
		}
		s, ok := resolved.(*stream)
		if !ok {
			return nil, fmt.Errorf("ToUnicode is not a stream")
		}
		data, err := d.decode(s)
		if err != nil {
			return nil, err
		}
		f.unicode, err = parseCMap(data)
		if err != nil {
			return nil, err
		}
	}
	composite := fd["Subtype"] == name("Type0")
	enc, err := d.resolve(fd["Encoding"])
	if err != nil {
		return nil, err
	}
	if composite {
		n, ok := enc.(name)
		if !ok || (n != "Identity-H") {
			return nil, fmt.Errorf("unsupported composite font encoding %v (requires Identity-H)", enc)
		}
		if f.unicode == nil {
			return nil, fmt.Errorf("composite font needs a ToUnicode map")
		}
		descendants, err := d.resolve(fd["DescendantFonts"])
		if err != nil {
			return nil, err
		}
		list, ok := descendants.(array)
		if !ok || len(list) != 1 {
			return nil, fmt.Errorf("invalid descendant font")
		}
		descendant, err := d.dictionary(list[0])
		if err != nil {
			return nil, err
		}
		f.defaultWidth = 1000
		defaultWidth, err := d.resolve(descendant["DW"])
		if err != nil {
			return nil, err
		}
		if width, ok := number(defaultWidth); ok {
			f.defaultWidth = width
		}
		if raw, exists := descendant["W"]; exists {
			resolved, err := d.resolve(raw)
			if err != nil {
				return nil, err
			}
			widths, ok := resolved.(array)
			if !ok {
				return nil, fmt.Errorf("invalid CID widths")
			}
			for i := 0; i < len(widths); {
				start, err := d.resolve(widths[i])
				if err != nil {
					return nil, err
				}
				first, ok := integer(start)
				i++
				if !ok || first < 0 || first > 65535 || i >= len(widths) {
					return nil, fmt.Errorf("invalid CID width range")
				}
				// Array entries may themselves be indirect objects, including the
				// nested width array in the CID [width ...] representation.
				next, err := d.resolve(widths[i])
				if err != nil {
					return nil, err
				}
				if list, ok := next.(array); ok {
					i++
					for j, v := range list {
						v, err = d.resolve(v)
						if err != nil {
							return nil, err
						}
						width, ok := number(v)
						if !ok || int(first)+j > 65535 {
							return nil, fmt.Errorf("invalid CID width")
						}
						f.widths[int(first)+j] = width
					}
				} else {
					last, ok := integer(next)
					i++
					if !ok || last < first || last > 65535 || i >= len(widths) {
						return nil, fmt.Errorf("invalid CID width range")
					}
					rawWidth, err := d.resolve(widths[i])
					if err != nil {
						return nil, err
					}
					width, ok := number(rawWidth)
					i++
					if !ok {
						return nil, fmt.Errorf("invalid CID width")
					}
					for code := first; code <= last; code++ {
						f.widths[int(code)] = width
					}
				}
			}
		}
		return f, nil
	}
	f.encoding, err = simpleEncoding(enc)
	if err != nil && f.unicode == nil {
		return nil, err
	}
	if enc == nil && f.unicode == nil {
		base, _ := fd["BaseFont"].(name)
		if strings.Contains(string(base), "Symbol") || strings.Contains(string(base), "ZapfDingbats") {
			return nil, fmt.Errorf("symbolic font needs an explicit ToUnicode map")
		}
		// Embedded fonts without an explicit encoding can define arbitrary glyph
		// codes. Do not silently interpret those codes as English text.
		if descriptor, exists := fd["FontDescriptor"]; exists {
			desc, err := d.dictionary(descriptor)
			if err != nil {
				return nil, err
			}
			if raw := desc["FontFile"]; raw != nil {
				resolved, err := d.resolve(raw)
				if err != nil {
					return nil, err
				}
				s, ok := resolved.(*stream)
				if !ok {
					return nil, fmt.Errorf("FontFile is not a stream")
				}
				data, err := d.decode(s)
				if err != nil {
					return nil, err
				}
				f.encoding, err = type1Encoding(data)
				if err != nil {
					return nil, err
				}
			} else if desc["FontFile2"] != nil || desc["FontFile3"] != nil {
				return nil, fmt.Errorf("embedded font needs an explicit Encoding or ToUnicode map")
			}
		}
	}
	if raw := fd["Widths"]; raw != nil {
		resolved, err := d.resolve(raw)
		if err != nil {
			return nil, err
		}
		widths, ok := resolved.(array)
		first, okFirst := integer(fd["FirstChar"])
		if !ok || !okFirst || first < 0 || first > 255 || len(widths) > 256-int(first) {
			return nil, fmt.Errorf("invalid simple font widths")
		}
		for i, v := range widths {
			width, ok := number(v)
			if !ok {
				return nil, fmt.Errorf("invalid glyph width")
			}
			f.widths[int(first)+i] = width
		}
	}
	return f, nil
}

// Type 1 font programs put their encoding in the clear-text portion before
// eexec. Read the standard encoding or the common explicit `dup code /name put`
// definitions; never execute the embedded PostScript program.
func type1Encoding(data []byte) (map[byte]string, error) {
	p := newSyntax(bytes.NewReader(data), 0)
	result := map[byte]string{}
	found := false
	for tokens := 0; tokens < maxObjects; tokens++ {
		v, err := p.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if v == keyword("eexec") {
			break
		}
		if v == name("Encoding") {
			found = true
			next, err := p.next()
			if err != nil {
				return nil, err
			}
			if next == keyword("StandardEncoding") {
				return simpleEncoding(name("StandardEncoding"))
			}
			continue
		}
		if !found {
			continue
		}
		if v == keyword("def") {
			break
		}
		if v != keyword("dup") {
			continue
		}
		codeRaw, err := p.next()
		if err != nil {
			return nil, err
		}
		glyphRaw, err := p.next()
		if err != nil {
			return nil, err
		}
		put, err := p.next()
		if err != nil {
			return nil, err
		}
		code, ok := integer(codeRaw)
		glyph, okGlyph := glyphRaw.(name)
		if !ok || !okGlyph || code < 0 || code > 255 || put != keyword("put") {
			return nil, fmt.Errorf("unsupported Type 1 encoding program")
		}
		if text, ok := glyphUnicode(string(glyph)); ok {
			result[byte(code)] = text
		}
	}
	if !found || len(result) == 0 {
		return nil, fmt.Errorf("Type 1 font lacks a supported built-in encoding")
	}
	return result, nil
}
func (f *font) decode(raw []byte) (string, float64, int, int, error) {
	var text strings.Builder
	var width float64
	spaces := 0
	glyphCount := 0
	for len(raw) > 0 {
		size := 1
		var chars string
		found := false
		if f.unicode != nil {
			for _, r := range f.unicode.ranges {
				n := len(r.low)
				if len(raw) >= n && bytes.Compare(raw[:n], r.low) >= 0 && bytes.Compare(raw[:n], r.high) <= 0 {
					size = n
					chars, found = f.unicode.codes[string(raw[:n])]
					if err := f.unicode.invalid[string(raw[:n])]; err != nil {
						return "", 0, 0, 0, fmt.Errorf("font %s ToUnicode code <%x>: %w", f.label, raw[:n], err)
					}
					break
				}
			}
		}
		if !found && size == 1 && f.encoding != nil {
			chars, found = f.encoding[raw[0]]
		}
		if !found {
			return "", 0, 0, 0, fmt.Errorf("font %s has no Unicode mapping for character code %x", f.label, raw[:min(size, len(raw))])
		}
		code := codeNumber(raw[:size])
		w, ok := f.widths[code]
		if !ok {
			w = f.defaultWidth
		}
		width += w
		if size == 1 && raw[0] == 32 {
			spaces++
		}
		text.WriteString(chars)
		glyphCount++
		raw = raw[size:]
	}
	return text.String(), width, spaces, glyphCount, nil
}
func simpleEncoding(v value) (map[byte]string, error) {
	base := name("StandardEncoding")
	var differences array
	switch encoding := v.(type) {
	case nil:
	case name:
		base = encoding
	case dict:
		if b, exists := encoding["BaseEncoding"]; exists {
			var ok bool
			base, ok = b.(name)
			if !ok {
				return nil, fmt.Errorf("invalid BaseEncoding")
			}
		}
		if diff, exists := encoding["Differences"]; exists {
			var ok bool
			differences, ok = diff.(array)
			if !ok {
				return nil, fmt.Errorf("invalid Encoding Differences")
			}
		}
	default:
		return nil, fmt.Errorf("invalid font Encoding")
	}
	result := map[byte]string{}
	switch base {
	case "WinAnsiEncoding", "MacRomanEncoding":
		table := charmap.Windows1252
		if base == "MacRomanEncoding" {
			table = charmap.Macintosh
		}
		for i := 0; i < 256; i++ {
			r := table.DecodeByte(byte(i))
			if r != utf8.RuneError {
				result[byte(i)] = string(r)
			}
		}
	case "StandardEncoding":
		for i := 32; i < 127; i++ {
			result[byte(i)] = string(rune(i))
		}
		result[39] = "’"
		result[96] = "‘"
		for code, glyph := range standardHigh {
			text, ok := glyphUnicode(glyph)
			if ok {
				result[code] = text
			}
		}
	default:
		return nil, fmt.Errorf("unsupported font encoding /%s", base)
	}
	code := -1
	for _, v := range differences {
		if n, ok := integer(v); ok {
			if n < 0 || n > 255 {
				return nil, fmt.Errorf("invalid Differences code")
			}
			code = int(n)
			continue
		}
		glyph, ok := v.(name)
		if !ok || code < 0 || code > 255 {
			return nil, fmt.Errorf("invalid Differences glyph")
		}
		text, ok := glyphUnicode(string(glyph))
		if ok {
			result[byte(code)] = text
		} else {
			delete(result, byte(code))
		}
		code++
	}
	return result, nil
}

var standardHigh = map[byte]string{161: "exclamdown", 162: "cent", 163: "sterling", 164: "fraction", 165: "yen", 166: "florin", 167: "section", 168: "currency", 169: "quotesingle", 170: "quotedblleft", 171: "guillemotleft", 172: "guilsinglleft", 173: "guilsinglright", 174: "fi", 175: "fl", 177: "endash", 178: "dagger", 179: "daggerdbl", 180: "periodcentered", 182: "paragraph", 183: "bullet", 184: "quotesinglbase", 185: "quotedblbase", 186: "quotedblright", 187: "guillemotright", 188: "ellipsis", 189: "perthousand", 191: "questiondown", 193: "grave", 194: "acute", 195: "circumflex", 196: "tilde", 197: "macron", 198: "breve", 199: "dotaccent", 200: "dieresis", 202: "ring", 203: "cedilla", 205: "hungarumlaut", 206: "ogonek", 207: "caron", 208: "emdash", 225: "AE", 227: "ordfeminine", 232: "Lslash", 233: "Oslash", 234: "OE", 235: "ordmasculine", 241: "ae", 245: "dotlessi", 248: "lslash", 249: "oslash", 250: "oe", 251: "germandbls"}
var glyphs = map[string]string{
	"space": " ", "nbspace": "\u00a0", "nonbreakingspace": "\u00a0", "exclam": "!", "quotedbl": "\"", "numbersign": "#", "dollar": "$", "percent": "%", "ampersand": "&", "quotesingle": "'", "parenleft": "(", "parenright": ")", "asterisk": "*", "plus": "+", "comma": ",", "hyphen": "-", "period": ".", "slash": "/", "colon": ":", "semicolon": ";", "less": "<", "equal": "=", "greater": ">", "question": "?", "at": "@", "bracketleft": "[", "backslash": "\\", "bracketright": "]", "asciicircum": "^", "underscore": "_", "grave": "`", "braceleft": "{", "bar": "|", "braceright": "}", "asciitilde": "~",
	"zero": "0", "one": "1", "two": "2", "three": "3", "four": "4", "five": "5", "six": "6", "seven": "7", "eight": "8", "nine": "9",
	"exclamdown": "¡", "cent": "¢", "sterling": "£", "fraction": "⁄", "yen": "¥", "florin": "ƒ", "section": "§", "currency": "¤", "quotedblleft": "“", "guillemotleft": "«", "guilsinglleft": "‹", "guilsinglright": "›", "fi": "fi", "fl": "fl", "ff": "ff", "ffi": "ffi", "ffl": "ffl", "endash": "–", "emdash": "—", "dagger": "†", "daggerdbl": "‡", "periodcentered": "·", "paragraph": "¶", "bullet": "•", "quotesinglbase": "‚", "quotedblbase": "„", "quotedblright": "”", "guillemotright": "»", "ellipsis": "…", "perthousand": "‰", "questiondown": "¿", "acute": "´", "circumflex": "ˆ", "tilde": "˜", "macron": "¯", "breve": "˘", "dotaccent": "˙", "dieresis": "¨", "ring": "˚", "cedilla": "¸", "hungarumlaut": "˝", "ogonek": "˛", "caron": "ˇ", "AE": "Æ", "ordfeminine": "ª", "Lslash": "Ł", "Oslash": "Ø", "OE": "Œ", "ordmasculine": "º", "ae": "æ", "dotlessi": "ı", "lslash": "ł", "oslash": "ø", "oe": "œ", "germandbls": "ß", "quoteleft": "‘", "quoteright": "’", "Euro": "€", "copyright": "©", "registered": "®", "trademark": "™", "minus": "−", "multiply": "×", "divide": "÷", "degree": "°", "mu": "µ",
}

func glyphUnicode(glyph string) (string, bool) {
	glyph = strings.SplitN(glyph, ".", 2)[0]
	// Common TeX Type 1 names for alternate Greek glyph shapes.
	if text, ok := map[string]string{"epsilon1": "ϵ", "theta1": "ϑ", "phi1": "ϕ", "sigma1": "ς", "omega1": "ϖ"}[glyph]; ok {
		return text, true
	}
	if strings.Contains(glyph, "_") {
		var out strings.Builder
		for _, part := range strings.Split(glyph, "_") {
			text, ok := glyphUnicode(part)
			if !ok {
				return "", false
			}
			out.WriteString(text)
		}
		return out.String(), true
	}
	if text, ok := glyphs[glyph]; ok {
		return text, true
	}
	if text, ok := adobeGlyphs[glyph]; ok {
		return text, true
	}
	if len(glyph) == 1 && (glyph[0] >= 'a' && glyph[0] <= 'z' || glyph[0] >= 'A' && glyph[0] <= 'Z') {
		return glyph, true
	}
	if strings.HasPrefix(glyph, "uni") && len(glyph) > 3 && (len(glyph)-3)%4 == 0 {
		raw := make([]byte, (len(glyph)-3)/2)
		for i := range raw {
			n, err := strconv.ParseUint(glyph[3+i*2:5+i*2], 16, 8)
			if err != nil {
				return "", false
			}
			raw[i] = byte(n)
		}
		text, err := unicodeString(raw)
		return text, err == nil
	}
	if strings.HasPrefix(glyph, "u") && len(glyph) >= 5 && len(glyph) <= 7 {
		n, err := strconv.ParseUint(glyph[1:], 16, 32)
		if err == nil && utf8.ValidRune(rune(n)) {
			return string(rune(n)), true
		}
	}
	for suffix, mark := range map[string]string{"grave": "\u0300", "acute": "\u0301", "circumflex": "\u0302", "tilde": "\u0303", "macron": "\u0304", "breve": "\u0306", "dotaccent": "\u0307", "dieresis": "\u0308", "ring": "\u030a", "hungarumlaut": "\u030b", "caron": "\u030c", "cedilla": "\u0327", "ogonek": "\u0328"} {
		if base, ok := strings.CutSuffix(glyph, suffix); ok && len(base) == 1 {
			return norm.NFC.String(base + mark), true
		}
	}
	return "", false
}
