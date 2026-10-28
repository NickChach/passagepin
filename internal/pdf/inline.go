package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"fmt"
	"io"
)

// An inline image is binary data inside the content stream. Its end must be
// derived from its dimensions or outer filter, never an arbitrary EI substring.
func (r *interpreter) skipInlineImage(p *syntax, data []byte) error {
	options := dict{}
	for entries := 0; ; entries++ {
		if entries > 64 {
			return fmt.Errorf("inline image dictionary too large")
		}
		key, err := p.value(0)
		if err != nil {
			return fmt.Errorf("inline image dictionary: %w", err)
		}
		if key == keyword("ID") {
			break
		}
		n, ok := key.(name)
		if !ok {
			return fmt.Errorf("inline image dictionary key is not a name")
		}
		v, err := p.value(0)
		if err != nil {
			return err
		}
		options[string(n)] = v
	}
	separator, err := p.read()
	if err != nil || !white(separator) {
		return fmt.Errorf("inline image ID requires whitespace")
	}
	if separator == '\r' {
		if next, _ := p.peek(); next == '\n' {
			p.read()
		}
	}
	start := int(p.pos)
	if start > len(data) {
		return io.ErrUnexpectedEOF
	}
	length, err := r.inlineImageLength(options, data[start:])
	if err != nil {
		return err
	}
	end := start + length
	if end >= len(data) || !white(data[end]) {
		return fmt.Errorf("inline image data lacks an EI separator")
	}
	// Reset the token reader exactly after the encoded payload. This also
	// discards buffered bytes which belong to the binary image, not PDF syntax.
	*p = *newSyntax(bytes.NewReader(data[end:]), int64(end))
	v, err := p.next()
	if err != nil || v != keyword("EI") {
		return fmt.Errorf("inline image data does not end at EI")
	}
	return nil
}

func inlineOption(options dict, long, short string) value {
	if v, ok := options[long]; ok {
		return v
	}
	return options[short]
}

func (r *interpreter) inlineImageLength(options dict, data []byte) (int, error) {
	filter := inlineOption(options, "Filter", "F")
	if list, ok := filter.(array); ok {
		if len(list) == 0 {
			filter = nil
		} else {
			filter = list[0]
		}
	}
	if filter == nil {
		width, okW := integer(inlineOption(options, "Width", "W"))
		height, okH := integer(inlineOption(options, "Height", "H"))
		bits, okB := integer(inlineOption(options, "BitsPerComponent", "BPC"))
		components := 1
		if mask, _ := inlineOption(options, "ImageMask", "IM").(bool); mask {
			bits, okB = 1, true
		} else {
			var err error
			components, err = r.imageComponents(inlineOption(options, "ColorSpace", "CS"), 0)
			if err != nil {
				return 0, err
			}
		}
		if !okW || !okH || !okB || width < 1 || height < 1 || width > maxStream || height > maxStream || (bits != 1 && bits != 2 && bits != 4 && bits != 8 && bits != 16) {
			return 0, fmt.Errorf("invalid inline image dimensions or component depth")
		}
		row := (width*int64(components)*bits + 7) / 8
		if row > maxStream || height > maxStream/row || row*height > int64(len(data)) {
			return 0, fmt.Errorf("inline image data is truncated or oversized")
		}
		return int(row * height), nil
	}
	n, ok := filter.(name)
	if !ok {
		return 0, fmt.Errorf("inline image filter is not a name")
	}
	switch n {
	case "FlateDecode", "Fl":
		input := bytes.NewReader(data) // ReadByte prevents zlib from reading past its checksum.
		reader, err := zlib.NewReader(input)
		if err != nil {
			return 0, fmt.Errorf("inline image FlateDecode: %w", err)
		}
		err = r.discardImage(reader)
		closeErr := reader.Close()
		if err != nil {
			return 0, err
		}
		if closeErr != nil {
			return 0, closeErr
		}
		return len(data) - input.Len(), nil
	case "ASCII85Decode", "A85":
		end := bytes.Index(data, []byte("~>"))
		if end < 0 {
			return 0, fmt.Errorf("inline ASCII85 image lacks terminator")
		}
		payload := bytes.TrimPrefix(bytes.TrimSpace(data[:end]), []byte("<~"))
		if err := r.discardImage(ascii85.NewDecoder(bytes.NewReader(payload))); err != nil {
			return 0, err
		}
		return end + 2, nil
	case "ASCIIHexDecode", "AHx":
		for i, b := range data {
			if b == '>' {
				return i + 1, nil
			}
			if !white(b) && !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F') {
				return 0, fmt.Errorf("invalid inline ASCIIHex image")
			}
		}
		return 0, fmt.Errorf("inline ASCIIHex image lacks terminator")
	case "RunLengthDecode", "RL":
		decoded := 0
		for i := 0; i < len(data); {
			n := int(data[i])
			i++
			if n == 128 {
				return i, nil
			}
			consumed, expanded := n+1, n+1
			if n > 128 {
				consumed, expanded = 1, 257-n
			}
			if consumed > len(data)-i {
				return 0, fmt.Errorf("truncated inline RunLength image")
			}
			decoded += expanded
			if decoded > maxStream {
				return 0, fmt.Errorf("inline image exceeds decoded size limit")
			}
			i += consumed
		}
		return 0, fmt.Errorf("inline RunLength image lacks terminator")
	case "DCTDecode", "DCT":
		return jpegLength(data)
	default:
		return 0, unsupported("inline-image-filter", "unsupported inline image filter /%s", n)
	}
}

func (r *interpreter) discardImage(reader io.Reader) error {
	n, err := io.Copy(io.Discard, io.LimitReader(contextReader{r.d, reader}, maxStream+1))
	if err != nil {
		return err
	}
	if n > maxStream {
		return fmt.Errorf("inline image exceeds decoded size limit")
	}
	return nil
}

func (r *interpreter) imageComponents(v value, depth int) (int, error) {
	if depth > 8 {
		return 0, fmt.Errorf("inline image colour space nesting too deep")
	}
	v, err := r.d.resolve(v)
	if err != nil {
		return 0, err
	}
	if n, ok := v.(name); ok {
		switch n {
		case "DeviceGray", "G":
			return 1, nil
		case "DeviceRGB", "RGB":
			return 3, nil
		case "DeviceCMYK", "CMYK":
			return 4, nil
		}
		spaces, err := r.d.dictionary(r.resources["ColorSpace"])
		if err != nil {
			return 0, err
		}
		if next, ok := spaces[string(n)]; ok {
			return r.imageComponents(next, depth+1)
		}
	}
	if list, ok := v.(array); ok && len(list) > 0 {
		switch list[0] {
		case name("Indexed"), name("I"), name("Separation"), name("CalGray"):
			return 1, nil
		case name("CalRGB"), name("Lab"):
			return 3, nil
		case name("DeviceN"):
			if len(list) > 1 {
				if names, ok := list[1].(array); ok && len(names) > 0 && len(names) <= 32 {
					return len(names), nil
				}
			}
		case name("ICCBased"):
			if len(list) > 1 {
				profile, err := r.d.resolve(list[1])
				if err != nil {
					return 0, err
				}
				if s, ok := profile.(*stream); ok {
					if n, ok := integer(s.dict["N"]); ok && (n == 1 || n == 3 || n == 4) {
						return int(n), nil
					}
				}
			}
		}
	}
	return 0, unsupported("inline-image-colorspace", "unsupported inline image colour space")
}

func jpegLength(data []byte) (int, error) {
	if len(data) < 2 || data[0] != 0xff || data[1] != 0xd8 {
		return 0, fmt.Errorf("inline JPEG lacks SOI marker")
	}
	for pos := 2; pos < len(data); {
		if data[pos] != 0xff {
			return 0, fmt.Errorf("invalid inline JPEG marker")
		}
		for pos < len(data) && data[pos] == 0xff {
			pos++
		}
		if pos == len(data) {
			break
		}
		marker := data[pos]
		pos++
		if marker == 0xd9 {
			return pos, nil
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if marker == 0 || marker == 0xd8 || pos+2 > len(data) {
			break
		}
		length := int(data[pos])<<8 | int(data[pos+1])
		if length < 2 || length > len(data)-pos {
			break
		}
		pos += length
		if marker == 0xda {
			for pos < len(data) {
				if data[pos] != 0xff {
					pos++
					continue
				}
				if pos+1 >= len(data) {
					break
				}
				next := data[pos+1]
				if next == 0 || next >= 0xd0 && next <= 0xd7 {
					pos += 2
					continue
				}
				break
			}
		}
	}
	return 0, fmt.Errorf("truncated inline JPEG")
}
