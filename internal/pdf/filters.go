package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"encoding/hex"
	"fmt"
	"io"
)

type contextReader struct {
	d *document
	r io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.d.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
func (d *document) decode(s *stream) ([]byte, error) {
	var data []byte
	if s.data != nil {
		data = s.data
	} else {
		v, err := d.resolve(s.dict["Length"])
		if err != nil {
			return nil, err
		}
		length, ok := integer(v)
		if !ok || length < 0 || length > maxStream || s.offset < 0 || s.offset > d.size || length > d.size-s.offset {
			return nil, fmt.Errorf("invalid or oversized PDF stream Length")
		}
		data = make([]byte, int(length))
		if _, err := d.file.ReadAt(data, s.offset); err != nil && !(length == 0 && err == io.EOF) {
			return nil, err
		}
		p, err := d.parser(s.offset + length)
		if err != nil {
			return nil, err
		}
		end, err := p.next()
		if err != nil || end != keyword("endstream") {
			return nil, fmt.Errorf("stream Length does not end at endstream")
		}
	}
	filter, err := d.resolve(s.dict["Filter"])
	if err != nil {
		return nil, err
	}
	if filter == nil {
		return data, nil
	}
	var filters array
	if a, ok := filter.(array); ok {
		filters = a
	} else {
		filters = array{filter}
	}
	params, err := d.resolve(s.dict["DecodeParms"])
	if err != nil {
		return nil, err
	}
	var parameters array
	if a, ok := params.(array); ok {
		parameters = a
	} else {
		parameters = array{params}
	}
	for i, filter := range filters {
		if err := d.ctx.Err(); err != nil {
			return nil, err
		}
		filter, err = d.resolve(filter)
		if err != nil {
			return nil, err
		}
		n, ok := filter.(name)
		if !ok {
			return nil, fmt.Errorf("stream filter is not a name")
		}
		options := dict{}
		if i < len(parameters) && parameters[i] != nil {
			options, err = d.dictionary(parameters[i])
			if err != nil {
				return nil, err
			}
		}
		switch n {
		case "FlateDecode", "Fl":
			r, err := zlib.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, fmt.Errorf("FlateDecode: %w", err)
			}
			data, err = io.ReadAll(io.LimitReader(contextReader{d, r}, maxStream+1))
			closeErr := r.Close()
			if err != nil {
				return nil, err
			}
			if closeErr != nil {
				return nil, closeErr
			}
		case "ASCIIHexDecode", "AHx":
			var raw []byte
			for _, b := range data {
				if b == '>' {
					break
				}
				if !white(b) {
					raw = append(raw, b)
				}
			}
			if len(raw)%2 != 0 {
				raw = append(raw, '0')
			}
			data = make([]byte, hex.DecodedLen(len(raw)))
			if _, err := hex.Decode(data, raw); err != nil {
				return nil, err
			}
		case "ASCII85Decode", "A85":
			data = bytes.TrimSpace(data)
			data = bytes.TrimPrefix(data, []byte("<~"))
			end := bytes.Index(data, []byte("~>"))
			if end < 0 {
				return nil, fmt.Errorf("ASCII85 stream missing terminator")
			}
			out, err := io.ReadAll(io.LimitReader(contextReader{d, ascii85.NewDecoder(bytes.NewReader(data[:end]))}, maxStream+1))
			if err != nil {
				return nil, err
			}
			data = out
		case "RunLengthDecode", "RL":
			data, err = runLength(data)
			if err != nil {
				return nil, err
			}
		case "LZWDecode", "LZW":
			early := int64(1)
			if v, exists := options["EarlyChange"]; exists {
				var ok bool
				early, ok = integer(v)
				if !ok || early < 0 || early > 1 {
					return nil, fmt.Errorf("invalid LZW EarlyChange")
				}
			}
			data, err = decodeLZW(data, int(early))
			if err != nil {
				return nil, err
			}
		default:
			return nil, unsupported("stream-filter", "unsupported PDF stream filter /%s", n)
		}
		if len(data) > maxStream {
			return nil, fmt.Errorf("decoded PDF stream exceeds %d MiB", maxStream>>20)
		}
		if n == "FlateDecode" || n == "Fl" || n == "LZWDecode" || n == "LZW" {
			data, err = undoPredictor(data, options)
			if err != nil {
				return nil, err
			}
		}
	}
	return data, nil
}
func runLength(data []byte) ([]byte, error) {
	var out []byte
	for i := 0; i < len(data); {
		n := int(data[i])
		i++
		if n == 128 {
			return out, nil
		}
		count := n + 1
		if n > 128 {
			count = 257 - n
		}
		if len(out) > maxStream-count {
			return nil, fmt.Errorf("RunLength stream too large")
		}
		if n < 128 {
			if count > len(data)-i {
				return nil, fmt.Errorf("truncated RunLength stream")
			}
			out = append(out, data[i:i+count]...)
			i += count
		} else {
			if i >= len(data) {
				return nil, fmt.Errorf("truncated RunLength repetition")
			}
			for j := 0; j < count; j++ {
				out = append(out, data[i])
			}
			i++
		}
	}
	return nil, fmt.Errorf("RunLength stream missing terminator")
}
func decodeLZW(data []byte, early int) ([]byte, error) {
	table := make([][]byte, 4096)
	for i := 0; i < 256; i++ {
		table[i] = []byte{byte(i)}
	}
	width, next, bit := 9, 258, 0
	var previous, out []byte
	for {
		if bit+width > len(data)*8 {
			return nil, fmt.Errorf("truncated LZW stream")
		}
		code := 0
		for i := 0; i < width; i++ {
			code = code<<1 | int((data[bit/8]>>uint(7-bit%8))&1)
			bit++
		}
		if code == 256 {
			width, next, previous = 9, 258, nil
			continue
		}
		if code == 257 {
			return out, nil
		}
		var word []byte
		if code < next && code < len(table) {
			word = table[code]
		} else if code == next && len(previous) > 0 {
			word = append(append([]byte(nil), previous...), previous[0])
		}
		if len(word) == 0 {
			return nil, fmt.Errorf("invalid LZW code")
		}
		if len(out) > maxStream-len(word) {
			return nil, fmt.Errorf("LZW stream too large")
		}
		out = append(out, word...)
		if len(previous) > 0 && next < 4096 {
			table[next] = append(append([]byte(nil), previous...), word[0])
			next++
			if width < 12 && next+early == 1<<width {
				width++
			}
		}
		previous = word
	}
}
func undoPredictor(data []byte, options dict) ([]byte, error) {
	get := func(key string, fallback int64) (int64, error) {
		v, ok := options[key]
		if !ok {
			return fallback, nil
		}
		n, ok := integer(v)
		if !ok {
			return 0, fmt.Errorf("invalid predictor %s", key)
		}
		return n, nil
	}
	predictor, err := get("Predictor", 1)
	if err != nil {
		return nil, err
	}
	if predictor == 1 {
		return data, nil
	}
	colors, err := get("Colors", 1)
	if err != nil {
		return nil, err
	}
	columns, err := get("Columns", 1)
	if err != nil {
		return nil, err
	}
	bits, err := get("BitsPerComponent", 8)
	if err != nil {
		return nil, err
	}
	if colors < 1 || colors > 32 || columns < 1 || columns > maxStream || (bits != 1 && bits != 2 && bits != 4 && bits != 8 && bits != 16) {
		return nil, fmt.Errorf("invalid predictor parameters")
	}
	row64 := (colors*columns*bits + 7) / 8
	if row64 < 1 || row64 > maxStream {
		return nil, fmt.Errorf("predictor row too large")
	}
	row := int(row64)
	bpp := int((colors*bits + 7) / 8)
	if predictor == 2 {
		if bits != 8 {
			return nil, fmt.Errorf("TIFF predictor supports only 8-bit components")
		}
		if len(data)%row != 0 {
			return nil, fmt.Errorf("truncated TIFF predictor data")
		}
		out := append([]byte(nil), data...)
		for start := 0; start < len(out); start += row {
			for j := bpp; j < row; j++ {
				out[start+j] += out[start+j-bpp]
			}
		}
		return out, nil
	}
	if predictor < 10 || predictor > 15 {
		return nil, unsupported("stream-predictor", "unsupported predictor %d", predictor)
	}
	if len(data)%(row+1) != 0 {
		return nil, fmt.Errorf("truncated PNG predictor data")
	}
	out := make([]byte, 0, len(data))
	previous := make([]byte, row)
	for start := 0; start < len(data); start += row + 1 {
		filter := data[start]
		current := append([]byte(nil), data[start+1:start+1+row]...)
		for j := 0; j < row; j++ {
			var a, b, c byte
			b = previous[j]
			if j >= bpp {
				a = current[j-bpp]
				c = previous[j-bpp]
			}
			switch filter {
			case 0:
			case 1:
				current[j] += a
			case 2:
				current[j] += b
			case 3:
				current[j] += byte((int(a) + int(b)) / 2)
			case 4:
				current[j] += paeth(a, b, c)
			default:
				return nil, fmt.Errorf("invalid PNG predictor filter %d", filter)
			}
		}
		out = append(out, current...)
		previous = current
	}
	return out, nil
}
func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	abs := func(x int) int {
		if x < 0 {
			return -x
		}
		return x
	}
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	if pa <= pb && pa <= pc {
		return a
	}
	if pb <= pc {
		return b
	}
	return c
}
