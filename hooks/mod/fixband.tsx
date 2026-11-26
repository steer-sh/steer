// The band's guard and check states: the offer to sleep after a block, the sleeping
// countdown and the short check-recovered line, and the status lines every band draws
// under its full state. A Sleep or Wake press runs the steer CLI, which writes the
// sleep; the mod only reads it back.

import type { EngineInterface, RenderChildren, RenderElement, RenderInput, TextProps } from 'claude-code'

import { passedText, timeLeft } from './band'
import type { StatusLine } from './band'
import { repoKey, repoName, ruleParts } from './blocks'
import { textPart, vectorCard } from './card'
import { BLOCK_ROWS, BOX_COLUMNS, COLORS, MARK_MIN_COLUMNS, SHIELD_COLUMNS, crossCells, shieldCells } from './marks'
import { plural } from './review'
import { sessionRecords } from './rows'
import { SLEEP_MINUTES, fixViews, parseSleep } from './sleep'
import type { FixView, SleepRecord } from './sleep'
import { MARK_HEIGHT, MARK_SMALL_HEIGHT, MARK_SMALL_WIDTH, MARK_WIDTH, markSvg, stateClock, textColor } from './vector'
import type { Mark, Motion, Tone } from './vector'

// The session the state below belongs to.
let owner = ''
let sleeps: SleepRecord[] = []
// Block rows a Dismiss closed, and the time of the last prompt, which closes every block before it.
let closed = new Set<string>()
let closedAt = -Infinity
// The targets whose Sleep or Wake is running, and what the last failed one said.
let pending = new Set<string>()
let failures = new Map<string, string>()

function own(session: string) {
  if (owner === session) return
  owner = session
  sleeps = []
  closed = new Set()
  closedAt = -Infinity
  pending = new Set()
  failures = new Map()
}

/**
 * Reads the session's sleep records. A sleep whose record is gone, as a wake removes
 * it, is kept as ended at `now`, so the blocks it answered stay answered.
 */
export async function pollSleeps($: EngineInterface, session: string, now: number): Promise<void> {
  own(session)
  const home = await $.env.get('HOME')
  if (!session || !home) return
  const dir = `${home}/.steer/state/sleep/${await repoKey(session)}`
  const read: SleepRecord[] = []
  for (const entry of await $.fs.list(dir).catch(() => [])) {
    if (entry.kind !== 'file' || entry.isLink || !entry.name.endsWith('.json')) continue
    const record = parseSleep(await $.fs.read(`${dir}/${entry.name}`).catch(() => ''))
    if (record?.session === session) read.push(record)
  }
  const target = (r: SleepRecord) => `${r.kind}:${r.target}`
  const kept = new Map(read.map(r => [target(r), r]))
  for (const old of sleeps) if (!kept.has(target(old))) kept.set(target(old), { ...old, untilMs: Math.min(old.untilMs, now) })
  sleeps = [...kept.values()]
}

/** The guard and check states the band can show now. */
export function currentFixes(session: string, now: number): FixView[] {
  own(session)
  return fixViews({ ...sessionRecords(), sleeps, closed, closedAt, session, now })
}

/** Closes every offer for a block recorded up to `now`: the user sent the next prompt. */
export function closeOffers(now: number) {
  closedAt = Math.max(closedAt, now)
}

/** What of the states changes the band: a poll redraws only on a change. */
export function fixKey(views: readonly FixView[], now: number, clocks: boolean): string {
  return views
    .map(v => {
      const extra =
        v.kind === 'sleeping'
          ? `${timeLeft(v.untilMs, now, clocks)}:${v.passed}`
          : v.kind === 'offer'
            ? `${v.count}:${v.type === 'check' ? `${v.failed.join(',')}:${v.allFailed}` : ''}`
            : ''
      return [v.kind, v.key, extra, pending.has(v.key), failures.get(v.key) ?? ''].join(':')
    })
    .join('|')
}

/** The steer the user's hooks run, else this plugin's own launcher. */
async function steerBin($: EngineInterface): Promise<string> {
  const home = await $.env.get('HOME')
  const stable = home ? `${home}/.steer/bin/steer` : ''
  if (stable && (await $.fs.exists(stable))) return stable
  return `${$.plugin.root}/scripts/steer`
}

/** Runs `steer <args>` for a press on `key`'s state, then reads the sleeps back. */
async function runSteer($: EngineInterface, key: string, args: string[], after: () => void) {
  if (pending.has(key)) return
  const session = owner
  pending.add(key)
  failures.delete(key)
  $.ui.invalidate('ui.render')
  try {
    const result = await $.process.run([await steerBin($), ...args], { timeoutMs: 15_000 })
    if (result.exitCode === 0) after()
    else failures.set(key, errorLine(result.stderr) || firstLine(result.stdout) || `steer exited ${result.exitCode}`)
  } catch (err) {
    failures.set(key, firstLine(String(err)))
  } finally {
    pending.delete(key)
  }
  if (owner === session) await pollSleeps($, session, await $.clock.now()).catch(() => undefined)
  $.ui.invalidate('ui.render')
}

const firstLine = (text: string) => (text.split('\n').find(l => l.trim() !== '') ?? '').trim()

/** The message of the error box steer prints on stderr: its first line of text, without the border or "Error: ". */
const errorLine = (text: string) =>
  firstLine(
    text
      .replace(/\x1b\[[0-9;]*m/g, '')
      .split('\n')
      .map(l => l.replace(/[\u2500-\u257f]/g, '').trim())
      .join('\n'),
  ).replace(/^Error: /, '')

const targetArgs = (v: FixView) => {
  if (v.kind === 'offer') return v.type === 'guard' ? ['guard', v.hook, ...v.repos.flatMap(r => ['--repo', r])] : ['check', '--repo', v.repo]
  if (v.kind === 'sleeping') return v.type === 'guard' ? ['guard', v.target] : ['check', '--repo', v.target]
  return []
}

function sleepNow($: EngineInterface, v: FixView & { kind: 'offer' }) {
  void runSteer($, v.key, ['sleep', ...targetArgs(v), '--session', owner, '--minutes', String(SLEEP_MINUTES)], () => {
    for (const id of v.rowIds) closed.add(id)
  })
}

function wakeNow($: EngineInterface, v: FixView & { kind: 'sleeping' }) {
  void runSteer($, v.key, ['wake', ...targetArgs(v), '--session', owner], () => undefined)
}

function dismiss($: EngineInterface, v: FixView & { kind: 'offer' }) {
  for (const id of v.rowIds) closed.add(id)
  failures.delete(v.key)
  $.ui.invalidate('ui.render')
}

const hueOf = (type: 'guard' | 'check') => (type === 'guard' ? COLORS.guard : COLORS.check)

/** The noun for what a guard stopped, by the tool it stopped. */
const noun = (tool: string) => (tool === 'Bash' ? 'command' : 'call')

/** The hue of a status line's state. */
const toneOf = (line: StatusLine): Tone =>
  line.kind === 'more'
    ? 'dim'
    : line.type === 'guard' || line.type === 'check'
      ? line.type
      : line.kind === 'held'
        ? 'held'
        : line.kind === 'running'
          ? 'review'
          : 'dim'

/** Rails and rows for the band's status lines, each rail in its state's hue; `desktop` for a surface that draws `Svg`. */
export function statusRowsOf(
  els: ReturnType<EngineInterface['ui']['resolve']>,
  status: readonly StatusLine[],
  desktop: boolean,
): { rows: RenderChildren[]; rails: string[] } {
  const { Box, Text } = els
  const rails: string[] = []
  const rows = status.map(line => {
    const hue = desktop ? textColor(toneOf(line)) : COLORS[toneOf(line)]
    rails.push(hue)
    const text = line.kind === 'more' ? line.text : `${line.who}: ${line.state}${line.tail ? ` · ${line.tail}` : ''}`
    if (desktop)
      return (
        <Box flexDirection="row" minWidth={0}>
          <Box flexShrink={0}>
            <Text color={hue}>▌ </Text>
          </Box>
          <Box flexShrink={1} minWidth={0}>
            <Text dimColor wrap="truncate">
              {text}
            </Text>
          </Box>
        </Box>
      )
    if (line.kind === 'more') return <Text dimColor>{line.text}</Text>
    return (
      <Text wrap="truncate-end">
        <Text dimColor>{line.who}: </Text>
        <Text color={hue}>{line.state}</Text>
        <Text dimColor>{line.tail ? ` · ${line.tail}` : ''}</Text>
      </Text>
    )
  })
  return { rows, rails }
}

/** What a Desktop card draws for a state: its hue, how its ground moves, its mark, whether the mark moves, and the mark's alt. */
function vectorOf(view: FixView): { tone: Tone; motion: Motion; mark: Mark; moves: boolean; small: boolean; alt: string } {
  if (view.kind === 'offer')
    return view.type === 'guard'
      ? { tone: 'guard', motion: 'once', mark: 'shield', moves: true, small: false, alt: 'guard stopped a call' }
      : { tone: 'check', motion: 'once', mark: 'cross', moves: true, small: false, alt: 'check kept the turn open' }
  if (view.kind === 'sleeping')
    return {
      tone: 'dim',
      motion: 'still',
      mark: view.type === 'guard' ? 'shield' : 'box',
      moves: false,
      small: true,
      alt: view.type === 'guard' ? 'guard sleeps' : 'check.sh sleeps',
    }
  return { tone: 'pass', motion: 'still', mark: 'tick', moves: true, small: true, alt: 'check.sh passes' }
}

/** When the state the card shows first drew: a new block of the same target starts its motion again. */
const fixClock = stateClock()
const fixId = (view: FixView) => `${view.kind}:${view.key}:${view.kind === 'sleeping' ? view.startedMs : view.atMs}`

/** Draws a guard or check state in full, with the status lines under it. */
export function drawFix(
  $: EngineInterface,
  e: RenderInput<'AbovePrompt'>,
  view: FixView,
  status: readonly StatusLine[],
  now: number,
  clocks: boolean,
): RenderElement {
  const els = $.ui.resolve(e)
  const { Box, Text, Button } = els
  const columns = typeof e.props.bodyColumns === 'number' ? e.props.bodyColumns : 0
  const Raster = e.surface === 'terminal' && 'Raster' in els && columns >= MARK_MIN_COLUMNS ? els.Raster : undefined
  const Svg = e.surface === 'desktop' && 'Svg' in els ? els.Svg : undefined
  const desktop = Svg !== undefined
  const hue = desktop ? textColor(view.type) : hueOf(view.type)
  const heldHue = desktop ? textColor('held') : COLORS.held
  const passHue = desktop ? textColor('pass') : COLORS.pass
  const label = view.type === 'guard' ? 'GUARD' : 'CHECK'
  const busy = pending.has(view.key)
  const failed = failures.get(view.key)
  let rail: string = hue
  let mark: RenderChildren = null
  let lines: RenderChildren[]
  const part = (text: string, props: TextProps = {}, shrink = 1) => textPart({ Box, Text }, text, props, shrink)
  const parts = (...items: RenderChildren[]) => (
    <Box flexDirection="row" flexShrink={1} minWidth={0}>
      {items}
    </Box>
  )

  if (view.kind === 'offer') {
    if (Raster)
      mark =
        view.type === 'guard' ? (
          <Raster key="offer-mark" columns={SHIELD_COLUMNS} rows={BLOCK_ROWS} cells={shieldCells()} />
        ) : (
          <Raster key="offer-mark" columns={BOX_COLUMNS} rows={BLOCK_ROWS} cells={crossCells()} />
        )
    const what =
      view.type === 'guard'
        ? view.count > 1
          ? `stopped ${plural(view.count, noun(view.tool))} this turn`
          : `stopped a ${noun(view.tool)}`
        : view.count > 1
          ? `kept the turn open ${view.count} times`
          : 'kept the turn open'
    const name = view.type === 'guard' ? view.hook : `check.sh · ${repoName(view.repo)}`
    let rule: string
    if (view.type === 'guard') {
      const { never, instead } = ruleParts(view.rule)
      rule = never && instead ? `${never} → ${instead}` : never || 'Steer stopped this call.'
    } else {
      const shown = view.failed.slice(0, 2)
      const more = view.failed.length - shown.length
      rule =
        view.failed.length > 0
          ? `${plural(view.failed.length, 'check')} ${view.allFailed ? 'failed' : 'did not pass'}: ${[...shown, ...(more > 0 ? [`${more} more`] : [])].join(' · ')}`
          : `check.sh ${view.outcome || 'failed'}`
    }
    const why =
      view.type === 'guard'
        ? 'Sleep lets this guard pass commands in this session.'
        : 'Sleep lets turns here end without check.sh in this session.'
    lines = [
      <Box flexDirection="row" justifyContent="space-between" columnGap={2}>
        {desktop ? (
          parts(part(label, { color: hue, weight: 'bold' }, 0), part(` · ${what}`, { color: hue, weight: 'bold' }))
        ) : (
          <Box flexShrink={1} minWidth={0}>
            <Text wrap="truncate-end">
              <Text color={hue} weight="bold">
                {label}
              </Text>
              <Text color={hue} weight="bold">
                {' '}
                · {what}
              </Text>
            </Text>
          </Box>
        )}
        <Box flexShrink={3} minWidth={0}>
          <Text dimColor wrap="truncate">
            {name}
          </Text>
        </Box>
      </Box>,
      <Text wrap="truncate-end">{rule}</Text>,
      <Box flexDirection="row" justifyContent="space-between" columnGap={2}>
        <Box flexShrink={1} minWidth={0}>
          {failed ? (
            <Text color={heldHue} wrap="truncate-end">
              Sleep did not start: {failed}
            </Text>
          ) : (
            <Text dimColor wrap="truncate-end">
              {busy ? 'Putting it to sleep…' : why}
            </Text>
          )}
        </Box>
        <Box flexDirection="row" columnGap={1} flexShrink={0}>
          <Button key="steer-sleep" variant="primary" onPress={() => sleepNow($, view)}>
            {`Sleep ${SLEEP_MINUTES} min`}
          </Button>
          <Button key="steer-dismiss" role="dismiss" onPress={() => dismiss($, view)}>
            Dismiss
          </Button>
        </Box>
      </Box>,
    ]
  } else if (view.kind === 'sleeping') {
    rail = COLORS.dim
    const what = view.type === 'guard' ? `${view.target} sleeps` : 'check.sh sleeps'
    const left = ` · ${what} · ${timeLeft(view.untilMs, now, clocks)} · ${passedText(view)}`
    lines = [
      <Box flexDirection="row" justifyContent="space-between" columnGap={2}>
        {desktop ? (
          parts(
            part(label, { color: hue, weight: 'bold' }, 0),
            part(left),
            failed ? part(` · Wake did not run: ${failed}`, { color: heldHue }) : '',
          )
        ) : (
          <Box flexShrink={1} minWidth={0}>
            <Text wrap="truncate-end">
              <Text color={hue} weight="bold">
                {label}
              </Text>
              <Text>
                {' '}
                · {what} · {timeLeft(view.untilMs, now, clocks)} · {passedText(view)}
              </Text>
              {failed ? <Text color={COLORS.held}> · Wake did not run: {failed}</Text> : ''}
            </Text>
          </Box>
        )}
        <Box flexShrink={0}>
          <Button key="steer-wake" onPress={() => wakeNow($, view)}>
            Wake
          </Button>
        </Box>
      </Box>,
    ]
  } else {
    rail = COLORS.pass
    lines = [
      desktop ? (
        parts(part(label, { color: hue, weight: 'bold' }, 0), part(' · check.sh passes now · ', {}, 0), part('all checks passed', { color: passHue }))
      ) : (
        <Text wrap="truncate-end">
          <Text color={hue} weight="bold">
            {label}
          </Text>
          <Text> · check.sh passes now · </Text>
          <Text color={COLORS.pass}>all checks passed</Text>
        </Text>
      ),
    ]
  }

  const { rows, rails } = statusRowsOf(els, status, desktop)
  if (Svg) {
    const v = vectorOf(view)
    const t = fixClock(fixId(view), now)
    const drawn =
      columns >= MARK_MIN_COLUMNS
        ? {
            source: markSvg(v.mark, v.moves, t, v.small),
            alt: v.alt,
            width: v.small ? MARK_SMALL_WIDTH : MARK_WIDTH,
            height: v.small ? MARK_SMALL_HEIGHT : MARK_HEIGHT,
          }
        : undefined
    return vectorCard({ Box, Svg }, { key: 'steer-fix', tone: v.tone, motion: v.motion, t, rows: lines.length, mark: drawn, lines, status: rows })
  }
  return (
    <Box key="steer-fix" flexDirection="row">
      <Box flexDirection="column" marginRight={1} flexShrink={0}>
        {lines.map(() => (
          <Text color={rail}>▌</Text>
        ))}
        {rails.map(hue => (
          <Text color={hue}>▌</Text>
        ))}
      </Box>
      {mark ? (
        <Box marginRight={2} flexShrink={0} alignSelf={rows.length > 0 ? 'flex-start' : undefined}>
          {mark}
        </Box>
      ) : null}
      <Box flexDirection="column" flexGrow={1} flexShrink={1} minWidth={0}>
        {lines}
        {rows}
      </Box>
    </Box>
  )
}
