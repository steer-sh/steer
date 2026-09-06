# TUI styling proposal

Implemented in the TUI and refined from live-terminal review. Based on Jeremy's six screenshots,
the current renderers, and the versioned upstream documentation linked below.

The design premise is that Steer should make configuration and evidence easy to
read, with visual emphasis explaining what is selected and what needs attention.
The strongest argument against another pass is that the current information
hierarchy already works: decoration could consume space, exaggerate warnings,
and make a practical interface harder to scan. The screenshots support a
restrained refinement: keep the five panels, stacked summaries, inline detail,
and existing information order. Give them one visual grammar.

Recommended direction: a quiet, open terminal interface with neutral typography,
purple focus, short semantic status labels, selective background surfaces, and
deliberate spacing. Alignment and restrained depth should make navigation,
selection, and expanded content feel cohesive.

## What the toolkit actually supports

The repository pins Bubble Tea **v1.3.10**, Lip Gloss **v1.1.0**, Bubbles
**v1.0.0**, and `x/ansi` **v0.11.6** in [cli/go.mod](cli/go.mod).

Bubble Tea supplies the event/update/render loop; Lip Gloss styles the rendered
strings. The existing dependencies are sufficient for this proposal.
[Bubble Tea v1.3.10](https://github.com/charmbracelet/bubbletea/tree/v1.3.10)

| Capability | Available in this Lip Gloss version | Recommended use |
| --- | --- | --- |
| Foreground/background colours | True colour, 256-colour, ANSI palettes, automatic degradation | Adaptive palette and selective fills over the terminal background |
| Light/dark variants | `AdaptiveColor`, `CompleteAdaptiveColor` | Explicit light and dark variants for semantic colours |
| Text emphasis | Bold, italic, underline, faint, reverse, strike, blink | Bold for hierarchy; leave the other effects unused in normal views |
| Layout | Cell-based width/height, padding, margins, alignment, joins | Shared gutter, consistent spacing, bounded prose |
| Borders | Individual sides and multiple border styles | One subtle left edge for expanded detail and editors |
| Measurement | Rendered cell width and height | Budget the complete styled frame |

These are documented by the [Lip Gloss v1.1.0 API](https://pkg.go.dev/github.com/charmbracelet/lipgloss@v1.1.0).
Terminal support determines the final appearance of text attributes. Do not make
an essential distinction depend solely on colour or an emphasis attribute.

**Fonts:** this stack has no portable font-family or font-size styling API.
The terminal profile chooses the typeface, size, and how bold/italic faces are
rendered. Typography here means weight, contrast, whitespace, and alignment.
There are terminal-specific extensions: kitty's text-sizing protocol supports
multiple sizes, but adopting it would introduce capability detection and cell
layout concerns outside this polish pass.
[Kitty text-sizing protocol](https://sw.kovidgoyal.net/kitty/text-sizing-protocol/)

Do not change Jeremy's terminal preferences or depend on special fonts, emoji,
or Nerd Font glyphs. Keep a plain-text fallback for structural symbols. Current
upstream Lip Gloss examples also include v2 APIs; implement against the pinned
v1 documentation rather than treating a dependency migration as design work.

## What the screenshots and code show

| Observation | Repository evidence | Design implication |
| --- | --- | --- |
| There is already a purple/green/amber theme | [theme.go](cli/internal/styles/theme.go) defines brand and semantic colours | Consolidate their roles rather than introduce a new visual identity |
| Panel titles are bright white, bold, and underlined; most other headings and field labels are bold too | `SectionHeader` in `theme.go`; `title`, `section`, and `field` in [render.go](cli/internal/tui/render.go) | Too many levels share emphasis; reduce competing weight |
| Problems highlights its selected title; the other selectable panels mainly highlight a small chevron | `problemSummary` in [problems.go](cli/internal/tui/problems.go), `viewChannels`, `viewGuards`, `viewReviewer` | Apply the same selection treatment everywhere |
| Arming timestamps look unexpectedly heavy | `armingBadge` wraps both `armed` and the muted suffix inside the bold success style in [channels.go](cli/internal/tui/channels.go) | Render status and timestamp as sibling spans |
| Essential explanations can look like low-priority metadata | Guard descriptions and reviewer help use `dim` in [guards.go](cli/internal/tui/guards.go) and [reviewer.go](cli/internal/tui/reviewer.go) | Separate readable supporting text from incidental metadata |
| Expanded Problems has an entire amber sentence amid dense nested prose | `limitsDetail` calls `warn(limit)` in `problems.go` | Keep caution on the short result label; explain the cause in normal text |
| Screens have different initial gaps and the footer follows their content | `section` adds a leading blank; `chrome` appends the legend in [dashboard.go](cli/internal/tui/dashboard.go) | Give spacing and footer placement a shared owner |
| Wide screenshots produce extremely long prose lines | `bodyWidth` returns the entire terminal width | Bound prose width while retaining compact tables |

The lavender cast of ordinary text and the background in the screenshots are
not established as application colours: many strings use the terminal's default
foreground, and `chrome` paints no background. Exact screenshot contrast cannot
be inferred from the hardcoded palette alone.

Prior decisions read: `674fde4`, `5bbabef`, `f5434ec`, and `bb9cecb`, plus
[TUI_pass.md](TUI_pass.md). Preserve the kind → harness → scope ordering, the
five-panel structure, and one inline expansion. `674fde4` explicitly rejected
colouring whole comparison-limit paragraphs; the current `limitsDetail` remains
an exception to that intended rule.

## The visual grammar

### Colour roles

Keep the user's background and default foreground for the open canvas. Use one
small adaptive palette, with explicit foreground/background pairs on filled
surfaces. The values below are proposed starting values for visual review, not
measured contrast guarantees across terminal themes.

| Role | Dark terminal | Light terminal | Meaning |
| --- | --- | --- | --- |
| Primary text | Terminal foreground | Terminal foreground | Titles, values, explanations, evidence |
| Secondary text | `#B0B6C4` | `#535C6C` | Labels, subtitles, inactive tabs, useful help |
| Metadata | `#969EAE` | `#626C7C` | Timestamps, storage paths, incidental provenance |
| Focus | `#A78BFA` | `#6941C6` | Active tab, selected item, active input |
| Positive status | `#6FCF97` | `#237A49` | Short status words with an established positive meaning |
| Caution status | `#E8B86D` | `#8A5700` | Short withheld-result or attention labels |
| Adverse/error status | `#F08A8A` | `#B4233D` | Short adverse-result labels and failed operations |
| Structural edge | `#555E70` | `#BDC4CE` | Optional detail/editor edge; carries no essential meaning |
| Active tab fill / text | `#A78BFA` / `#211A32` | `#6941C6` / `#FFFFFF` | Compact, strongest focus surface |
| Selected title fill / text | `#393249` / `#C4B5FD` | `#EDE7F6` / `#6941C6` | Quiet highlight behind the selected title row |
| Detail and footer fill / primary text | `#2E3340` / `#E4E7EE` | `#EDF0F5` / `#242B36` | Subdued surfaces for expanded content and controls |

Purple always means focus. Green, amber, and red describe a state. A selected
broken registration must retain its warning label while its name gains focus.
An unselected row can still carry an important status. Do not recolour an entire
selected row and erase that distinction.

Preserve the current verdict-to-colour mapping during this pass. Review any
proposed semantic remapping separately; a new colour must not imply a stronger
measurement claim. Stored history counts remain neutral. Routine feedback uses
neutral text; a failure gets an error label. Cyan adds no necessary role here.

On low-colour terminals, allow palette degradation initially and inspect the
result; explicit fallback colours are available if needed. In monochrome,
brackets, chevrons, labels, and spacing must still communicate the same state.
Use explicit secondary colours rather than combining grey with `Faint(true)`.

### Typography and emphasis

| Element | Treatment |
| --- | --- |
| Active tab | Purple background, contrasting bold text, bracketed; equal padding to inactive tabs |
| Inactive tab | Secondary foreground, regular weight |
| Panel title | Primary foreground, bold; remove underline |
| Panel description after the dash | Secondary foreground, regular weight; wrap below when necessary |
| Section heading | Primary foreground, bold, with space before it |
| Unselected item title | Primary foreground, regular weight |
| Selected item title | Purple, bold, on a subtle title-row fill, with the existing `›` marker |
| Field label | Secondary foreground, regular weight, aligned column |
| Field value and explanatory prose | Primary foreground, regular weight |
| Result or arming state | Semantic colour on the short label only, bold |
| Detail subheading | Primary foreground, bold; indentation establishes its level |
| Keys in footer | Surface primary foreground; explanations and separators secondary, on a subdued strip |
| Paths, commands, model identifiers | Upright text; no italic or extra decorative quoting |

Retain brackets on the active tab: they provide an unambiguous monochrome cue
and already occupy the same width as the inactive tabs' spaces. Fill only the
active tab, leaving its neighbours on the open canvas. Reserve reverse video as
a possible fallback for input focus,
not the normal selection style. Italics and underlines are available but have no
required role in this design; using every available attribute would weaken it.

### Borders and background surfaces

Use selective surfaces as part of the proposed design, with these four roles:

- **Active tab:** a compact purple fill with contrasting text and one cell of
  horizontal breathing room. Keep its dimensions stable when selection moves.
- **Selected item:** a subtle fill across the title row within the content
  column. For a wrapped title, fill its continuation lines too. In compact
  tables, preserve each status span's semantic colour against that background;
  the fill must not turn all row text purple or imply that the item is healthy.
- **Expanded details and editors:** a quiet background, a subtle left border,
  and inner padding. Give the active editor's edge the focus colour. Keep
  ordinary lists and collapsed summaries unboxed so the expanded boundary is
  meaningful. Full rounded boxes remain an option for visual comparison, but
  the preferred starting treatment is the lighter single edge.
- **Footer:** a subdued background strip across the shared frame width, with
  keys in higher contrast than their descriptions. Fill wrapped footer lines
  to the same width. Keep feedback immediately above this strip.

Paint each surface's padding and trailing cells consistently, including blank
lines within expanded content. Explicit text colours on those surfaces prevent
an arbitrary terminal foreground from disappearing against a chosen fill.
Check secondary text and every status colour against the fills as well. In
limited-colour or monochrome rendering, retain brackets, the selection marker,
and detail boundaries even if subtle background distinctions disappear.

### Spacing and frame

- Add a shared two-cell left and right gutter at ordinary widths. Titles, tabs,
  section headings, and footer align to that gutter. The selection marker sits
  there too; selectable names start two cells farther in. Reduce the gutter to
  one cell on small terminals.
- Keep one empty line between scope and title, one between title and first
  content, one between summary entries, and one before subsequent sections.
  Remove the extra first-section blank contributed by `section()`.
- Target a maximum body width of **120 cells**, left aligned. Compact tables
  can use this width; long prose and detail should target about **104 cells**
  including their indentation. These are starting measurements to review at
  Jeremy's actual font size. Keep unused space at the right instead of centring
  a moving content column.
- Give expanded details a quiet background, subtle left border, and one cell of
  inner padding. Use the same inset treatment for an open editor. Keep closed
  rows unboxed, with a fill only on the selected title row. Budget the border
  before wrapping; retain the existing inline
  expansion and scrolling model.
- Anchor the key legend near the bottom of the reported terminal height, with
  one preceding blank line. Feedback belongs immediately above it. When height
  is unknown, let the frame flow naturally.
- Wrap footer hints as complete key/action groups, and compute their actual
  height. At narrow widths use two lines before sacrificing body readability.
  A fixed overhead of six lines will no longer be sufficient.

An expanded detail's structure would read like this; the colours and weights
follow the table above, and this excerpt omits later fields:

```text
  Arming   [Problems]   Guards   Reviewer   Diagnostics
  Reading evidence from every project on this machine

  Problems — what recurs, and whether our fixes help

  Results with no repair available — read the counts and judge these yourself
  › inline python reads a path missing from cwd
      Fix       pre-tool guard — ...
      Result    Not comparable
      Why       ...

      │ What goes wrong
      │   An inline python script reads a relative path ...
      │ Evidence
      │   before   10 recurrences in 442 agent sessions
      │   after    4 recurrences in 76 agent sessions
      │   ...
```

## Panel-specific refinements

### Arming — screenshot 1

Use the common selected-name treatment, preserving the harness/scope columns
and each row's own status. Split `ok("armed")` from the separately rendered
metadata suffix so the timestamp does not inherit bold. Add one blank line
between the commit-gate block and denials recorder; keep paired project/global
rows together and preserve their existing order.

The header currently says **Configure: this project**, but `channelRows` includes
both scopes and `toggleChannel` writes `row.scope`. Change this panel's scope
line to **Configure: project and global registrations on this machine**. This
is a correctness clarification, not an invitation to alter scope behavior.

Make the CLI-only installation note a short label followed by the two complete,
wrapped commands. Do not leave the commands in one long muted sentence.

### Problems — screenshots 2 and 5

Keep the collapsed Fix / Result / Why / Next layout and its information order.
The immediate gains are regular-weight field labels, neutral unselected titles,
the shared prose width, and the inset detail edge. Give small gaps to detail
sections when height permits; avoid a blank line after every label.

Colour **Not comparable**, then render the explanation in normal text. The
comparison-limits block should foreground the concrete cause already available
in `ComparisonCauses`, without repeatedly announcing the generic limitation.
Any prose shortening must preserve every cause, the distinction between hard
limits and work-mix qualifications, and the caveat on descriptive counts.

The Fix summary and “Fix in full” repeat text because `fixDetail` starts with
`fixSummary`. Treat reducing that duplication as optional copy work after the
visual pass: retain installation provenance and the statement that a recorded
fix does not prove current installation. Do not hide important caveats merely
to make the expanded block shorter.

### Guards — screenshot 3

Promote the empty-inventory sentence and useful guard descriptions to readable
secondary text. Keep the storage path in metadata. Existing local-scope and
machine-local qualifications remain present.

The name column truncates to 30 runes even on this wide screen. Allocate it
from available cell width, with a reasonable cap, and show the complete selected
name when it still does not fit. At narrow widths put its description beneath
the name. Enter toggles a guard, so do not rely on opening a detail view to learn
the identity of the thing about to be enabled. Preserve installed-first ordering
and the single appearance of an enabled built-in guard.

### Reviewer — screenshot 4

Apply the shared selected-label treatment. Keep values primary and help
secondary; the setting's value should be easier to find than its label. Render
the selected help at a stable indent beneath the row. Use the same inset edge
for its choice list or text input when editing.

Keep the settings-precedence explanation visible. Overrides need a short amber
lead-in and normal explanatory text, rather than whole warning paragraphs.
Storage path and configuration keys can remain as quiet metadata; moving them
behind a new toggle adds interaction without much gain on this sparse panel.

### Diagnostics — screenshot 6

Keep the current order: issues, history, team configuration, optional technical
details. Use amber on issue headlines, normal text on consequences and repair
instructions, and neutral history counts. Align counts in a compact numeric
column with nearby labels rather than giving them dashboard-card treatment.

Long paths and instrument strings must remain readable and reachable. The
empty/healthy wording should retain its current claim boundaries: these checks
found no issue, and these counts describe records on this machine.

## Layout and interaction defects to include with implementation

These are supported by renderer/control-flow inspection; they have not been
reproduced in a live terminal during this document-only pass.

- **Diagnostics overflow is unreachable.** `rowCount` returns zero for this
  panel, `diagnosticsKey` handles only `t`, and `windowBody` crops around a
  selection marker that Diagnostics never renders. In a short terminal the
  lower content can be hidden without a working scroll action. Add a simple
  shared-body scroll offset for a read-only panel; show scroll keys only where
  they work. Do not introduce a second nested viewport for Problems.
- **Several rows bypass wrapping.** Arming rows and its CLI note, guard rows,
  reviewer value rows, and problem titles directly concatenate strings.
  `wrapTo` covers much prose but does not guarantee the whole frame fits.
  Measure rendered cell widths and wrap long values with hanging indents.
- **The narrow-width floor permits overflow.** `wrapTo` enforces a minimum of
  24 cells after indentation. Adapt indentation and column layouts before
  imposing that floor; for an impractically small terminal, show a concise
  resize message rather than a partially actionable screen.
- **Footer hints overgeneralize.** Diagnostics advertises movement despite
  selecting no rows; Problems advertises stop-tracking on queue entries; Arming
  can say “turn on/off or read outcomes” regardless of the selected row. Derive
  the primary hint from the current selection and mode. Preserve all bindings.

## Implementation boundaries and acceptance

Build the new semantic styles inside the TUI, reusing palette concepts where
appropriate. `cli/internal/styles/theme.go` also styles other CLI output;
changing its global `SectionHeader` or `Bold` would extend this task's reach.
Route TUI headings, fields, selection, feedback, and status spans through named
helpers rather than adding individual style expressions to every panel.

Update frame geometry in `render.go` and `dashboard.go` together. In particular,
`markerLine` currently searches for the rendered `pointer(true)` string. A new
style must not break selection tracking or let a decorative marker identify the
wrong line. Explicit selection position is preferable if composition makes the
string search unreliable. Preserve the existing terminal-height behavior until
the complete replacement geometry is verified.

Implement in this order: semantic typography, spans, and paired surface colours;
shared gutter and frame;
panel adjustments and reachable overflow; then optional copy trimming. Rebuild
the shipped binaries before committing CLI changes, as the repository requires.

Acceptance should combine mechanical checks with Jeremy's visual review:

- Render representative fixtures at 80×24, 120×40, and a wide 180×50, plus a
  small-window case. Check actual cell widths and frame heights, visible
  selection, accessible last content, and a reachable footer after resize.
- Cover selected rows carrying success/caution/error, long guard names and
  model identifiers, expanded Problems, reviewer editing/overrides, empty
  inventories, and Diagnostics with technical details. Confirm styling does
  not change Enter's target or action.
- Inspect dark and light terminals, a limited-colour profile, and monochrome.
  Verify primary text remains primary, muted text stays legible, and both tab
  and row selection are understandable without hue.
- Inspect active-tab, selected-row, detail/editor, and footer fills together.
  Check text and status contrast on each surface, continuous padding and wrapped
  backgrounds, and clean redraws when selection moves or details close. The
  active tab should be the strongest fill; content surfaces should stay quiet.
- Check that no timestamp inherits status bold, no full comparison paragraph
  is amber, field labels do not outweigh values, and footer keys are easy to
  distinguish from their descriptions.
- Run `./check.sh`; add behavioral geometry tests for the overflow fixes rather
  than tests that merely repeat the palette constants. The existing window
  tests are a base, not proof of all-panel width or scroll correctness.
- Take the same six screenshots after implementation. Jeremy judges the
  proposed spacing, line length, and palette in the actual terminal. Automated
  checks establish readable geometry and working navigation; they do not
  establish that the design is beautiful.

## Implemented refinements from terminal review

Arming and Guards cap at 160 cells; other panels cap at 120, and expanded
Problems at 104. Guard name columns follow the inventory's cell widths, with
hanging description continuations. Detail headings start at the inner edge.
Evidence uses colon labels, primary counts and separate secondary Coverage
lines, preserving the shared formatter and every qualification. Surface colours
are restored after nested SGR resets, including beneath styled text and padding.

Run `./check.sh` for formatting, build, vet and tests. From `cli/`, run
`STEER_TUI_PREVIEW_DIR=<directory> go test ./internal/tui -run TestStylingPreviews`
to export ANSI fixtures, and `VERSION=0.1.0 ./build.sh` to rebuild shipped binaries.
