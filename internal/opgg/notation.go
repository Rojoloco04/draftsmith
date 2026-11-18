package opgg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Most OP.GG tools answer in a positional "class" notation instead of JSON:
//
//	class LolListChampions: data
//	class Data: champions
//	class Champion: champion_id,key,name
//
//	LolListChampions(Data([Champion(1,"Annie","Annie"),...]))
//	OP.GG page: https://op.gg/...
//
// The header gives each class's field names in order; the expression is
// built from constructor calls, lists, strings, numbers and booleans. Class
// names say nothing about where a value sits: with position "all" the tier
// list declares only Top and reuses Top(...) for every lane. Everything
// after the expression (the "OP.GG page:" line) is ignored.
//
// A few tools (lol_get_lane_matchup_guide) answer in plain JSON instead;
// Parse and Decode accept that too.

// Parse decodes a tool response into plain Go values: map[string]any for
// constructor calls (keyed by the header's field names), []any for lists,
// string, float64, bool, and nil.
func Parse(text string) (any, error) {
	body := strings.TrimSpace(text)
	if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
		var v any
		if err := json.NewDecoder(strings.NewReader(body)).Decode(&v); err != nil {
			return nil, fmt.Errorf("opgg: JSON response: %w", err)
		}
		return v, nil
	}
	classes, expr, err := parseHeader(body)
	if err != nil {
		return nil, err
	}
	p := &parser{src: expr, classes: classes}
	p.skipSpace()
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.src) {
		if c := p.src[p.pos]; c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			return nil, p.errorf("unexpected %q after the value", c)
		}
	}
	return v, nil
}

// FieldError reports requested fields OP.GG no longer has. The server skips
// them and says so in a "_field_diagnostics" value instead of failing, which
// would otherwise look like an empty result.
type FieldError struct {
	Fields []string
}

func (e *FieldError) Error() string {
	return "opgg: response is missing requested fields " + strings.Join(e.Fields, ", ") + " (the tool's schema changed?)"
}

// Decode parses a tool response and stores it in v the way encoding/json
// would, so typed structs only need json tags matching OP.GG's field names.
// It returns a *FieldError if the server skipped any requested field.
func Decode(text string, v any) error {
	tree, err := Parse(text)
	if err != nil {
		return err
	}
	if m, ok := tree.(map[string]any); ok {
		if diag, ok := m["_field_diagnostics"].(map[string]any); ok {
			fe := &FieldError{}
			list, _ := diag["unmatched_fields"].([]any)
			for _, f := range list {
				fe.Fields = append(fe.Fields, fmt.Sprint(f))
			}
			if len(fe.Fields) == 0 {
				fe.Fields = []string{"(unknown)"}
			}
			return fe
		}
	}
	b, err := json.Marshal(tree)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	return dec.Decode(v)
}

// parseHeader reads the leading "class Name: a,b,c" lines and returns the
// field lists by class name plus the text after the header.
func parseHeader(text string) (map[string][]string, string, error) {
	classes := map[string][]string{}
	rest := text
	for {
		rest = strings.TrimLeft(rest, " \t\r\n")
		after, ok := strings.CutPrefix(rest, "class ")
		if !ok {
			break
		}
		line, next, _ := strings.Cut(after, "\n")
		rest = next
		name, fields, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || !isIdent(name) {
			return nil, "", fmt.Errorf("opgg: bad class line %q", "class "+strings.TrimSpace(line))
		}
		var names []string
		if f := strings.TrimSpace(fields); f != "" {
			for _, n := range strings.Split(f, ",") {
				n = strings.TrimSpace(n)
				if n == "" {
					return nil, "", fmt.Errorf("opgg: empty field name in class %s", name)
				}
				names = append(names, n)
			}
		}
		if prev, dup := classes[name]; dup && strings.Join(prev, ",") != strings.Join(names, ",") {
			return nil, "", fmt.Errorf("opgg: class %s declared twice with different fields", name)
		}
		classes[name] = names
	}
	if len(classes) == 0 {
		return nil, "", fmt.Errorf("opgg: response has no class header: %.80q", text)
	}
	return classes, rest, nil
}

type parser struct {
	src     string
	pos     int
	classes map[string][]string
}

func (p *parser) errorf(format string, args ...any) error {
	start := max(p.pos-30, 0)
	end := min(p.pos+30, len(p.src))
	return fmt.Errorf("opgg: offset %d near %q: %s", p.pos, p.src[start:end], fmt.Sprintf(format, args...))
}

func (p *parser) skipSpace() {
	for p.pos < len(p.src) {
		switch p.src[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) value() (any, error) {
	if p.pos >= len(p.src) {
		return nil, p.errorf("unexpected end of input")
	}
	c := p.src[p.pos]
	switch {
	case c == '"' || c == '\'':
		return p.str()
	case c == '[':
		p.pos++
		return p.list()
	case c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9'):
		return p.number()
	case isIdentStart(c):
		start := p.pos
		for p.pos < len(p.src) && isIdentPart(p.src[p.pos]) {
			p.pos++
		}
		name := p.src[start:p.pos]
		p.skipSpace()
		if p.pos < len(p.src) && p.src[p.pos] == '(' {
			p.pos++
			return p.call(name)
		}
		switch name {
		case "true", "True":
			return true, nil
		case "false", "False":
			return false, nil
		case "null", "None", "nil":
			return nil, nil
		}
		p.pos = start
		return nil, p.errorf("unknown word %q", name)
	}
	return nil, p.errorf("unexpected %q", c)
}

// items reads comma separated values up to the closing byte, allowing a
// trailing comma. The opening byte has already been consumed.
func (p *parser) items(closing byte) ([]any, error) {
	var out []any
	for {
		p.skipSpace()
		if p.pos >= len(p.src) {
			return nil, p.errorf("missing %q", closing)
		}
		if p.src[p.pos] == closing {
			p.pos++
			return out, nil
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.skipSpace()
		if p.pos >= len(p.src) {
			return nil, p.errorf("missing %q", closing)
		}
		switch p.src[p.pos] {
		case ',':
			p.pos++
		case closing:
		default:
			return nil, p.errorf("expected ',' or %q", closing)
		}
	}
}

func (p *parser) list() (any, error) {
	vs, err := p.items(']')
	if err != nil {
		return nil, err
	}
	if vs == nil {
		vs = []any{}
	}
	return vs, nil
}

func (p *parser) call(name string) (any, error) {
	fields, ok := p.classes[name]
	if !ok {
		return nil, p.errorf("class %s is not declared in the header", name)
	}
	args, err := p.items(')')
	if err != nil {
		return nil, err
	}
	if len(args) != len(fields) {
		return nil, p.errorf("%s has %d fields (%s) but got %d values", name, len(fields), strings.Join(fields, ","), len(args))
	}
	m := make(map[string]any, len(fields))
	for i, f := range fields {
		m[f] = args[i]
	}
	return m, nil
}

func (p *parser) number() (any, error) {
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '+' || c == 'e' || c == 'E' {
			p.pos++
			continue
		}
		break
	}
	f, err := strconv.ParseFloat(p.src[start:p.pos], 64)
	if err != nil {
		p.pos = start
		return nil, p.errorf("bad number %q", p.src[start:p.pos])
	}
	return f, nil
}

func (p *parser) str() (any, error) {
	quote := p.src[p.pos]
	p.pos++
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == quote:
			p.pos++
			return b.String(), nil
		case c == '\\':
			if p.pos+1 >= len(p.src) {
				return nil, p.errorf("unterminated string")
			}
			e := p.src[p.pos+1]
			p.pos += 2
			switch e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'u':
				r, err := p.hexRune()
				if err != nil {
					return nil, err
				}
				b.WriteRune(r)
			default: // \" \' \\ \/ and anything else stand for themselves
				b.WriteByte(e)
			}
		default:
			_, size := utf8.DecodeRuneInString(p.src[p.pos:])
			b.WriteString(p.src[p.pos : p.pos+size])
			p.pos += size
		}
	}
	return nil, p.errorf("unterminated string")
}

// hexRune reads the XXXX of a \uXXXX escape, joining UTF-16 surrogate pairs.
func (p *parser) hexRune() (rune, error) {
	read := func() (rune, bool) {
		if p.pos+4 > len(p.src) {
			return 0, false
		}
		n, err := strconv.ParseUint(p.src[p.pos:p.pos+4], 16, 32)
		if err != nil {
			return 0, false
		}
		p.pos += 4
		return rune(n), true
	}
	r, ok := read()
	if !ok {
		return 0, p.errorf("bad \\u escape")
	}
	if r >= 0xD800 && r < 0xDC00 && strings.HasPrefix(p.src[p.pos:], `\u`) {
		save := p.pos
		p.pos += 2
		if lo, ok := read(); ok && lo >= 0xDC00 && lo < 0xE000 {
			return (r-0xD800)<<10 + (lo - 0xDC00) + 0x10000, nil
		}
		p.pos = save
	}
	return r, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

func isIdent(s string) bool {
	if s == "" || !isIdentStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isIdentPart(s[i]) {
			return false
		}
	}
	return true
}
