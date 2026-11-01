package mine

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

// readFileIfExists returns a file's bytes, or (nil, nil) when it does not exist.
func readFileIfExists(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

// pyDumps renders a value the way Python's json.dumps(sort_keys=True) does: sorted
// keys, ensure_ascii escaping, no HTML escaping, and Python's float rendering. With
// indent 0 it uses the compact ", "/": " separators; with indent > 0 it pretty-prints.
// It is the byte-identity writer for interventions.jsonl and the register/reject/retire
// stdout dumps.
func pyDumps(v any, indent int) string {
	var b strings.Builder
	emitPyJSON(&b, v, indent, 0)
	return b.String()
}

func emitPyJSON(b *strings.Builder, v any, indent, level int) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		b.WriteString(pyJSONString(t))
	case json.Number:
		b.WriteString(t.String())
	case int:
		fmt.Fprintf(b, "%d", t)
	case int64:
		fmt.Fprintf(b, "%d", t)
	case float64:
		b.WriteString(string(pyFloat(t)))
	case map[string]any:
		emitMap(b, t, indent, level)
	case []any:
		emitSlice(b, t, indent, level)
	case []string:
		conv := make([]any, len(t))
		for i, s := range t {
			conv[i] = s
		}
		emitSlice(b, conv, indent, level)
	default:
		// Fall back to the standard encoder for any type not modeled above.
		raw, _ := json.Marshal(t)
		b.Write(raw)
	}
}

func emitMap(b *strings.Builder, m map[string]any, indent, level int) {
	if len(m) == 0 {
		b.WriteString("{}")
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			writeItemSep(b, indent)
		}
		writeNewlineIndent(b, indent, level+1)
		b.WriteString(pyJSONString(k))
		b.WriteString(": ")
		emitPyJSON(b, m[k], indent, level+1)
	}
	writeNewlineIndent(b, indent, level)
	b.WriteByte('}')
}

func emitSlice(b *strings.Builder, s []any, indent, level int) {
	if len(s) == 0 {
		b.WriteString("[]")
		return
	}
	b.WriteByte('[')
	for i, v := range s {
		if i > 0 {
			writeItemSep(b, indent)
		}
		writeNewlineIndent(b, indent, level+1)
		emitPyJSON(b, v, indent, level+1)
	}
	writeNewlineIndent(b, indent, level)
	b.WriteByte(']')
}

func writeItemSep(b *strings.Builder, indent int) {
	if indent > 0 {
		b.WriteByte(',')
	} else {
		b.WriteString(", ")
	}
}

func writeNewlineIndent(b *strings.Builder, indent, level int) {
	if indent > 0 {
		b.WriteByte('\n')
		b.WriteString(strings.Repeat(" ", indent*level))
	}
}

// pyJSONString escapes a string as Python's json module does with ensure_ascii: the
// shorthand escapes, \uXXXX for control and non-ASCII characters (surrogate pairs above
// U+FFFF), and no escaping of / < > &.
func pyJSONString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 || r > 0x7e {
				if r > 0xffff {
					r2 := r - 0x10000
					fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(r2>>10), 0xdc00+(r2&0x3ff))
				} else {
					fmt.Fprintf(&b, `\u%04x`, r)
				}
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// pyRepr reproduces Python's repr of a string: single quotes unless the string holds a
// single quote and no double quote, then double. Go's %q always uses double quotes.
func pyRepr(s string) string {
	quote := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote):
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7f:
			b.WriteRune(r)
		case unicode.IsPrint(r):
			b.WriteRune(r)
		case r <= 0xff:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r <= 0xffff:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteByte(quote)
	return b.String()
}
