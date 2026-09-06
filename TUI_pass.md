# TUI pass

Working action list for the continuing UX/UI discussion. Items below capture
agreed work; implementation is pending. Terminology follows [terms-final.md](terms-final.md).

## Combine candidates and tracked problems

- [x] Remove the separate **Candidates** tab and move its queue into the current
  **Classes** panel.
- [x] Rename the **Classes** tab to **Problems**, matching its existing body
  heading and the terminology already agreed in `terms-final.md`.
- [x] Add a compact **Problems to consider** section above the scored inventory.
  Preserve the queue's patterns, counts, source distinctions, and `/steer-report`
  handoff. Review findings awaiting clustering must remain distinguishable from
  an identified recurring problem; neither implies a suggested fix exists.
- [x] Keep scored fixes and refusals below that section, retaining their results,
  explanations, measurement limits, and grouping by required action.
- [x] Replace the separate candidate cursor with one navigable Problems list.
  Enter expands either kind of item as specified below. Keep stop-tracking
  actions correctly targeted to eligible inventory rows only.
- [x] Keep proposal authoring and decisions in `/steer-report`. Do not restore
  the removed register/reject wizard or add interactions merely to justify a tab.

The combined panel should answer: what problems are showing up, and are our
responses helping? Pending work needs visibility, but the current queue's counts
and shared handoff do not require a separate destination. Substantial evidence
browsing or comparison could justify reconsidering a separate view later.

## Redesign the Problems reading experience

The overview must answer four questions in order: what is the problem, what
response was chosen, what result is known, and what needs attention? The current
renderer puts full prose, regex, counts, and diagnostics together, dims the fix,
and highlights every comparison warning. Its vertical window does not wrap text
to the terminal width. Renaming that output alone cannot make it readable.

### Compact overview

- [x] Render a vertical list of compact problem summaries with a blank line
  between entries. Use stacked labeled lines rather than a wide table:
  **problem name**, **Fix**, **Result**, and a short reason or next step where
  needed. Keep the title legible instead of truncating it at 22 characters.
- [x] Give the fix description normal foreground contrast and its label clear
  emphasis. Show what it does and its type; move the absolute target path into
  details. The fix is primary information, not grey metadata beside the result.
- [x] Distinguish a recorded fix, a declined suggestion, and a verified current
  installation. `ScoredIntervention` carries a tracking record and target, not
  a universal live-installation proof. Use **Guard installed** only when supported
  by current setup evidence; otherwise describe the recorded fix without
  promising enforcement. A declined suggestion establishes only that this fix
  was declined, not that no other fix exists.
- [x] Give the result its own labeled line using the shared result vocabulary.
  Keep a short explanation of a blocked comparison visible, such as **Session
  parsing changed between the periods**. Do not hide the reason a result is
  unavailable entirely behind Enter.
- [x] Show an actual next step only when supported. Omit generic instructions
  like **read the counts and decide** when they name no concrete decision.
- [x] Move raw regex, p-values, paths, full before/after measurements, work-mix
  diagnostics, eligibility rules, and provenance out of collapsed summaries.
  Keep the shared result label visible so hiding diagnostics never invents a
  benefit or hides a withheld comparison.
- [x] Use bold or clear foreground contrast for titles and labels, a visible
  selection marker/background, and readable body text. Reserve muted text for
  supporting metadata and key hints. Use warning color for the result requiring
  caution, not for entire paragraphs of work-mix detail. Color supplements words.
- [x] Keep Problems to consider above the tracked inventory, with short readable
  action-group headings below. Replace **Results no fix settles** and similar
  abstract headings with wording that describes the user's task or result state.
  Preserve scoring and action mappings; this is presentation work.

### Enter expands one problem

- [x] Enter opens an inline detail area immediately beneath the selected summary;
  Enter again or Escape closes it. Only one item is expanded at a time. Moving
  selection closes the prior item, keeping the overview compact.
- [x] Present expanded content in this order: full problem explanation; chosen
  fix or declined proposal and its reason; result explanation; evidence; relevant
  next action. Keep the summary visible as the detail area's context.
- [x] Give evidence its own labeled block. Show before and after on separate
  lines with dates when available, occurrence counts, denominators, and the
  correct noun (agent sessions or review sequences). An incomparable result's
  counts remain descriptive and must not imply that the fix helped.
- [x] Explain comparison limits in everyday language. Separate changes that
  prevent comparison from changes in work mix that merely qualify a permitted
  result. Combine repetitive causes into a readable sentence; retain all causes
  in details without repeating the same warning prefix on every line.
- [x] Put exact regex, target paths, statistical details, and provenance behind
  a further **Technical details** toggle inside the expanded area. These support
  inspection and repair and must remain accessible without dominating reading.
- [x] Keep **Stop tracking** in the expanded tracked item's action area, with
  the existing reason editor and a clear explanation that the change stays in
  place. Pending findings do not offer this action. Enter itself never mutates.

### New findings use the same interaction

- [x] Enter on a problem to consider expands the evidence summary available from
  the queue, its source and counts, and the instruction to run `/steer-report`
  in the coding agent. A findings batch awaiting clustering is labeled as such;
  do not invent a problem title or claim that a proposed fix already exists.
- [x] Keep this a clear handoff. Enter does not install, reject, register a fix,
  or silently launch an agent. No row-level skill invocation is promised without
  an implemented targeting contract. Proposal decisions remain in the skill.

### Width, scrolling, and verification

- [x] Wrap all prose to available terminal cells after indentation and padding.
  Handle ANSI styles and wide Unicode correctly. Long paths and regex must also
  remain fully accessible through wrapping; no horizontal scrolling is required.
- [x] Let compact summaries grow vertically on narrow terminals. Use stored
  labels and prose faithfully; do not generate unverified semantic summaries at
  render time. Longer explanations belong in the expanded view.
- [x] Provide explicit detail scrolling (Page Up/Down and Home/End), with a
  visible more-content indicator. Up/Down selects problems; the footer explains
  the current interaction. Keep the selected summary as context while reading
  long details, and preserve selection through resize and refresh when possible.
- [x] Verify narrow and wide terminals, long prose, long unbroken regex/paths,
  mixed pending/tracked items, declined suggestions, unknown installation state,
  and expanded content taller than the viewport. Check that every detail remains
  reachable and an unavailable comparison stays visible in collapsed form.

## Redesign the Guards panel

- [x] Rename **Hooks & presets** to **Guards**. Use **Built-in guards** for
  presets and remove **catalog roster**. Keep hook terminology and exact command
  names in setup or troubleshooting details where the mechanism matters.
- [x] Put **Your guards** first, followed by **Available built-in guards**.
  Show readable descriptions and installation location. Avoid listing an enabled
  built-in guard twice as though the catalog and installed inventory were
  separate protections.
- [x] Explain inventory origins in familiar language: custom guards generated
  through Steer, manually authored guards registered with Steer, and built-in
  guards. This inventory does not include every agent hook, such as the commit
  gate or unrelated SessionStart hooks.
- [x] Make empty states location-specific: **No guards recorded for this
  project**, for example. An empty project inventory establishes nothing about
  global guards or another machine. Manifest membership alone must not imply
  that an agent has approved and is running the guard.
- [x] Replace the unexplained **sync** and **rebuild master** footer actions
  with maintenance actions beside detected issues. Explain why the action is
  needed, which location and agents it affects, and what it will change.
  Keep advanced repair commands reachable in details when needed.
- [x] Offer **Update built-in guards** for outdated bundled definitions, with
  repair-specific wording for missing or changed files and registrations.
  Explain that this reconciles installed built-in guards with the catalog in
  the running Steer version and can replace local edits. It also ensures
  internal supporting guards; do not describe it as a read-only refresh or
  synchronization between machines. Preserve the existing sync semantics.
- [x] Offer **Update guard runner** when the installed runtime is outdated,
  with repair-specific wording for stale registrations. Explain that this
  rebuilds the shared runner from existing guard sources using the current
  runtime and updates agent registrations and derived instructions. Individual
  guard sources are preserved; with no guards, the operation removes runner
  registrations. Keep these details proportional to the detected issue.
- [x] Verify installed custom and built-in guards, available guards, empty
  project inventory with global guards present, unknown agent approval, and
  maintenance-needed states. Check that issue text names the appropriate
  operation and that a healthy screen does not demand routine maintenance.

## Redesign the Reviewer panel

- [x] Present **Settings for review before commit**, with a clear indication
  that the saved settings apply across projects on this machine, subject to
  overrides. Move the config path and raw configuration keys into details.
- [x] Use **Reviewer CLI**, **Model**, **Reasoning effort**, **Review runs**,
  and **Timeout per run** as field labels. Show readable values and units,
  such as **Provider default** and **5 minutes**.
- [x] Replace provider free text with an inline selector: Enter opens it,
  Up/Down chooses agy, Codex, or Claude Code, Enter saves, and Escape cancels.
  Derive choices from the supported provider definition rather than duplicating
  the enum in the TUI. Preserve the existing impact confirmation before a save
  that affects ongoing measurements.
- [x] Keep Model as free text with an explicit **Use provider default** action.
  Do not poll providers or maintain a model catalog in the TUI.
- [x] Keep Reasoning effort flexible. Provider-specific suggestions may assist
  entry, but retain custom input and the existing pass-through behavior for
  values beyond documented suggestions.
- [x] Use numeric editors for runs and timeout, with units and local validation
  messages. Explain what a review run is and that increasing the count increases
  review work and likely cost. Preserve the underlying setting semantics.
- [x] Replace **empty clears the key** with an explicit **Use default** action.
  Explain the field-specific fallback and any override that still applies.
  Use **Save** instead of **write**, with feedback beside the edited field.
- [x] Show help when selecting or editing a field. Replace the always-visible
  precedence paragraph with contextual override messages. Where values differ,
  show the stored setting and effective value separately, naming the environment
  variable or affected agent registration that supplies the override.
- [x] Before saving an overridden field, explain that the edit changes the
  stored setting and may not change what runs. Describe hook-command overrides
  per affected setting and agent; do not imply that one override masks every
  reviewer setting.
- [x] Treat provider, model, and effort as related settings. When changing
  provider, explicitly offer to keep the existing model/effort or use the new
  provider's defaults. Do not silently carry potentially incompatible values
  forward or discard them. Preview and save the chosen combination together.
- [x] Preserve the existing measurement-impact preview, naming affected
  problems and explaining the consequence in plain language. Distinguish changes
  that prevent fair comparison from lesser changes that only qualify a result.
- [x] When a timeout change needs a registered hook timeout updated, give a
  concrete follow-up naming the affected agent/location and operation. Do not
  imply that saving the engine timeout alone updates every agent's wall clock.
- [x] Verify selector navigation and cancellation, defaults, custom model and
  effort values, numeric input, stored/effective differences, provider switches,
  impact confirmation, and timeout follow-up. Keep text readable at narrow widths.

## Redesign Health as Diagnostics

- [x] Rename **Health** to **Diagnostics**. Organize it into **Issues detected**,
  **History on this machine**, and expandable **Technical details**. The page
  detects specific limitations and exposes provenance; it does not verify the
  entire system's health.
- [x] Lead with concrete issues, their consequences, and repair destinations:
  unresolved review outcomes point to Arming, and outdated or unknown guard
  runtimes point to the appropriate maintenance action in Guards. If none are
  detected, say **No issues detected by these checks**, making the checks'
  coverage clear rather than claiming everything works.
- [x] Show each history count once and remove the separate **Telemetry** block.
  Use **review sequences**, **agent sessions**, **actions blocked by auto mode**,
  and **guard blocks**, preserving their source boundaries. The existing
  review-findings size counts review sequences, not individual findings or raw
  log rows; it duplicates the telemetry review count, as do the denial counts.
- [x] Render history counts in normal foreground text. Data presence alone
  establishes neither freshness nor completeness nor successful operation, so
  do not color counts green as a health signal. Keep absence explicitly local
  to this machine and distinguish it from an actual read failure when known.
- [x] Describe current runner state as **Guard runner matches this Steer
  version**, with the relevant agent and installation location. Keep version
  identifiers and runtime digests in details; a matching runtime does not prove
  that an agent has approved or executed it.
- [x] Put storage paths, runner versions and digests, exact reviewer provenance,
  and team configuration in expandable technical details. Preserve access for
  inspection and troubleshooting without repeating normal Reviewer settings.
- [x] Present the optional lockfile as **Team guard configuration**. When absent,
  say **Not configured for this project** without implying a fault. Distinguish
  an existing unreadable or malformed file from an absent file and report the
  actual error. This requires repairing the reader's current error-to-absence
  behavior as well as changing the display.
- [x] Keep operation feedback attached to its originating panel and clear it
  appropriately when navigating. A Reviewer cancellation must not remain as
  **cancelled** on Diagnostics or another unrelated page. Account for async
  operations finishing after the user changes panels.
- [x] Verify healthy-under-these-checks, unresolved outcomes, outdated/unknown
  runtimes, empty local history, malformed/unreadable team configuration, expanded
  long paths, and cross-panel feedback. Check that counts have the correct nouns,
  appear once, and convey no unsupported health claim.

## Complete the visible vocabulary pass

- [x] Audit the whole TUI against `terms-final.md`: navigation labels, headings,
  row text, empty states, key legends, editors, confirmations, and errors. Include
  displayed values and shared formatting helpers, not only local string literals.
- [x] Use **problem / recurring problem** for class and **problem to consider**
  for candidate. Use **suggested fix** only once an actual change is proposed.
- [x] Replace **Hooks & presets — catalog roster** with guard-oriented wording,
  identifying **built-in guards** where appropriate. Preserve distinctions where
  an actual hook is not a guard; exact CLI command names remain valid in details.
- [x] Replace **x retire** with **stop tracking** wording. Explain that stopping
  tracking ends measurement and leaves the installed change in place.
- [x] Replace **instrument drift** with an explanation of what changed, following
  the provenance-display actions below. Apply the same vocabulary to Diagnostics.
- [x] Replace user-facing **transcripts** with **session history**, including
  the Arming panel's read-outcomes action and progress text.
- [x] Review source labels, `scope`, and other technical terms contextually:
  preserve what was counted, where installation applies, and which work enters
  a comparison. Avoid mechanically replacing words that carry different meanings.
- [x] Keep Go symbols, stored fields, JSON tokens, and compatibility aliases as
  specified in `terms-final.md`; the pending work concerns what a person reads.

## Clarify configuration reach and evidence provenance

- [x] Replace the shared **scope: project** label with contextual wording such
  as **Configure: this project**, shown only where it describes an action's
  actual reach. Account for actions that name their own project/global target.
- [x] Keep installation reach distinct from the work displayed or measured.
  The current header prints `m.scope`, while the dashboard gathers evidence
  across projects. The header must not imply that Problems is project-filtered;
  retain each comparison's own eligibility details.
- [x] Remove the **instrument** label and composite identifier from the shared
  header. The run count, prompt digest, and engine identity are diagnostic
  provenance, with no direct action in that location.
- [x] Keep readable reviewer settings in **Reviewer** and exact provenance
  reachable in troubleshooting details for inspection and reproduction.
- [x] Explain relevant production changes beside affected results in plain
  language, for example **The reviewer model and review prompt changed between
  these periods**. Name only changes supported by that result's evidence.
- [x] Distinguish reviewer provenance from session-parser provenance. A global
  reviewer identifier cannot explain every problem's comparison, because
  session-based measurements depend on their parser identity.

## Close the verification gap

- [x] Repair the vocabulary test's assumption that every string without
  whitespace is a machine token. `"Classes"` is a visible navigation label that
  the current `isPhrase` check skips.
- [x] Review vocabulary coverage against `terms-final.md`. The current word list
  omits candidate, instrument, and preset; contextual replacements need checks
  that distinguish legitimate technical details from primary display copy.
- [x] Verify rendered navigation and relevant states, including mixed pending
  and tracked rows, empty inventories, and stop-tracking selection after merging.
- [x] Update `CLAUDE.md` to reflect the resulting panel layout and behavior.
- [x] Run `./check.sh` for implementation changes, rebuild bundled binaries for
  CLI changes, and have Jeremy inspect the resulting TUI for clarity and layout.
  (`./check.sh` is green and the binaries are rebuilt. Jeremy's own read of the
  running TUI is the remaining half; no mechanical oracle covers layout.)

## Evidence and boundaries

- `3e188e7` removed candidate authoring and retained the queue for dense reading.
  This pass preserves the visibility while changing its placement.
- `51cf323` applied familiar-language wording to panel content, but
  `cli/internal/tui/dashboard.go` still labels the tab **Classes**, while
  `classes.go` already heads its content **Problems**.
- `cli/internal/status/vocabulary_test.go` includes TUI files but skips
  whitespace-free literals and checks only a subset of the plan's vocabulary.
- Further TUI items will be added as the discussion continues. This list does
  not authorize changes to scoring, proposal decisions, or removal semantics.
