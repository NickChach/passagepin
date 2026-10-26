package pdf

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"strconv"
)

const (
	maxStream  = 64 << 20
	maxToken   = 8 << 20
	maxObjects = 1_000_000
	maxDepth   = 64
)

type value = any
type name string
type keyword string
type pdfString []byte
type array []value
type dict map[string]value
type reference struct{ number, generation int64 }
type stream struct {
	dict   dict
	offset int64
	data   []byte
}
type syntax struct {
	r      *bufio.Reader
	pos    int64
	pushed []value
}

func newSyntax(r io.Reader, offset int64) *syntax { return &syntax{r: bufio.NewReader(r), pos: offset} }
func white(b byte) bool                           { return b == 0 || b == 9 || b == 10 || b == 12 || b == 13 || b == 32 }
func delimiter(b byte) bool {
	return white(b) || b == '(' || b == ')' || b == '<' || b == '>' || b == '[' || b == ']' || b == '{' || b == '}' || b == '/' || b == '%'
}
func (p *syntax) read() (byte, error) {
	b, err := p.r.ReadByte()
	if err == nil {
		p.pos++
	}
	return b, err
}
func (p *syntax) peek() (byte, error) {
	b, err := p.r.Peek(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}
func (p *syntax) skip() error {
	for {
		b, err := p.peek()
		if err != nil {
			return err
		}
		if white(b) {
			p.read()
			continue
		}
		if b != '%' {
			return nil
		}
		for {
			c, err := p.read()
			if err != nil {
				return err
			}
			if c == '\n' || c == '\r' {
				break
			}
		}
	}
}
func (p *syntax) next() (value, error) {
	if len(p.pushed) > 0 {
		v := p.pushed[0]
		p.pushed = p.pushed[1:]
		return v, nil
	}
	if err := p.skip(); err != nil {
		return nil, err
	}
	b, err := p.read()
	if err != nil {
		return nil, err
	}
	switch b {
	case '(':
		return p.literal()
	case '[', ']', '{', '}':
		return keyword(string(b)), nil
	case '<':
		if c, _ := p.peek(); c == '<' {
			p.read()
			return keyword("<<"), nil
		}
		var raw []byte
		for {
			c, err := p.read()
			if err != nil {
				return nil, fmt.Errorf("unterminated hex string")
			}
			if c == '>' {
				break
			}
			if white(c) {
				continue
			}
			raw = append(raw, c)
			if len(raw) > maxToken {
				return nil, fmt.Errorf("hex string too large")
			}
		}
		if len(raw)%2 != 0 {
			raw = append(raw, '0')
		}
		out := make([]byte, hex.DecodedLen(len(raw)))
		if _, err := hex.Decode(out, raw); err != nil {
			return nil, err
		}
		return pdfString(out), nil
	case '>':
		if c, _ := p.read(); c != '>' {
			return nil, fmt.Errorf("unexpected >")
		}
		return keyword(">>"), nil
	case ')':
		return nil, fmt.Errorf("unexpected )")
	}
	isName := b == '/'
	var raw []byte
	if !isName {
		raw = append(raw, b)
	}
	for {
		c, err := p.peek()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if delimiter(c) {
			break
		}
		p.read()
		raw = append(raw, c)
		if len(raw) > maxToken {
			return nil, fmt.Errorf("token too large")
		}
	}
	if isName {
		out := make([]byte, 0, len(raw))
		for i := 0; i < len(raw); i++ {
			if raw[i] == '#' {
				if i+2 >= len(raw) {
					return nil, fmt.Errorf("invalid name escape")
				}
				b, err := strconv.ParseUint(string(raw[i+1:i+3]), 16, 8)
				if err != nil {
					return nil, err
				}
				out = append(out, byte(b))
				i += 2
			} else {
				out = append(out, raw[i])
			}
		}
		return name(out), nil
	}
	s := string(raw)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
		return n, nil
	}
	switch s {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	return keyword(s), nil
}
func (p *syntax) literal() (value, error) {
	var out []byte
	depth := 1
	for depth > 0 {
		b, err := p.read()
		if err != nil {
			return nil, fmt.Errorf("unterminated literal string")
		}
		switch b {
		case '(':
			depth++
			out = append(out, b)
		case ')':
			depth--
			if depth > 0 {
				out = append(out, b)
			}
		case '\\':
			c, err := p.read()
			if err != nil {
				return nil, incompleteValue(err)
			}
			switch c {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\r':
				if c, _ := p.peek(); c == '\n' {
					p.read()
				}
			case '\n':
			default:
				if c >= '0' && c <= '7' {
					n := int(c - '0')
					for i := 0; i < 2; i++ {
						next, err := p.peek()
						if err != nil || next < '0' || next > '7' {
							break
						}
						p.read()
						n = n*8 + int(next-'0')
					}
					out = append(out, byte(n))
				} else {
					out = append(out, c)
				}
			}
		case '\r':
			if c, _ := p.peek(); c == '\n' {
				p.read()
			}
			out = append(out, '\n')
		default:
			out = append(out, b)
		}
		if len(out) > maxToken || depth > maxDepth {
			return nil, fmt.Errorf("literal string exceeds limits")
		}
	}
	return pdfString(out), nil
}
func (p *syntax) value(depth int) (value, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("PDF value nesting too deep")
	}
	v, err := p.next()
	if err != nil {
		return nil, err
	}
	if k, ok := v.(keyword); ok {
		switch k {
		case "[":
			a := array{}
			for {
				v, err := p.next()
				if err != nil {
					return nil, incompleteValue(err)
				}
				if k, ok := v.(keyword); ok && k == "]" {
					return a, nil
				}
				p.pushed = append([]value{v}, p.pushed...)
				item, err := p.value(depth + 1)
				if err != nil {
					return nil, incompleteValue(err)
				}
				a = append(a, item)
				if len(a) > maxObjects {
					return nil, fmt.Errorf("array too large")
				}
			}
		case "<<":
			d := dict{}
			for {
				key, err := p.next()
				if err != nil {
					return nil, incompleteValue(err)
				}
				if k, ok := key.(keyword); ok && k == ">>" {
					return d, nil
				}
				n, ok := key.(name)
				if !ok {
					return nil, fmt.Errorf("dictionary key is not a name")
				}
				v, err := p.value(depth + 1)
				if err != nil {
					return nil, incompleteValue(err)
				}
				d[string(n)] = v
				if len(d) > maxObjects {
					return nil, fmt.Errorf("dictionary too large")
				}
			}
		case "]", ">>":
			return nil, fmt.Errorf("unexpected %s", k)
		}
	}
	if n, ok := v.(int64); ok {
		second, err := p.next()
		if err == io.EOF {
			return v, nil
		}
		if err != nil {
			return nil, err
		}
		if gen, ok := second.(int64); ok {
			third, err := p.next()
			if err != nil && err != io.EOF {
				return nil, err
			}
			if k, ok := third.(keyword); ok && k == "R" {
				if n < 0 || gen < 0 {
					return nil, fmt.Errorf("negative reference")
				}
				return reference{n, gen}, nil
			}
			if err == nil {
				p.pushed = append([]value{third}, p.pushed...)
			}
		}
		p.pushed = append([]value{second}, p.pushed...)
	}
	return v, nil
}

// Only EOF before a new value is clean completion. Once a composite or an
// escape has started, running out of input means the PDF value is truncated.
func incompleteValue(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

func integer(v value) (int64, bool) { n, ok := v.(int64); return n, ok }
func number(v value) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}
