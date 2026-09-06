package tui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/steer-sh/steer-cli/internal/cadence"
	"github.com/steer-sh/steer-cli/internal/status"
)

func TestSurfaceBackgroundSurvivesNestedStatusSpans(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256, termenv.ANSI} {
		lipgloss.SetColorProfile(profile)
		for _, badge := range []string{ok("armed"), warn("broken"), errText("failed")} {
			row := selectedRow("selected name", " "+badge+dim(" timestamp"), true, 40)
			for _, rendered := range []string{row, inset(row, 44, false), paintSurface(surfaceStyle.Width(40), headingStyle.Render("enter")+secondary(" save"))} {
				assertBackgroundOnEveryCell(t, rendered)
			}
		}
	}
}

func assertBackgroundOnEveryCell(t *testing.T, rendered string) {
	t.Helper()
	sgr := regexp.MustCompile("\x1b\\[[0-9;]*m")
	bg := false
	pos := 0
	for _, loc := range sgr.FindAllStringIndex(rendered, -1) {
		if strings.Trim(rendered[pos:loc[0]], "\n") != "" && !bg {
			t.Fatalf("unpainted cells: %q", rendered[pos:loc[0]])
		}
		codes := strings.Split(rendered[loc[0]+2:loc[1]-1], ";")
		for i := 0; i < len(codes); i++ {
			code, _ := strconv.Atoi(codes[i])
			switch {
			case code == 0 || code == 49:
				bg = false
			case code == 48 || code == 38:
				if code == 48 {
					bg = true
				}
				if i+1 < len(codes) && codes[i+1] == "2" {
					i += 4
				} else {
					i += 2
				}
			case code >= 40 && code <= 47 || code >= 100 && code <= 107:
				bg = true
			}
		}
		pos = loc[1]
	}
	if strings.Trim(rendered[pos:], "\n") != "" && !bg {
		t.Fatal("unpainted trailing cells")
	}
}

func TestEvidenceSeparatesCountsFromCompleteCoverage(t *testing.T) {
	it := status.ScoredIntervention{Verdict: "confounded", Unit: "chain", Before: cadence.Incidence{Hits: 20, Units: 162}, After: cadence.Incidence{Hits: 2, Units: 28}, BeforeCoverage: cadence.Coverage{Eligible: 162, Total: 199, UnknownEvidence: 37}}
	text := ansi.Strip(evidenceDetail(it, 100))
	for _, want := range []string{"Before: 20 recurrences in 162 review sequences", "After: 2 recurrences in 28 review sequences", "Coverage: 162 eligible / 199 (37 with no decision recorded)", "They do not compare them"} {
		if !strings.Contains(strings.Join(strings.Fields(text), " "), want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "recurrences") && strings.Contains(line, "Coverage:") {
			t.Fatal("coverage shares the counts line")
		}
	}
}

func TestReadOutcomesSelectionHasOneLine(t *testing.T) {
	old := lipgloss.ColorProfile()
	defer lipgloss.SetColorProfile(old)
	lipgloss.SetColorProfile(termenv.TrueColor)
	m := geometryModel(t)
	m.active = panelChannels
	m.width = 160
	m.snap.Posture.Unresolved = 3
	m.cursor[panelChannels] = len(m.channelRows()) - 1
	for _, reading := range []bool{false, true} {
		m.reading = reading
		lines := strings.Split(m.viewChannels(), "\n")
		selected := markerLine(lines)
		if selected < 0 {
			t.Fatal("missing action selection")
		}
		if selected+1 < len(lines) && strings.Contains(lines[selected+1], "\x1b[") {
			t.Fatalf("extra styled line after action: %q", lines[selected+1])
		}
	}
}
