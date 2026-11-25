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
	// ContextEmitted and ContextAvailable do the same for the steps a check
	// case shows before its block.
	ContextEmitted   int `json:"context_emitted,omitempty"`
	ContextAvailable int `json:"context_available,omitempty"`
}

// Render writes one case at one clip level. Fit calls it repeatedly.
type Render func(Case, Emission) ([]byte, error)

// Fit renders a case within an allowance, withholding prior readings first,
// then continuation steps from the tail. It never withholds the row or the
// blocked action: an allowance that cannot carry those is refused. A zero
// allowance withholds nothing and still reports the size.
func Fit(c Case, max int, render Render) ([]byte, Emission, error) {
	trim := func(l clipLevel) Case {
		trial := c
		if !l.prior {
			trial.Prior = nil
		}
		trial.Continuation.Steps = c.Continuation.Steps[:l.steps]
		return trial
	}
	return fitLevels(len(c.Prior), len(c.Continuation.Steps), 0, max, trim, render,
		"this firing's row and blocked action")
}

// clipLevel is one rendering's extent: whether prior readings stay, and how many
// continuation and context steps it keeps.
type clipLevel struct {
	prior          bool
	steps, context int
}

// fitLevels renders trim's case at each clip level, least withheld first, and
// returns the first rendering within max with its Emission. prior, steps and
// context are the counts available on each axis; kept names what no level
// withholds, for the error when even the last level exceeds max. A zero max
// returns the first level.
func fitLevels[C any](prior, steps, context, max int, trim func(clipLevel) C,
	render func(C, Emission) ([]byte, error), kept string) ([]byte, Emission, error) {
	base := Emission{MaxBytes: max, StepsAvailable: steps, ContextAvailable: context}

	levels := []clipLevel{{prior: true, steps: steps, context: context}}
	if prior > 0 {
		levels = append(levels, clipLevel{steps: steps, context: context})
	}
	for n := steps - 1; n >= 0; n-- {
		levels = append(levels, clipLevel{steps: n, context: context})
	}
	for n := context - 1; n >= 0; n-- {
		levels = append(levels, clipLevel{context: n})
	}

	var smallest int
	for _, l := range levels {
		e := base
		e.StepsEmitted, e.ContextEmitted = l.steps, l.context
		if !l.prior && prior > 0 {
			e.Clipped = append(e.Clipped,
				fmt.Sprintf("%d prior reading(s) withheld to fit the allowance", prior))
		}
		if l.steps < steps {
			e.Clipped = append(e.Clipped, fmt.Sprintf(
				"%d of %d continuation step(s) withheld to fit the allowance, from the end furthest from the block",
				steps-l.steps, steps))
		}
		if l.context < context {
			e.Clipped = append(e.Clipped, fmt.Sprintf(
				"%d of %d context step(s) withheld to fit the allowance, from the end furthest from the block",
				context-l.context, context))
		}
		size, out, err := settle(trim(l), e, render)
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
		"the allowance of %d byte(s) does not carry %s, "+
			"which need %d; raise --max-bytes or leave the case unread", max, kept, smallest)
}

// settle renders until the size the output claims is the size it has. The count
// is inside what it counts, so it is a fixed point rather than a measurement:
// each round feeds back the last length, and the claim only ever grows by a
// digit, so it settles.
func settle[C any](c C, e Emission, render func(C, Emission) ([]byte, error)) (int, []byte, error) {
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
