package pdf

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"strings"
)

type matrix [6]float64

func identity() matrix { return matrix{1, 0, 0, 1, 0, 0} }
func (a matrix) mul(b matrix) matrix {
	return matrix{a[0]*b[0] + a[2]*b[1], a[1]*b[0] + a[3]*b[1], a[0]*b[2] + a[2]*b[3], a[1]*b[2] + a[3]*b[3], a[0]*b[4] + a[2]*b[5] + a[4], a[1]*b[4] + a[3]*b[5] + a[5]}
}
func (m matrix) point(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

type textState struct {
	ctm, tm, line                                     matrix
	font                                              *font
	size, charSpace, wordSpace, hscale, leading, rise float64
	inside                                            bool
}

func (s *textState) advance(distance float64) {
	s.tm[4] += s.tm[0] * distance
	s.tm[5] += s.tm[1] * distance
}
func (s *textState) move(x, y float64) { s.line = s.line.mul(matrix{1, 0, 0, 1, x, y}); s.tm = s.line }

type textOutput struct {
	text        strings.Builder
	x, y        float64
	hasPosition bool
}

func (o *textOutput) emit(s *textState, text string, advance float64) error {
	x, y := s.ctm.point(s.tm[4]+s.tm[2]*s.rise, s.tm[5]+s.tm[3]*s.rise)
	if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) || math.IsNaN(advance) || math.IsInf(advance, 0) {
		return fmt.Errorf("invalid text coordinates")
	}
	if text != "" {
		size := math.Abs(s.size) * math.Hypot(s.ctm[0], s.ctm[1])
		if o.hasPosition && o.text.Len() > 0 {
			if math.Abs(y-o.y) > max(1, size*.35) || x < o.x-max(2, size) {
				o.text.WriteByte('\n')
			} else if x-o.x > max(0.5, size*.18) {
				o.text.WriteByte(' ')
			}
		}
		if o.text.Len() > maxStream-len(text) {
			return fmt.Errorf("extracted page text too large")
		}
		o.text.WriteString(text)
	}
	s.advance(advance)
	o.x, o.y = s.ctm.point(s.tm[4]+s.tm[2]*s.rise, s.tm[5]+s.tm[3]*s.rise)
	o.hasPosition = true
	return nil
}

type interpreter struct {
	d          *document
	resources  dict
	fonts      map[name]*font
	state      textState
	stack      []textState
	out        *textOutput
	forms      map[*stream]bool
	operations *int
}

func (d *document) pageText(ctx context.Context, page pageInfo) (string, error) {
	d.ctx = ctx
	resources := dict{}
	if page.resources != nil {
		var err error
		resources, err = d.dictionary(page.resources)
		if err != nil {
			return "", err
		}
	}
	data, err := d.content(page.object["Contents"])
	if err != nil {
		return "", err
	}
	count := 0
	runner := &interpreter{d: d, resources: resources, fonts: map[name]*font{}, state: textState{ctm: identity(), tm: identity(), line: identity(), hscale: 1}, out: &textOutput{}, forms: map[*stream]bool{}, operations: &count}
	if err := runner.run(data, 0); err != nil {
		return "", err
	}
	return strings.Join(strings.Fields(runner.out.text.String()), " "), nil
}

// Content arrays are one logical stream: an operator's operands can cross a
// stream boundary. Only streams belonging to the selected page are decoded.
func (d *document) content(v value) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	v, err := d.resolve(v)
	if err != nil {
		return nil, err
	}
	if s, ok := v.(*stream); ok {
		return d.decode(s)
	}
	list, ok := v.(array)
	if !ok {
		return nil, fmt.Errorf("page Contents must be a stream or array")
	}
	var all []byte
	for _, item := range list {
		v, err := d.resolve(item)
		if err != nil {
			return nil, err
		}
		s, ok := v.(*stream)
		if !ok {
			return nil, fmt.Errorf("Contents array item is not a stream")
		}
		data, err := d.decode(s)
		if err != nil {
			return nil, err
		}
		if len(all) > maxStream-len(data)-1 {
			return nil, fmt.Errorf("page content exceeds limit")
		}
		all = append(all, data...)
		all = append(all, '\n')
	}
	return all, nil
}
func (r *interpreter) selectFont(n name, size float64) error {
	f, exists := r.fonts[n]
	if !exists {
		fonts, err := r.d.dictionary(r.resources["Font"])
		if err != nil {
			return err
		}
		raw, exists := fonts[string(n)]
		if !exists {
			return fmt.Errorf("missing font /%s", n)
		}
		f, err = r.d.loadFont(raw)
		if err != nil {
			return fmt.Errorf("font /%s: %w", n, err)
		}
		r.fonts[n] = f
	}
	r.state.font, r.state.size = f, size
	return nil
}
func (r *interpreter) show(v value) error {
	raw, ok := v.(pdfString)
	if !ok {
		return fmt.Errorf("text-show operand must be a string")
	}
	if !r.state.inside || r.state.font == nil {
		return fmt.Errorf("text shown without BT and a selected font")
	}
	text, width, spaces, glyphs, err := r.state.font.decode(raw)
	if err != nil {
		return err
	}
	advance := (width/1000*r.state.size + float64(glyphs)*r.state.charSpace + float64(spaces)*r.state.wordSpace) * r.state.hscale
	return r.out.emit(&r.state, text, advance)
}
func (r *interpreter) run(data []byte, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("Form XObject nesting too deep")
	}
	p := newSyntax(bytes.NewReader(data), 0)
	var args []value
	for {
		if err := r.d.ctx.Err(); err != nil {
			return err
		}
		v, err := p.value(0)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("PDF content syntax: %w", err)
		}
		op, ok := v.(keyword)
		if !ok {
			args = append(args, v)
			if len(args) > maxObjects {
				return fmt.Errorf("too many content operands")
			}
			continue
		}
		*r.operations++
		if *r.operations > maxObjects {
			return fmt.Errorf("too many page content operations")
		}
		if op == "BI" {
			return fmt.Errorf("inline images are not supported; use PDFs with image XObjects")
		}
		if err := r.operator(op, args, depth); err != nil {
			return fmt.Errorf("operator %s: %w", op, err)
		}
		args = nil
	}
	if len(args) > 0 {
		return fmt.Errorf("page content ends with unused operands")
	}
	if len(r.stack) > 0 {
		return fmt.Errorf("unbalanced graphics state saves")
	}
	return nil
}
func (r *interpreter) operator(op keyword, args []value, depth int) error {
	count := func(n int) error {
		if len(args) != n {
			return fmt.Errorf("expected %d operands, got %d", n, len(args))
		}
		return nil
	}
	nums := func(n int) ([]float64, error) {
		if err := count(n); err != nil {
			return nil, err
		}
		result := make([]float64, n)
		for i, v := range args {
			x, ok := number(v)
			if !ok || math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 1e12 {
				return nil, fmt.Errorf("invalid numeric operand")
			}
			result[i] = x
		}
		return result, nil
	}
	switch op {
	case "BT":
		if err := count(0); err != nil {
			return err
		}
		if r.state.inside {
			return fmt.Errorf("nested text object")
		}
		r.state.inside = true
		r.state.tm, r.state.line = identity(), identity()
	case "ET":
		if err := count(0); err != nil {
			return err
		}
		if !r.state.inside {
			return fmt.Errorf("ET without BT")
		}
		r.state.inside = false
	case "Tf":
		if err := count(2); err != nil {
			return err
		}
		n, ok := args[0].(name)
		size, okSize := number(args[1])
		if !ok || !okSize || math.IsNaN(size) || math.IsInf(size, 0) {
			return fmt.Errorf("invalid Tf operands")
		}
		return r.selectFont(n, size)
	case "Tj":
		if err := count(1); err != nil {
			return err
		}
		return r.show(args[0])
	case "TJ":
		if err := count(1); err != nil {
			return err
		}
		items, ok := args[0].(array)
		if !ok {
			return fmt.Errorf("TJ requires an array")
		}
		for _, item := range items {
			if n, ok := number(item); ok {
				if math.IsNaN(n) || math.IsInf(n, 0) {
					return fmt.Errorf("invalid TJ adjustment")
				}
				r.state.advance(-n / 1000 * r.state.size * r.state.hscale)
			} else if err := r.show(item); err != nil {
				return err
			}
		}
	case "'":
		if err := count(1); err != nil {
			return err
		}
		r.state.move(0, -r.state.leading)
		return r.show(args[0])
	case "\"":
		if err := count(3); err != nil {
			return err
		}
		word, ok := number(args[0])
		char, okChar := number(args[1])
		if !ok || !okChar {
			return fmt.Errorf("invalid spacing")
		}
		r.state.wordSpace, r.state.charSpace = word, char
		r.state.move(0, -r.state.leading)
		return r.show(args[2])
	case "Td", "TD":
		v, err := nums(2)
		if err != nil {
			return err
		}
		if op == "TD" {
			r.state.leading = -v[1]
		}
		r.state.move(v[0], v[1])
	case "Tm":
		v, err := nums(6)
		if err != nil {
			return err
		}
		copy(r.state.tm[:], v)
		r.state.line = r.state.tm
	case "T*":
		if err := count(0); err != nil {
			return err
		}
		r.state.move(0, -r.state.leading)
	case "Tc", "Tw", "Tz", "TL", "Ts":
		v, err := nums(1)
		if err != nil {
			return err
		}
		switch op {
		case "Tc":
			r.state.charSpace = v[0]
		case "Tw":
			r.state.wordSpace = v[0]
		case "Tz":
			r.state.hscale = v[0] / 100
		case "TL":
			r.state.leading = v[0]
		case "Ts":
			r.state.rise = v[0]
		}
	case "q":
		if err := count(0); err != nil {
			return err
		}
		if len(r.stack) >= maxDepth {
			return fmt.Errorf("graphics state stack too deep")
		}
		r.stack = append(r.stack, r.state)
	case "Q":
		if err := count(0); err != nil {
			return err
		}
		if len(r.stack) == 0 {
			return fmt.Errorf("Q without q")
		}
		r.state = r.stack[len(r.stack)-1]
		r.stack = r.stack[:len(r.stack)-1]
	case "cm":
		v, err := nums(6)
		if err != nil {
			return err
		}
		var m matrix
		copy(m[:], v)
		r.state.ctm = r.state.ctm.mul(m)
	case "Do":
		if err := count(1); err != nil {
			return err
		}
		n, ok := args[0].(name)
		if !ok {
			return fmt.Errorf("Do requires a name")
		}
		return r.form(n, depth)
	case "gs":
		if err := count(1); err != nil {
			return err
		}
		n, ok := args[0].(name)
		if !ok {
			return fmt.Errorf("gs requires a name")
		}
		states, err := r.d.dictionary(r.resources["ExtGState"])
		if err != nil {
			return err
		}
		state, err := r.d.dictionary(states[string(n)])
		if err != nil {
			return err
		}
		if raw := state["Font"]; raw != nil {
			v, err := r.d.resolve(raw)
			if err != nil {
				return err
			}
			list, ok := v.(array)
			if !ok || len(list) != 2 {
				return fmt.Errorf("invalid graphics state font")
			}
			f, err := r.d.loadFont(list[0])
			if err != nil {
				return err
			}
			size, ok := number(list[1])
			if !ok {
				return fmt.Errorf("invalid graphics state font size")
			}
			r.state.font, r.state.size = f, size
		}
	}
	return nil
}
func (r *interpreter) form(n name, depth int) error {
	objects, err := r.d.dictionary(r.resources["XObject"])
	if err != nil {
		return err
	}
	v, err := r.d.resolve(objects[string(n)])
	if err != nil {
		return err
	}
	s, ok := v.(*stream)
	if !ok {
		return fmt.Errorf("XObject /%s is not a stream", n)
	}
	if s.dict["Subtype"] == name("Image") {
		return nil
	}
	if s.dict["Subtype"] != name("Form") {
		return fmt.Errorf("unsupported XObject subtype %v", s.dict["Subtype"])
	}
	if r.forms[s] {
		return fmt.Errorf("recursive Form XObject")
	}
	r.forms[s] = true
	defer delete(r.forms, s)
	resources := r.resources
	if own := s.dict["Resources"]; own != nil {
		resources, err = r.d.dictionary(own)
		if err != nil {
			return err
		}
	}
	state := r.state
	if raw := s.dict["Matrix"]; raw != nil {
		resolved, err := r.d.resolve(raw)
		if err != nil {
			return err
		}
		a, ok := resolved.(array)
		if !ok || len(a) != 6 {
			return fmt.Errorf("invalid form Matrix")
		}
		var m matrix
		for i, v := range a {
			n, ok := number(v)
			if !ok {
				return fmt.Errorf("invalid form Matrix value")
			}
			m[i] = n
		}
		state.ctm = state.ctm.mul(m)
	}
	data, err := r.d.decode(s)
	if err != nil {
		return err
	}
	nested := &interpreter{d: r.d, resources: resources, fonts: map[name]*font{}, state: state, out: r.out, forms: r.forms, operations: r.operations}
	return nested.run(data, depth+1)
}
