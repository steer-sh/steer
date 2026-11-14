// Steer's Claude Code mod: a band above the prompt that shows this session's commit
// review while it runs and how it ended. It reads the live review file the commit gate
// writes and handles no tool event; the gate itself stays in the settings hooks.

import type { EngineInterface, Register, RenderChildren, RenderInput } from 'claude-code'

import { BOOK_COLUMNS, BOOK_ROWS, COLORS, FRAME_MS, MARK_MIN_COLUMNS, RUN_COLUMNS, bookCells, bookStep, runCells } from './marks'
import { bySeverity, clock, parseLive, pickView, plural, sessionSlug, severityCounts } from './review'
import type { LiveReview, ReviewView } from './review'
import {
  BOOK_HEIGHT,
  BOOK_SMALL_HEIGHT,
  BOOK_SMALL_WIDTH,
  BOOK_WIDTH,
  GROUND_HEIGHT,
  LABEL_COLOR,
  PULSE_HEIGHT,
  PULSE_WIDTH,
  RAIL_WIDTH,
  SKY_WIDTH,
  WASH_WIDTH,
  bookSvg,
  cardColors,
  pulseSvg,
  railSvg,
  skySvg,
  washSvg,
} from './vector'
import type { Motion, Pose, Tone } from './vector'

const POLL_MS = 1000
// How many polls pass before the state directory is resolved again.
const ROOT_EVERY = 30
// Findings the held band lists by name; the rest are counted.
const FINDINGS_SHOWN = 2

const ANSWERED = new Set(['ok', 'salvaged', 'salvaged_clean'])

// Columns the card's edge and the book take from a line where the band is drawn with `Svg`.
const VECTOR_MARK_COLUMNS = 18
// The height of a line of text where the band is drawn with `Svg`, in CSS px.
const ROW_PX = 20
// Columns a run's pulse and the gap after it take beside its words.
const PULSE_COLUMNS = 4

/** How each view looks where the band is drawn with `Svg`: its hue, its book and what the book says. */
const VECTOR: Record<ReviewView['kind'], { tone: Tone; pose: Pose; motion: Motion; alt: string }> = {
  running: { tone: 'review', pose: 'reading', motion: 'live', alt: 'review running' },
  held: { tone: 'held', pose: 'marked', motion: 'still', alt: 'commit held' },
  handed: { tone: 'pass', pose: 'open', motion: 'still', alt: 'commit not held' },
  passed: { tone: 'pass', pose: 'checked', motion: 'still', alt: 'commit passed' },
  unreviewed: { tone: 'held', pose: 'closed', motion: 'still', alt: 'commit not reviewed' },
}

type Timer = { cancel(): void }

let pollTimer: Timer | null = null
let polls = 0
let liveRoot = ''
let sessionId = ''
let docs: LiveReview[] = []
// What the band last drew from, so a poll redraws only on a change.
let drawnKey = ''

// The moving marks' timer and the band instance it repaints.
let frameTimer: Timer | null = null
let frame = 0
let moving: { requestId: string; runs: number; done: Set<number> } | null = null

// The review state the vector band last drew and when it first drew it: its art's time zero.
let entered = { id: '', at: 0 }
// Whether the band shows a running clock. A redraw restarts the vector art's animations,
// so the vector band shows none and redraws only on a change of state.
let ticking = true

/** The directory that holds this session's live review files, as statestore.Resolve picks it. */
async function resolveRoot($: EngineInterface): Promise<string> {
  const override = await $.env.get('COMMIT_GATE_STATE_DIR')
  if (override) return `${override}/live`
  const home = await $.env.get('HOME')
  if (!home) return ''
  const neutral = `${home}/.steer/state/commit-review`
  const legacy = `${home}/.claude/state/commit-review`
  if (await $.fs.exists(`${neutral}/.steer-state`)) return `${neutral}/live`
  const old = await $.fs.list(legacy).catch(() => [])
  return `${old.length > 0 ? legacy : neutral}/live`
}

async function poll($: EngineInterface): Promise<void> {
  if (polls++ % ROOT_EVERY === 0 || !liveRoot) liveRoot = await resolveRoot($)
  if (!sessionId) sessionId = await $.session.id()
  const slug = sessionSlug(sessionId)
  const read: LiveReview[] = []
  if (liveRoot && slug) {
    const dir = `${liveRoot}/${slug}`
    const entries = await $.fs.list(dir).catch(() => [])
    for (const entry of entries) {
      if (entry.kind !== 'file' || !entry.name.endsWith('.json') || entry.name.startsWith('.')) continue
      const text = await $.fs.read(`${dir}/${entry.name}`).catch(() => '')
      const doc = parseLive(text)
      if (doc) read.push(doc)
    }
  }
  docs = read
  const view = pickView(docs, sessionId, await $.clock.now())
  const key = keyOf(view)
  if (key !== drawnKey) $.ui.invalidate('ui.render')
}

/** Everything of a view that changes what the band draws. */
function keyOf(view: ReviewView | null): string {
  if (!view) return ''
  const { doc } = view
  const seconds = view.kind === 'running' && ticking ? Math.floor(view.elapsedMs / 1000) : 0
  return [view.kind, doc.started_ms, doc.round, doc.runs_ended.length, doc.findings.length, seconds].join(':')
}

function startPolling($: EngineInterface) {
  if (pollTimer) return
  pollTimer = $.clock.every(POLL_MS, () => void poll($).catch(() => undefined))
  void poll($).catch(() => undefined)
}

function stopFrames() {
  frameTimer?.cancel()
  frameTimer = null
  moving = null
}

/** Repaints the reading light and each reading run's pulse until the band stops showing a running review. */
function startFrames($: EngineInterface) {
  if (frameTimer) return
  frameTimer = $.clock.every(FRAME_MS, () => {
    const shown = moving
    if (!shown) return stopFrames()
    frame++
    const blits = []
    if (bookStep(frame) !== bookStep(frame - 1)) blits.push({ key: 'book', cells: bookCells(frame) })
    for (let run = 1; run <= shown.runs; run++)
      if (!shown.done.has(run)) blits.push({ key: `run-${run}`, cells: runCells(false, frame, run * 5) })
    for (const blit of blits)
      void $.ui
        .blit({ requestId: shown.requestId, ...blit })
        .then(r => {
          if ('deny' in r && r.deny && moving === shown) stopFrames()
        })
        .catch(() => undefined)
  })
}

function who(doc: LiveReview, columns: number): string {
  const round = doc.final ? 'final review' : `round ${doc.round} of ${doc.max_rounds}`
  const reviewer = doc.model && columns >= 100 ? `${doc.reviewer} · ${doc.model}` : doc.reviewer
  return `${round} · ${reviewer}`
}

const cut = (text: string, width: number) => (text.length <= width ? text : `${text.slice(0, Math.max(1, width - 1))}…`)

function band($: EngineInterface, e: RenderInput<'AbovePrompt'>, view: ReviewView, now: number) {
  const els = $.ui.resolve(e)
  const { Box, Text } = els
  const { doc } = view
  const columns = typeof e.props.bodyColumns === 'number' ? e.props.bodyColumns : 0
  const Raster = e.surface === 'terminal' && 'Raster' in els && columns >= MARK_MIN_COLUMNS ? els.Raster : undefined
  const Svg = e.surface === 'desktop' && 'Svg' in els ? els.Svg : undefined
  const vector = VECTOR[view.kind]
  ticking = !Svg
  // Seconds since this state first drew: where the vector art's animations stand.
  let t = 0
  if (Svg) {
    const id = `${view.kind}:${doc.started_ms}:${doc.round}`
    if (entered.id !== id) entered = { id, at: now }
    t = Math.max(0, now - entered.at) / 1000
  }
  // The columns left for a line of text beside the rail and the mark.
  // A narrow Desktop band leaves the book out, as a narrow terminal leaves its marks out.
  const book = Svg !== undefined && columns >= MARK_MIN_COLUMNS
  const room = Math.max(20, columns - 2 - (Raster ? BOOK_COLUMNS + 2 : book ? VECTOR_MARK_COLUMNS : Svg ? 2 : 0))
  const right = view.kind === 'running' && !ticking ? who(doc, columns) : `${who(doc, columns)} · ${clock(view.elapsedMs)}`

  const head = (label: RenderChildren) => {
    if (!Svg)
      return (
        <Box justifyContent="space-between" columnGap={2}>
          <Text wrap="truncate-end">
            <Text color={COLORS.review} weight="bold">
              REVIEW
            </Text>
            {label}
          </Text>
          <Text dimColor>{right}</Text>
        </Box>
      )
    // Desktop truncates only a Text that holds plain text, so each part is its own
    // Box that gives way; the right-hand text gives way first.
    return (
      <Box flexDirection="row" justifyContent="space-between" columnGap={2}>
        <Box flexDirection="row" flexShrink={1} minWidth={0}>
          <Box flexShrink={0}>
            <Text color={LABEL_COLOR} weight="bold">
              REVIEW
            </Text>
          </Box>
          <Box flexShrink={1} minWidth={0}>
            {label}
          </Box>
        </Box>
        <Box flexShrink={3} minWidth={0}>
          <Text dimColor wrap="truncate">
            {right}
          </Text>
        </Box>
      </Box>
    )
  }

  /** The header's words after `REVIEW`: a bold part, then a dim part; either may be empty. */
  const label = (bold: string, dim = '') => {
    if (!Svg)
      return (
        <Text>
          {bold ? <Text weight="bold"> · {bold}</Text> : ''}
          {dim ? <Text dimColor> · {dim}</Text> : ''}
        </Text>
      )
    const parts: RenderChildren[] = []
    if (bold)
      parts.push(
        <Box flexShrink={1} minWidth={0}>
          <Text weight="bold" wrap="truncate">{` · ${bold}`}</Text>
        </Box>,
      )
    if (dim)
      parts.push(
        <Box flexShrink={1} minWidth={0}>
          <Text dimColor wrap="truncate">{` · ${dim}`}</Text>
        </Box>,
      )
    return (
      <Box flexDirection="row" minWidth={0}>
        {parts}
      </Box>
    )
  }
  // Rows the Desktop band's text takes beyond one per line: the run pulses' row can wrap.
  let wrapped = 0

  let rail: string = COLORS.review
  let mark: RenderChildren = null
  let lines: RenderChildren[]

  if (view.kind === 'running') {
    const done = new Map(doc.runs_ended.map(r => [r.run, r.outcome]))
    if (Raster) {
      moving = { requestId: e.requestId, runs: doc.runs, done: new Set(done.keys()) }
      startFrames($)
      mark = <Raster key="book" columns={BOOK_COLUMNS} rows={BOOK_ROWS} cells={bookCells(frame)} />
    }
    const runs: RenderChildren[] = []
    // Each run's width in columns: a run wraps to the next row whole.
    const runWidths: number[] = []
    for (let run = 1; run <= doc.runs; run++) {
      const outcome = done.get(run)
      const word = outcome === undefined ? 'reading' : ANSWERED.has(outcome) ? 'done' : 'no answer'
      if (Svg) {
        runWidths.push(PULSE_COLUMNS + `run ${run} ${word}`.length)
        runs.push(
          <Box flexDirection="row" alignItems="center" columnGap={1} marginRight={run < doc.runs ? 2 : 0} flexShrink={0}>
            <Box flexShrink={0} alignItems="center">
              <Svg source={pulseSvg(outcome !== undefined, t, run * 0.4)} alt={word} width={PULSE_WIDTH} height={PULSE_HEIGHT} />
            </Box>
            <Text dimColor>run {run} {word}</Text>
          </Box>,
        )
      } else if (Raster) {
        runs.push(
          <Text dimColor>run {run} </Text>,
          <Raster key={`run-${run}`} columns={RUN_COLUMNS} rows={1} cells={runCells(outcome !== undefined, frame, run * 5)} />,
          <Text dimColor> {word}{run < doc.runs ? '   ' : ''}</Text>,
        )
      } else {
        runs.push(<Text dimColor>run {run} {word}{run < doc.runs ? ' · ' : ''}</Text>)
      }
    }
    lines = [
      head(label('', 'at commit')),
      <Text wrap="truncate-end">
        {doc.final
          ? `Steer reads the last fixes with ${plural(doc.runs, 'run')}. This review does not hold the commit.`
          : `Steer holds the commit while ${plural(doc.runs, 'run')} read${doc.runs === 1 ? 's' : ''} the diff.`}
      </Text>,
      <Box flexWrap="wrap">{runs}</Box>,
      <Text dimColor>Claude waits for the result.</Text>,
    ]
    // Lays the runs out as flex-wrap does, two columns apart, and counts the extra rows.
    let used = 0
    for (const width of runWidths) {
      if (used > 0 && used + 2 + width > room) {
        wrapped++
        used = 0
      }
      used += (used > 0 ? 2 : 0) + width
    }
  } else if (view.kind === 'held' || view.kind === 'handed') {
    rail = view.kind === 'held' ? COLORS.held : COLORS.pass
    if (Raster) mark = <Raster key="book" columns={BOOK_COLUMNS} rows={BOOK_ROWS} cells={bookCells()} />
    const sorted = bySeverity(doc.findings)
    const shown = sorted.slice(0, FINDINGS_SHOWN)
    const more = sorted.length - shown.length
    const title = view.kind === 'held' ? 'commit held' : 'commit not held'
    const count = sorted.length > 0 ? ` · ${plural(sorted.length, 'finding')}: ${severityCounts(sorted)}` : ''
    lines = [
      head(label(`${title}${count}`)),
      ...shown.map(f =>
        Svg ? (
          <Box flexDirection="row" minWidth={0}>
            <Box flexShrink={0}>
              <Text weight={f.severity === 'BLOCKING' ? 'bold' : 'regular'} dimColor={f.severity !== 'BLOCKING'}>
                {f.severity.padEnd(12)}
              </Text>
            </Box>
            <Box flexShrink={1} minWidth={0}>
              <Text wrap="truncate">{cut(`${f.file}  ${f.claim}`, room - 12)}</Text>
            </Box>
          </Box>
        ) : (
          <Text wrap="truncate-end">
            <Text weight={f.severity === 'BLOCKING' ? 'bold' : 'regular'} dimColor={f.severity !== 'BLOCKING'}>
              {f.severity.padEnd(12)}
            </Text>
            {cut(`${f.file}  ${f.claim}`, room - 12)}
          </Text>
        ),
      ),
      <Text dimColor>
        {more > 0 ? `…${plural(more, 'more finding')}. ` : ''}Claude now fixes or rejects each finding.
      </Text>,
    ]
  } else if (view.kind === 'passed') {
    rail = COLORS.pass
    lines = [
      head(label('commit passed', `${plural(doc.answered, 'run')}, no findings`)),
    ]
  } else {
    rail = COLORS.held
    lines = [
      head(label('commit not reviewed', 'no reviewer answered')),
    ]
  }
  if (view.kind !== 'running' && moving?.requestId === e.requestId) stopFrames()

  if (Svg) {
    const { tone, motion } = vector
    const card = cardColors(tone)
    const small = lines.length === 1
    const railHeight = (lines.length + wrapped) * ROW_PX - 4
    return (
      <Box
        key="steer-review"
        position="relative"
        flexDirection="row"
        alignItems="center"
        paddingX={1}
        overflow="hidden"
        backgroundColor={card.ground}
        borderStyle="round"
        borderColor={card.edge}
      >
        <Box key="steer-wash" position="absolute" top={-1} left={0} bottom={-1}>
          <Svg source={washSvg(tone, motion, t)} alt="steer" width={WASH_WIDTH} height={GROUND_HEIGHT} />
        </Box>
        <Box key="steer-sky" position="absolute" top={-1} right={0} bottom={-1}>
          <Svg source={skySvg(tone, motion, t)} alt="steer" width={SKY_WIDTH} height={GROUND_HEIGHT} />
        </Box>
        {/* Desktop paints a Box over the absolute art only when the Box holds an absolute child. */}
        <Box position="relative" marginRight={1} flexShrink={0} alignItems="center">
          <Box position="absolute" top={0} left={0} />
          <Svg source={railSvg(tone, railHeight)} alt="steer" width={RAIL_WIDTH} height={railHeight} />
        </Box>
        {book ? (
          <Box position="relative" marginRight={2} flexShrink={0} alignItems="center">
            <Box position="absolute" top={0} left={0} />
            <Svg
              source={bookSvg(tone, vector.pose, t, small)}
              alt={vector.alt}
              width={small ? BOOK_SMALL_WIDTH : BOOK_WIDTH}
              height={small ? BOOK_SMALL_HEIGHT : BOOK_HEIGHT}
            />
          </Box>
        ) : (
          ''
        )}
        <Box position="relative" flexDirection="column" flexGrow={1} flexShrink={1} minWidth={0}>
          <Box position="absolute" top={0} left={0} />
          {lines}
        </Box>
      </Box>
    )
  }

  return (
    <Box flexDirection="row">
      <Box flexDirection="column" marginRight={1} flexShrink={0}>
        {lines.map(() => (
          <Text color={rail}>▌</Text>
        ))}
      </Box>
      {mark ? (
        <Box marginRight={2} flexShrink={0}>
          {mark}
        </Box>
      ) : null}
      <Box flexDirection="column" flexGrow={1}>
        {lines}
      </Box>
    </Box>
  )
}

export const register: Register = on => {
  on('session.start', ($, e, next) => {
    if (e.isInteractive) startPolling($)
    return next(e)
  })

  on('ui.render', { component: 'AbovePrompt' }, async ($, e, next) => {
    // A mod installed into a running session gets no session.start.
    startPolling($)
    const now = await $.clock.now()
    const view = e.props.hasSurvey ? null : pickView(docs, sessionId, now)
    if (!view) {
      drawnKey = ''
      if (moving?.requestId === e.requestId) stopFrames()
      return next(e)
    }
    const drawn = band($, e, view, now)
    // After the band, which decides whether the clock ticks and so what the key holds.
    drawnKey = keyOf(view)
    return drawn
  })
}
