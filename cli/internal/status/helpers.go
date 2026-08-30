package status

import (
	"bytes"
	"encoding/json"
	"os"

	"github.com/steer-sh/steer-cli/internal/cadence"
)

// str reads a string value, or "" when the key is absent or not a string.
func str(v any) string {
	s, _ := v.(string)
	return s
}

// numFloat reads a numeric value decoded from JSON, tolerating json.Number.
func numFloat(v any) float64 {
	switch n := v.(type) {
	case json.Number:
		f, _ := n.Float64()
		return f
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func numInt(v any) int { return int(numFloat(v)) }

// incidenceOf reads a before/after block from a ScoreRecord result map into the
// contract's Incidence. The score map's own keys are hits, chains, rate; the
// contract keys the denominator "units", so chains maps onto Units here.
func incidenceOf(v any) cadence.Incidence {
	m, _ := v.(map[string]any)
	if m == nil {
		return cadence.Incidence{}
	}
	return cadence.Incidence{
		Hits:  numInt(m["hits"]),
		Units: numInt(m["chains"]),
		Rate:  numFloat(m["rate"]),
	}
}

// countLines counts non-empty lines in a file. A missing file reads as zero.
func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			count++
		}
	}
	return count
}
