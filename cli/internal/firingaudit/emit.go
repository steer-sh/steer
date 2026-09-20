package firingaudit

import (
	"encoding/json"
	"fmt"
)

// Emission reports what one rendering of a case cost and what it withheld, so
// a caller spends an allowance without measuring the output itself.
type Emission struct {
	// EmittedBytes is the exact size of this rendering, counting its framing
	// and this count itself.
	EmittedBytes int `json:"emitted_bytes"`
	// MaxBytes is the allowance the caller set, zero when it set none.
	MaxBytes int `json:"max_bytes,omitempty"`
	// Clipped names what this rendering withheld to fit MaxBytes. It is not a
	// step's Truncated flag, which is the continuation reader cutting one
	// step's text.
	Clipped []string `json:"clipped,omitempty"`
	// StepsEmitted and StepsAvailable separate what a reader saw from what the
	// continuation lookup reached, so a judgment states the extent it read.
	StepsEmitted   int `json:"steps_emitted"`
	StepsAvailable int `json:"steps_available"`
}

// Render writes one case at one clip level. Fit calls it repeatedly.
type Render func(Case, Emission) ([]byte, error)

// Fit renders a case within an allowance, withholding prior readings first,
// then continuation steps from the tail. It never withholds the row or the
// blocked action: an allowance that cannot carry those is refused. A zero
// allowance withholds nothing and still reports the size.
func Fit(c Case, max int, render Render) ([]byte, Emission, error) {
	available := len(c.Continuation.Steps)
	base := Emission{MaxBytes: max, StepsAvailable: available}

	type level struct {
		prior bool
		steps int
	}
	levels := []level{{prior: true, steps: available}}
	if len(c.Prior) > 0 {
		levels = append(levels, level{prior: false, steps: available})
	}
	for n := available - 1; n >= 0; n-- {
		levels = append(levels, level{prior: false, steps: n})
	}

	var smallest int
	for _, l := range levels {
		trial := c
		if !l.prior {
			trial.Prior = nil
		}
		trial.Continuation.Steps = c.Continuation.Steps[:l.steps]

		e := base
		e.StepsEmitted = l.steps
		if !l.prior && len(c.Prior) > 0 {
			e.Clipped = append(e.Clipped,
				fmt.Sprintf("%d prior reading(s) withheld to fit the allowance", len(c.Prior)))
		}
		if l.steps < available {
			e.Clipped = append(e.Clipped, fmt.Sprintf(
				"%d of %d continuation step(s) withheld to fit the allowance, from the end furthest from the block",
				available-l.steps, available))
		}
		size, out, err := settle(trial, e, render)
		if err != nil {
			return nil, Emission{}, err
		}
		if max <= 0 || size <= max {
			e.EmittedBytes = size
			return out, e, nil
		}
		smallest = size
	}
	return nil, Emission{}, fmt.Errorf(
		"the allowance of %d byte(s) does not carry this firing's row and blocked action, "+
			"which need %d; raise --max-bytes or leave the case unread", max, smallest)
}

// settle renders until the size the output claims is the size it has. The count
// is inside what it counts, so it is a fixed point rather than a measurement:
// each round feeds back the last length, and the claim only ever grows by a
// digit, so it settles.
func settle(c Case, e Emission, render Render) (int, []byte, error) {
	claim := 0
	for i := 0; i < 8; i++ {
		e.EmittedBytes = claim
		out, err := render(c, e)
		if err != nil {
			return 0, nil, err
		}
		if len(out) == claim {
			return claim, out, nil
		}
		claim = len(out)
	}
	return 0, nil, fmt.Errorf("the emitted byte count did not settle")
}

// emitted is the JSON shape: the case's own fields beside this rendering's
// account of itself.
type emitted struct {
	Case
	Emission Emission `json:"emission"`
}

// RenderJSON is the JSON rendering, valid at every clip level because a clip
// drops whole values rather than cutting inside one.
func RenderJSON(c Case, e Emission) ([]byte, error) {
	out, err := json.MarshalIndent(emitted{Case: c, Emission: e}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
