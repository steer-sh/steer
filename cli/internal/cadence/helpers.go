package cadence

import "encoding/json"

// str reads a string field, or "" when absent or not a string.
func str(v any) string {
	s, _ := v.(string)
	return s
}

// strOr reads a string field, falling back to def when absent or empty.
func strOr(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}

// numFloat reads a JSON number (json.Number or float64) as a float.
func numFloat(v any) float64 {
	switch n := v.(type) {
	case json.Number:
		f, _ := n.Float64()
		return f
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

func numInt(v any) int { return int(numFloat(v)) }
