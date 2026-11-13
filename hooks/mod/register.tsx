// Steer's Claude Code mod: a band above the prompt that shows this session's commit
// review while it runs and how it ended. It reads the live review file the commit gate
// writes and handles no tool event; the gate itself stays in the settings hooks.

import type { EngineInterface, Register, RenderChildren, RenderInput } from 'claude-code'

import { BOOK_COLUMNS, BOOK_ROWS, COLORS, FRAME_MS, MARK_MIN_COLUMNS, RUN_COLUMNS, bookCells, bookStep, runCells } from './marks'
import { bySeverity, clock, parseLive, pickView, plural, sessionSlug, severityCounts } from './review'
import type { LiveReview, ReviewView } from './review'

const POLL_MS = 1000
// How many polls pass before the state directory is resolved again.
const ROOT_EVERY = 30
// Findings the held band lists by name; the rest are counted.
const FINDINGS_SHOWN = 2

const ANSWERED = new Set(['ok', 'salvaged', 'salvaged_clean'])

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
  const seconds = view.kind === 'running' ? Math.floor(view.elapsedMs / 1000) : 0
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

function band($: EngineInterface, e: RenderInput<'AbovePrompt'>, view: ReviewView) {
  const els = $.ui.resolve(e)
  const { Box, Text } = els
  const { doc } = view
  const columns = typeof e.props.bodyColumns === 'number' ? e.props.bodyColumns : 0
  const Raster = e.surface === 'terminal' && 'Raster' in els && columns >= MARK_MIN_COLUMNS ? els.Raster : undefined
  // The columns left for a line of text beside the rail and the mark.
  const room = Math.max(20, columns - 2 - (Raster ? BOOK_COLUMNS + 2 : 0))
  const right = `${who(doc, columns)} · ${clock(view.elapsedMs)}`

  const head = (label: RenderChildren) => (
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
    for (let run = 1; run <= doc.runs; run++) {
      const outcome = done.get(run)
      const word = outcome === undefined ? 'reading' : ANSWERED.has(outcome) ? 'done' : 'no answer'
      if (Raster) {
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
      head(<Text dimColor> · at commit</Text>),
      <Text wrap="truncate-end">
        {doc.final
          ? `Steer reads the last fixes with ${plural(doc.runs, 'run')}. This review does not hold the commit.`
          : `Steer holds the commit while ${plural(doc.runs, 'run')} read${doc.runs === 1 ? 's' : ''} the diff.`}
      </Text>,
      <Box flexWrap="wrap">{runs}</Box>,
      <Text dimColor>Claude waits for the result.</Text>,
    ]
  } else if (view.kind === 'held' || view.kind === 'handed') {
    rail = view.kind === 'held' ? COLORS.held : COLORS.pass
    if (Raster) mark = <Raster key="book" columns={BOOK_COLUMNS} rows={BOOK_ROWS} cells={bookCells()} />
    const sorted = bySeverity(doc.findings)
    const shown = sorted.slice(0, FINDINGS_SHOWN)
    const more = sorted.length - shown.length
    const title = view.kind === 'held' ? 'commit held' : 'commit not held'
    const count = sorted.length > 0 ? ` · ${plural(sorted.length, 'finding')}: ${severityCounts(sorted)}` : ''
    lines = [
      head(<Text weight="bold"> · {title}{count}</Text>),
      ...shown.map(f => (
        <Text wrap="truncate-end">
          <Text weight={f.severity === 'BLOCKING' ? 'bold' : 'regular'} dimColor={f.severity !== 'BLOCKING'}>
            {f.severity.padEnd(12)}
          </Text>
          {cut(`${f.file}  ${f.claim}`, room - 12)}
        </Text>
      )),
      <Text dimColor>
        {more > 0 ? `…${plural(more, 'more finding')}. ` : ''}Claude now fixes or rejects each finding.
      </Text>,
    ]
  } else if (view.kind === 'passed') {
    rail = COLORS.pass
    lines = [
      head(
        <Text>
          <Text weight="bold"> · commit passed</Text>
          <Text dimColor> · {plural(doc.answered, 'run')}, no findings</Text>
        </Text>,
      ),
    ]
  } else {
    rail = COLORS.held
    lines = [
      head(
        <Text>
          <Text weight="bold"> · commit not reviewed</Text>
          <Text dimColor> · no reviewer answered</Text>
        </Text>,
      ),
    ]
  }
  if (view.kind !== 'running' && moving?.requestId === e.requestId) stopFrames()

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
    const view = e.props.hasSurvey ? null : pickView(docs, sessionId, await $.clock.now())
    drawnKey = keyOf(view)
    if (!view) {
      if (moving?.requestId === e.requestId) stopFrames()
      return next(e)
    }
    return band($, e, view)
  })
}
