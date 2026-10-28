package pdf

import (
	"encoding/binary"
	"fmt"
	"unicode"
	"unicode/utf8"
)

// Recover only explicitly encoded Unicode values, never infer Unicode from a
// CID or a glyph's numeric position. Ambiguous reverse mappings are excluded.
func (d *document) embeddedCIDUnicode(descendant dict) (map[int]string, error) {
	if descendant["FontDescriptor"] == nil {
		return nil, nil
	}
	desc, err := d.dictionary(descendant["FontDescriptor"])
	if err != nil {
		return nil, err
	}
	if desc["FontFile2"] == nil {
		return nil, nil
	}
	v, err := d.resolve(desc["FontFile2"])
	if err != nil {
		return nil, err
	}
	s, ok := v.(*stream)
	if !ok {
		return nil, fmt.Errorf("FontFile2 is not a stream")
	}
	data, err := d.decode(s)
	if err != nil {
		return nil, err
	}
	glyphs, err := trueTypeUnicode(data)
	if err != nil {
		return nil, err
	}
	mapValue, err := d.resolve(descendant["CIDToGIDMap"])
	if err != nil {
		return nil, err
	}
	result := make(map[int]string)
	if mapValue == nil || mapValue == name("Identity") {
		for gid, text := range glyphs {
			result[int(gid)] = text
		}
		return result, nil
	}
	s, ok = mapValue.(*stream)
	if !ok {
		return nil, unsupported("cid-glyph-map", "unsupported CIDToGIDMap")
	}
	data, err = d.decode(s)
	if err != nil {
		return nil, err
	}
	if len(data)%2 != 0 || len(data) > 2*65536 {
		return nil, fmt.Errorf("invalid CIDToGIDMap size")
	}
	for cid := 0; cid < len(data)/2; cid++ {
		if text, ok := glyphs[binary.BigEndian.Uint16(data[2*cid:])]; ok {
			result[cid] = text
		}
	}
	return result, nil
}

// OpenType cmap formats 4 and 12 cover BMP and supplementary Unicode mappings.
// See https://learn.microsoft.com/en-us/typography/opentype/spec/cmap.
func trueTypeUnicode(data []byte) (map[uint16]string, error) {
	if len(data) < 12 || (binary.BigEndian.Uint32(data) != 0x00010000 && string(data[:4]) != "true") {
		return nil, fmt.Errorf("invalid embedded TrueType header")
	}
	count := int(binary.BigEndian.Uint16(data[4:]))
	if count > (len(data)-12)/16 {
		return nil, fmt.Errorf("truncated TrueType table directory")
	}
	var cmap []byte
	for i := 0; i < count; i++ {
		record := data[12+16*i:]
		offset, length := uint64(binary.BigEndian.Uint32(record[8:])), uint64(binary.BigEndian.Uint32(record[12:]))
		if offset+length > uint64(len(data)) {
			return nil, fmt.Errorf("TrueType table outside font")
		}
		if string(record[:4]) == "cmap" {
			cmap = data[offset : offset+length]
		}
	}
	result := make(map[uint16]string)
	if cmap == nil {
		return result, nil
	}
	if len(cmap) < 4 || binary.BigEndian.Uint16(cmap) != 0 {
		return nil, fmt.Errorf("invalid TrueType cmap header")
	}
	count = int(binary.BigEndian.Uint16(cmap[2:]))
	if count > (len(cmap)-4)/8 {
		return nil, fmt.Errorf("truncated TrueType cmap directory")
	}
	ambiguous := make(map[uint16]bool)
	remaining := maxObjects
	put := func(code, gid uint32) error {
		remaining--
		if remaining < 0 {
			return fmt.Errorf("TrueType cmap exceeds mapping limit")
		}
		if gid == 0 || gid > 65535 || !utf8.ValidRune(rune(code)) || code > utf8.MaxRune {
			return nil
		}
		ch := rune(code)
		// Private-use/symbol encodings identify a font slot, not a character.
		if unicode.IsControl(ch) || unicode.Is(unicode.Co, ch) || ch == 0xfffe || ch == 0xffff {
			return nil
		}
		key, text := uint16(gid), string(ch)
		if previous, exists := result[key]; exists && previous != text {
			ambiguous[key] = true
		}
		if !ambiguous[key] {
			result[key] = text
		}
		return nil
	}
	seen := make(map[uint32]bool)
	for i := 0; i < count; i++ {
		record := cmap[4+8*i:]
		platform, encoding := binary.BigEndian.Uint16(record), binary.BigEndian.Uint16(record[2:])
		if !(platform == 0 && encoding != 5 || platform == 3 && (encoding == 1 || encoding == 10)) {
			continue
		}
		offset := binary.BigEndian.Uint32(record[4:])
		if seen[offset] {
			continue
		}
		seen[offset] = true
		if uint64(offset)+2 > uint64(len(cmap)) {
			return nil, fmt.Errorf("TrueType cmap offset outside table")
		}
		table := cmap[offset:]
		switch binary.BigEndian.Uint16(table) {
		case 4:
			if len(table) < 16 {
				return nil, fmt.Errorf("truncated TrueType cmap format 4")
			}
			length, segments := int(binary.BigEndian.Uint16(table[2:])), int(binary.BigEndian.Uint16(table[6:]))/2
			if length < 16 || length > len(table) || segments == 0 || binary.BigEndian.Uint16(table[6:])%2 != 0 || segments > (length-16)/8 {
				return nil, fmt.Errorf("invalid TrueType cmap format 4 length")
			}
			table = table[:length]
			previous := -1
			for segment := 0; segment < segments; segment++ {
				end := int(binary.BigEndian.Uint16(table[14+2*segment:]))
				start := int(binary.BigEndian.Uint16(table[16+2*segments+2*segment:]))
				delta := binary.BigEndian.Uint16(table[16+4*segments+2*segment:])
				at := 16 + 6*segments + 2*segment
				rangeOffset := int(binary.BigEndian.Uint16(table[at:]))
				if start > end || start <= previous || rangeOffset%2 != 0 {
					return nil, fmt.Errorf("invalid TrueType cmap segments")
				}
				previous = end
				for code := start; code <= end; code++ {
					gid := uint16(code) + delta
					if rangeOffset != 0 {
						index := at + rangeOffset + 2*(code-start)
						if index < 16+8*segments || index+2 > len(table) {
							return nil, fmt.Errorf("TrueType glyph offset outside cmap")
						}
						gid = binary.BigEndian.Uint16(table[index:])
						if gid != 0 {
							gid += delta
						}
					}
					if err := put(uint32(code), uint32(gid)); err != nil {
						return nil, err
					}
				}
			}
		case 12:
			if len(table) < 16 {
				return nil, fmt.Errorf("truncated TrueType cmap format 12")
			}
			length, groups := uint64(binary.BigEndian.Uint32(table[4:])), uint64(binary.BigEndian.Uint32(table[12:]))
			if length < 16 || length > uint64(len(table)) || groups > (length-16)/12 {
				return nil, fmt.Errorf("invalid TrueType cmap format 12 length")
			}
			var previous uint32
			for group := uint64(0); group < groups; group++ {
				record := table[16+12*group:]
				start, end, gid := binary.BigEndian.Uint32(record), binary.BigEndian.Uint32(record[4:]), binary.BigEndian.Uint32(record[8:])
				if start > end || end > utf8.MaxRune || (group > 0 && start <= previous) || gid > 65535 || end-start > 65535-gid {
					return nil, fmt.Errorf("invalid TrueType cmap groups")
				}
				previous = end
				for code := start; code <= end; code++ {
					if err := put(code, gid+code-start); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	for gid := range ambiguous {
		delete(result, gid)
	}
	return result, nil
}
