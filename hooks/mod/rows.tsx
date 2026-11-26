// Steer's transcript rows: one per guard block and check Stop block in this session,
// and one when check.sh passes again after a block. Each poll reads only the bytes
// the records gained since the last one, and places a row for each new block.

import type { EngineInterface, RenderChildren, RenderElement, RenderInput } from 'claude-code'

import { nextStep, parseCheckRun, parseExecution, parseGuardRow, repoKey, rowsOf, ruleParts, splitLines, tailAt } from './blocks'
import type { BlockRow, CheckExecution, CheckRun, GuardBlock, Tail } from './blocks'
import {
  BLOCK_ROWS,
  BOX_COLUMNS,
  COLORS,
  FRAME_MS,
  MARK_MIN_COLUMNS,
  SHIELD_COLUMNS,
  SHINE_FRAMES,
  TICK_FRAMES,
  crossCells,
  shieldCells,
  tickCells,
} from './marks'
import { plural } from './review'

// How far before a file's end the first read starts: past rows of other sessions are skipped.
const FIRST_READ_BYTES = 256 * 1024
// The most one read takes; the rest waits for the next poll.
const READ_LIMIT = 1024 * 1024
// A block recorded this long before the mod started watching still gets its row.
const GRACE_MS = 5_000
// How many polls pass before the mod looks again for repositories the session touches.
const RESCAN_EVERY = 10
// Frames the cross blinks before it stands still.
const BLINK_FOR = 30

type Timer = { cancel(): void }

/** The directory that holds a state component, as statestore.Resolve picks it. */
export async function stateRoot($: EngineInterface, component: string): Promise<string> {
  const home = await $.env.get('HOME')
  if (!home) return ''
  const neutral = `${home}/.steer/state/${component}`
  const legacy = `${home}/.claude/state/${component}`
  if (await $.fs.exists(`${neutral}/.steer-state`)) return neutral
  const old = await $.fs.list(legacy).catch(() => [])
  return old.length > 0 ? legacy : neutral
}

let watched = ''
let watchFrom = 0
let tails = new Map<string, Tail>()
let guards: GuardBlock[] = []
let runs: CheckRun[] = []
let executions: CheckExecution[] = []
// The repositories whose guard stores the mod reads, and the check-state directories whose records it reads.
let repos = new Set<string>()
let checkKeys = new Set<string>()
let polls = 0
// Whether a poll is reading: a slow poll and the next one must not read the same bytes.
let busy = false
let rows = new Map<string, BlockRow>()

/** Starts again for a new session: /clear gives the same process one. */
function reset(session: string, now: number) {
  watched = session
  watchFrom = now
  tails = new Map()
  guards = []
  runs = []
  executions = []
  repos = new Set()
  checkKeys = new Set()
  polls = 0
  rows = new Map()
}

/** The lines a file gained since the last read of it; none for a missing file. */
async function added($: EngineInterface, path: string): Promise<string[]> {
  const stat = await $.fs.stat(path).catch(() => null)
  if (!stat || stat.kind !== 'file') return []
  let tail = tails.get(path)
  if (!tail || stat.size < tail.offset) tail = tailAt(stat.size, FIRST_READ_BYTES)
  if (stat.size === tail.offset) {
    tails.set(path, tail)
    return []
  }
  const length = Math.min(stat.size - tail.offset, READ_LIMIT)
  const read = (await $.fs.read(path, { as: 'bytes', offset: tail.offset, length }).catch(() => null)) as { base64: string } | null
  if (!read) return []
  const bytes = (Uint8Array as unknown as { fromBase64(s: string): Uint8Array }).fromBase64(read.base64)
  const split = splitLines(tail, bytes)
  tails.set(path, split.tail)
  return split.lines
}

/** The repository that holds `dir`, resolved as checks.RepoIdentity names it; empty outside one. */
async function repoOf($: EngineInterface, dir: string): Promise<string> {
  for (let at = dir; at && at !== '/'; at = at.slice(0, at.lastIndexOf('/'))) {
    if (await $.fs.exists(`${at}/.git`)) {
      const stat = await $.fs.stat(at, { resolve: true }).catch(() => null)
      return stat?.realPath ?? at
    }
  }
  return ''
}

async function watch(identity: string) {
  if (!identity || repos.has(identity)) return
  repos.add(identity)
  checkKeys.add(await repoKey(identity))
}

/**
 * Watches the repository the session is in now, and each check-state directory whose
 * runs changed since the mod started watching: a check run there may be this session's.
 */
async function rescan($: EngineInterface, checks: string) {
  await watch(await repoOf($, await $.session.cwd()))
  if (!checks) return
  for (const entry of await $.fs.list(checks).catch(() => [])) {
    if (entry.kind !== 'dir' || checkKeys.has(entry.name)) continue
    const stat = await $.fs.stat(`${checks}/${entry.name}/runs.jsonl`).catch(() => null)
    if (stat && stat.mtimeMs >= watchFrom - GRACE_MS) checkKeys.add(entry.name)
  }
}

/**
 * Reads what the records gained and places a row for each new block. `seen` holds the
 * repositories other state names, such as a review's.
 */
export async function pollBlocks($: EngineInterface, session: string, now: number, seen: readonly string[]): Promise<void> {
  if (!session || busy) return
  busy = true
  try {
    await readAndPlace($, session, now, seen)
  } finally {
    busy = false
  }
}

async function readAndPlace($: EngineInterface, session: string, now: number, seen: readonly string[]): Promise<void> {
  if (session !== watched) reset(session, now)
  const checks = await stateRoot($, 'checks')
  if (polls++ % RESCAN_EVERY === 0) await rescan($, checks)
  for (const repo of seen) await watch(repo)
  const home = (await $.env.get('HOME')) ?? ''
  const stores = home ? [`${home}/.steer/hooks`, `${home}/.claude/hooks`] : []
  for (const repo of repos) stores.push(`${repo}/.steer/hooks`, `${repo}/.claude/hooks`)
  for (const store of stores) {
    for (const line of await added($, `${store}/steer-audit.jsonl`)) {
      const block = parseGuardRow(line)
      if (block?.session !== session) continue
      guards.push(block)
      await watch(block.repo)
    }
  }
  if (checks)
    for (const key of [...checkKeys]) {
      // Runs first: the runner appends a run's executions before the run, so a run read here
      // normally has them; one whose executions a later poll reads gets its row updated.
      for (const line of await added($, `${checks}/${key}/runs.jsonl`)) {
        const run = parseCheckRun(line)
        if (run?.session !== session) continue
        runs.push(run)
        await watch(run.repo)
      }
      for (const line of await added($, `${checks}/${key}/executions.jsonl`)) {
        const ex = parseExecution(line)
        if (ex?.session === session) executions.push(ex)
      }
    }
  let changed = false
  for (const row of rowsOf(guards, runs, executions, session)) {
    const had = rows.get(row.id)
    if (had) {
      // Executions or runs a later poll read update a row already placed.
      if (shownOf(had) !== shownOf(row)) changed = true
      rows.set(row.id, row)
      continue
    }
    if (atOf(row) < watchFrom - GRACE_MS) continue
    rows.set(row.id, row)
    // A placement that fails, or that no surface will draw, is tried again at the next poll.
    void $.ui
      .place({ id: row.id })
      .then(r => {
        if (!r.isPlaced && !r.requestId) rows.delete(row.id)
      })
      .catch(() => rows.delete(row.id))
  }
  if (changed) $.ui.invalidate('ui.render')
}

/** What of a row a later poll can change: its failed checks, its run number and its repository label. */
const shownOf = (row: BlockRow) =>
  row.kind === 'guard' ? '' : [row.kind === 'check-failed' ? row.failed.join(',') : '', row.n, row.place].join('|')

const atOf = (row: BlockRow) => (row.kind === 'guard' ? row.block.atMs : row.run.finishedMs)

// The rows whose mark still moves, by the request that draws them, and the frame each started at.
let frameTimer: Timer | null = null
let frame = 0
const moving = new Map<string, { row: BlockRow; start: number }>()
const settled = new Set<string>()

/** How long a row's mark moves, in frames. */
const runFor = (row: BlockRow) => (row.kind === 'guard' ? SHINE_FRAMES : row.kind === 'check-failed' ? BLINK_FOR : TICK_FRAMES)

function cellsFor(row: BlockRow, t?: number): string {
  if (row.kind === 'guard') return shieldCells(t)
  return row.kind === 'check-failed' ? crossCells(t) : tickCells(t)
}

function startFrames($: EngineInterface) {
  if (frameTimer) return
  frameTimer = $.clock.every(FRAME_MS, () => {
    frame++
    for (const [requestId, { row, start }] of moving) {
      const t = frame - start
      const done = t >= runFor(row)
      if (done) {
        moving.delete(requestId)
        settled.add(row.id)
      }
      void $.ui.blit({ requestId, key: 'mark', cells: cellsFor(row, done ? undefined : t) }).catch(() => undefined)
    }
    if (moving.size === 0) {
      frameTimer?.cancel()
      frameTimer = null
    }
  })
}

/** The noun for what a guard stopped, by the tool it stopped. */
const stoppedNoun = (tool: string) =>
  tool === 'Bash' ? 'command' : ['Edit', 'Write', 'MultiEdit', 'NotebookEdit', 'apply_patch'].includes(tool) ? 'edit' : 'call'

/** Up to two failed check names, then how many more. */
function failedNames(failed: readonly string[]): string {
  const shown = failed.slice(0, 2)
  const more = failed.length - shown.length
  return [...shown, ...(more > 0 ? [`${more} more`] : [])].join(' · ')
}

/** Line 2 of a row: plain text with one part in a colour of its own. */
type Result = { lead: string; hot?: string; hotColor?: string; tail?: string }

/** What a row says: its label, rail, timing phrase, the dim name at the right, and its two sentences. */
function wordsOf(row: BlockRow): { label: string; hue: string; rail: string; when: string; name: string; result: Result; next: string } {
  if (row.kind === 'guard') {
    const { block } = row
    const { never, instead } = ruleParts(block.rule)
    const actor = block.thread ? 'The subagent' : 'Claude'
    const noun = stoppedNoun(block.tool)
    return {
      label: 'GUARD',
      hue: COLORS.guard,
      rail: COLORS.guard,
      when: 'before a tool runs',
      name: block.hook,
      result: { lead: never ? `Steer stopped this ${noun}: ${never}.` : `Steer stopped this ${noun}.` },
      next: `${actor} ${nextStep(instead, noun)}.`,
    }
  }
  const name = `${row.place ? `${row.place} · ` : ''}run ${row.n}`
  if (row.kind === 'check-passed')
    return {
      label: 'CHECK',
      hue: COLORS.check,
      rail: COLORS.pass,
      when: 'when the turn ends',
      name,
      result: { lead: 'check.sh passes now: ', hot: 'all checks passed', hotColor: COLORS.pass, tail: '.' },
      next: 'The turn ends.',
    }
  const { failed } = row
  const result: Result =
    failed.length > 0
      ? {
          lead: 'Steer kept the turn open: ',
          hot: `${plural(failed.length, 'check')} ${row.allFailed ? 'failed' : 'did not pass'}`,
          hotColor: COLORS.held,
          tail: ' in check.sh.',
        }
      : row.run.outcome === 'passed'
        ? { lead: 'Steer kept the turn open: ', hot: 'a declared check left no result', hotColor: COLORS.held, tail: '.' }
        : { lead: 'Steer kept the turn open: ', hot: `check.sh ${row.run.outcome || 'failed'}`, hotColor: COLORS.held, tail: '.' }
  const names = failedNames(failed)
  return {
    label: 'CHECK',
    hue: COLORS.check,
    rail: COLORS.check,
    when: 'when the turn ends',
    name,
    result,
    next: `${names ? `${names}. ` : ''}Claude continues until check.sh passes.`,
  }
}

/** Draws one of Steer's placed rows; undefined for a row that is not Steer's. */
export function drawRow($: EngineInterface, e: RenderInput<'Placed'>): RenderElement | undefined {
  if (e.props.plugin !== $.plugin.name) return undefined
  const row = rows.get(e.props.id)
  if (!row) return undefined
  const els = $.ui.resolve(e)
  const { Box, Text } = els
  const w = wordsOf(row)
  const columns = e.viewport?.columns ?? MARK_MIN_COLUMNS
  const Raster = e.surface === 'terminal' && 'Raster' in els && columns >= MARK_MIN_COLUMNS ? els.Raster : undefined
  let mark: RenderChildren = null
  if (Raster) {
    const live = moving.get(e.requestId)
    if (!live && !settled.has(row.id)) {
      moving.set(e.requestId, { row, start: frame })
      startFrames($)
    }
    const t = settled.has(row.id) ? undefined : frame - (moving.get(e.requestId)?.start ?? frame)
    const width = row.kind === 'guard' ? SHIELD_COLUMNS : BOX_COLUMNS
    mark = <Raster key="mark" columns={width} rows={BLOCK_ROWS} cells={cellsFor(row, t)} />
  }
  const lines = [
    <Box flexDirection="row" justifyContent="space-between" columnGap={2}>
      <Box flexShrink={1} minWidth={0}>
        <Text wrap="truncate-end">
          <Text color={w.hue} weight="bold">
            {w.label}
          </Text>
          <Text dimColor> · {w.when}</Text>
        </Text>
      </Box>
      <Box flexShrink={3} minWidth={0}>
        <Text dimColor wrap="truncate">
          {w.name}
        </Text>
      </Box>
    </Box>,
    <Text wrap="truncate-end">
      {w.result.lead}
      {w.result.hot ? <Text color={w.result.hotColor}>{w.result.hot}</Text> : ''}
      {w.result.tail ?? ''}
    </Text>,
    <Text dimColor wrap="truncate-end">
      {w.next}
    </Text>,
  ]
  return (
    <Box key={`steer-${row.id}`} flexDirection="row">
      <Box flexDirection="column" marginRight={1} flexShrink={0}>
        {lines.map(() => (
          <Text color={w.rail}>▌</Text>
        ))}
      </Box>
      {mark ? (
        <Box marginRight={2} flexShrink={0}>
          {mark}
        </Box>
      ) : null}
      <Box flexDirection="column" flexGrow={1} flexShrink={1} minWidth={0}>
        {lines}
      </Box>
    </Box>
  )
}
