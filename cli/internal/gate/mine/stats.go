package mine

import (
	"encoding/json"
	"strconv"
	"strings"
)

// round4 formats to four decimals with ties-to-even, then parses back to the nearest
// double.
func round4(x float64) float64 {
	s := strconv.FormatFloat(x, 'f', 4, 64)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// pyFloat renders a float as Python's json module does: the shortest round-tripping
// form, with a trailing ".0" on integral values (0.0, not 0). Returned as json.Number
// so a standard encoder emits it verbatim.
func pyFloat(f float64) json.Number {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return json.Number(s)
}
