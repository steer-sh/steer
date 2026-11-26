import { expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'
import type { On } from 'claude-code'

import { compose, statusLines } from './band'
import { repoKey } from './blocks'
import type { ReviewView } from './review'
import type { FixView } from './sleep'

const HOME = '/home/u'
const SESSION = 'sess-1'
const REPO = '/repo'
const AUDIT = `${HOME}/.steer/hooks/steer-audit.jsonl`
const CHECKS = `${HOME}/.steer/state/checks`
const SLEEP = `${HOME}/.steer/state/sleep`
const LIVE = `${HOME}/.steer/state/commit-review/live`
const STEER = `${HOME}/.steer/bin/steer`
const NOW = 1_700_000_000_000

const iso = (ms: number) => new Date(ms).toISOString()
const bytes = (text: string) => new TextEncoder().encode(text)
const line = (doc: unknown) => `${JSON.stringify(doc)}\n`

const BAND = {
  plugin: 'steer',
  component: 'AbovePrompt',
  requestId: 'band',
  surface: 'terminal',
  props: {
    hasSurvey: false,
    isCollapsed: false,
    isWorking: true,
    maxRows: 20,
    bodyColumns: 120,
    scroll: { offset: 0, bodyRows: 20 },
    view: {},
  },
} as const

const guardRow = (over: Record<string, unknown> = {}) => ({
  timestamp: iso(NOW + 1_500),
  rule: 'never commit on main in this repository | work on a branch',
  hook_name: 'no-direct-main-hook',
  enforcement: 'block',
  session_id: SESSION,
  thread_id: '',
  repository: REPO,
  tool_name: 'Bash',
  firing_id: 'f1',
  ...over,
})

const stopRun = (over: Record<string, unknown> = {}) => ({
  version: 2,
  run_id: 'r1',
  repo: REPO,
  source: 'stop',
  session: SESSION,
  finished: iso(NOW + 1_500),
  outcome: 'failed',
  disposition: 'block',
  ...over,
})

/**
 * Answers the files, environment, clock, session and processes the mod reads and runs.
 * `files` stays live, so a test appends to it; a steer sleep or wake writes or removes
 * the record as the CLI does.
 */
async function world($: Engine, on: On, files: Record<string, string>) {
  const clock = mock.clock(on, { now: NOW })
  mock.env(on, { HOME })
  files[`${CHECKS}/.steer-state`] = ''
  files[`${HOME}/.steer/state/commit-review/.steer-state`] = ''
  files[`${REPO}/.git`] = ''
  files[STEER] = ''
  const runs: string[][] = []
  on('fs.exists', (_, e) => ({ value: e.path in files }))
  on('fs.list', (_, e) => {
    const names = new Set(
      Object.keys(files)
        .filter(p => p.startsWith(`${e.path}/`))
        .map(p => p.slice(e.path.length + 1).split('/')[0] ?? ''),
    )
    return {
      value: [...names].map(name => ({
        name,
        kind: `${e.path}/${name}` in files ? ('file' as const) : ('dir' as const),
        size: 0,
        mtimeMs: NOW,
        isLink: false,
      })),
    }
  })
  on('fs.stat', (_, e) => {
    const found = files[e.path]
    if (found === undefined && e.path !== REPO) throw new Error('missing')
    return { value: { kind: e.path === REPO ? ('dir' as const) : ('file' as const), size: bytes(found ?? '').length, mtimeMs: NOW, isLink: false, realPath: e.path } }
  })
  on('fs.read', (_, e) => {
    const found = files[e.path]
    if (found === undefined) throw new Error('missing')
    if (e.as !== 'bytes') return { value: found }
    const all = bytes(found)
    const offset = e.offset ?? 0
    const part = all.subarray(offset, offset + (e.length ?? all.length))
    return { value: { base64: (part as Uint8Array & { toBase64(): string }).toBase64() } }
  })
  on('process.run', async (_, e) => {
    const argv = [...e.argv]
    runs.push(argv)
    const [, verb, kind] = argv
    const at = (flag: string) => argv[argv.indexOf(flag) + 1] ?? ''
    const target = kind === 'guard' ? (argv[3] ?? '') : at('--repo')
    const path = `${SLEEP}/${await repoKey(at('--session'))}/${kind}-${await repoKey(target)}.json`
    const now = await clock.now()
    if (verb === 'sleep')
      files[path] = JSON.stringify({
        version: 1,
        kind,
        target,
        session: at('--session'),
        by: 'user',
        started_ms: now,
        until_ms: now + Number(at('--minutes')) * 60_000,
      })
    else delete files[path]
    return { value: { exitCode: 0, stdout: '', stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('session.id', () => ({ value: SESSION }))
  on('session.cwd', () => ({ value: REPO }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('turn.start', (_, e) => ({ turnId: e.turnId }))
  on('ui.blit', () => ({ value: {} }))
  on('ui.place', (_, e) => ({ value: { isPlaced: true as const, requestId: `row-${e.id}` } }))
  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
  await $.session.start({ cwd: REPO, surface: 'terminal', isInteractive: true })
  await clock.advance(1000)
  return { clock, runs }
}

type Mounted = {
  findAll(q: { type: string }): Promise<{ text: string; key?: string }[]>
  press(t: { key: string }): Promise<unknown>
  unmount(): Promise<void>
}

/** The band's text, drawn once and taken down. */
async function textOnce($: Engine): Promise<string> {
  const { ui, text } = await drawn($)
  await ui.unmount()
  return text
}

/** Draws the band once and returns its text, its buttons and its moving marks. */
async function drawn($: Engine, surface: 'terminal' | 'desktop' = 'terminal') {
  const ui = (await $.ui.mount({ ...BAND, surface })) as unknown as Mounted
  const text = (await ui.findAll({ type: 'Text' })).map(t => t.text).join('\n')
  const buttons = (await ui.findAll({ type: 'Button' })).map(b => b.key)
  const marks = (await ui.findAll({ type: 'Raster' })).map(r => r.key)
  return { ui, text, buttons, marks }
}

test('a guard block opens an offer; Sleep sleeps that guard for the session, the band counts what it let through, Wake ends it', async ($, on) => {
  const files: Record<string, string> = { [AUDIT]: '' }
  const { clock, runs } = await world($, on, files)
  files[AUDIT] += line(guardRow())
  await clock.advance(1000)

  const offer = await drawn($)
  expect(offer.text).toContain('GUARD')
  expect(offer.text).toContain('stopped a command')
  expect(offer.text).toContain('no-direct-main-hook')
  expect(offer.text).toContain('never commit on main in this repository → work on a branch')
  expect(offer.text).toContain('Sleep lets this guard pass commands in this session.')
  expect(offer.buttons).toEqual(['steer-sleep', 'steer-dismiss'])
  expect(offer.marks).toEqual(['offer-mark'])
  await offer.ui.press({ key: 'steer-sleep' })
  await clock.advance(1000)
  await offer.ui.unmount()
  expect(runs).toEqual([[STEER, 'sleep', 'guard', 'no-direct-main-hook', '--session', SESSION, '--minutes', '5']])

  files[AUDIT] += line(guardRow({ enforcement: 'slept', firing_id: 'f2', timestamp: iso(NOW + 4_000) }))
  await clock.advance(1000)
  const asleep = await drawn($)
  expect(asleep.text).toContain('no-direct-main-hook sleeps')
  expect(asleep.text).toContain('4:58 left')
  expect(asleep.text).toContain('1 command passed')
  expect(asleep.buttons).toEqual(['steer-wake'])
  await asleep.ui.press({ key: 'steer-wake' })
  await clock.advance(1000)
  await asleep.ui.unmount()
  expect(runs[1]).toEqual([STEER, 'wake', 'guard', 'no-direct-main-hook', '--session', SESSION])
  expect(await textOnce($)).toBe('engine')
})

test('Dismiss closes an offer, the next typed prompt closes a later one, and the self-protection guard gets none', async ($, on) => {
  const files: Record<string, string> = { [AUDIT]: '' }
  const { clock } = await world($, on, files)
  files[AUDIT] += line(guardRow({ hook_name: 'steer-self-protection', firing_id: 'self' }))
  await clock.advance(1000)
  expect(await textOnce($)).toBe('engine')

  files[AUDIT] += line(guardRow())
  await clock.advance(1000)
  const offer = await drawn($)
  await offer.ui.press({ key: 'steer-dismiss' })
  await offer.ui.unmount()
  expect(await textOnce($)).toBe('engine')

  files[AUDIT] += line(guardRow({ firing_id: 'f2' }))
  await clock.advance(1000)
  await $.turn.start({ text: '', turnId: 'continued' })
  const still = await drawn($)
  expect(still.text).toContain('stopped a command')
  await still.ui.unmount()
  await $.turn.start({ text: 'try again', turnId: 'next' })
  expect(await textOnce($)).toBe('engine')

  // A block recorded before the prompt but read after it is closed too.
  files[AUDIT] += line(guardRow({ firing_id: 'f3', timestamp: iso((await clock.now()) - 500) }))
  await $.turn.start({ text: 'and again', turnId: 'later' })
  await clock.advance(1000)
  expect(await textOnce($)).toBe('engine')

  // A sleep set from the CLI answers the open offer, which stays closed after its wake.
  files[AUDIT] += line(guardRow({ firing_id: 'f4', timestamp: iso((await clock.now()) + 100) }))
  await clock.advance(1000)
  expect(await textOnce($)).toContain('stopped a command')
  const record = `${SLEEP}/${await repoKey(SESSION)}/guard-${await repoKey('no-direct-main-hook')}.json`
  const at = await clock.now()
  files[record] = JSON.stringify({ version: 1, kind: 'guard', target: 'no-direct-main-hook', session: SESSION, by: 'user', started_ms: at, until_ms: at + 60_000 })
  await clock.advance(1000)
  expect(await textOnce($)).toContain('no-direct-main-hook sleeps')
  delete files[record]
  await clock.advance(1000)
  expect(await textOnce($)).toBe('engine')
})

test("a check block's offer sleeps check.sh for its repository, and the countdown counts the turns that ended", async ($, on) => {
  const key = await repoKey(REPO)
  const runsPath = `${CHECKS}/${key}/runs.jsonl`
  const executions = `${CHECKS}/${key}/executions.jsonl`
  const files: Record<string, string> = { [AUDIT]: '', [runsPath]: '', [executions]: '' }
  const { clock, runs } = await world($, on, files)
  files[executions] += line({ run_id: 'r1', session: SESSION, check_id: 'go-test', outcome: 'failed' })
  files[runsPath] += line(stopRun())
  await clock.advance(1000)

  const offer = await drawn($)
  expect(offer.text).toContain('CHECK')
  expect(offer.text).toContain('kept the turn open')
  expect(offer.text).toContain('1 check failed: go-test')
  expect(offer.text).toContain('check.sh · repo')
  await offer.ui.press({ key: 'steer-sleep' })
  await clock.advance(1000)
  await offer.ui.unmount()
  expect(runs).toEqual([[STEER, 'sleep', 'check', '--repo', REPO, '--session', SESSION, '--minutes', '5']])

  for (const id of ['s1', 's2'])
    files[runsPath] += line(stopRun({ run_id: id, outcome: 'not-executed', disposition: 'slept', finished: iso(NOW + 5_000) }))
  await clock.advance(1000)
  const asleep = await drawn($)
  expect(asleep.text).toContain('check.sh sleeps')
  expect(asleep.text).toContain('2 turns ended')
})

test('an offer takes the band from a running review, which shrinks to a status line and stops its book', async ($, on) => {
  const files: Record<string, string> = {
    [AUDIT]: '',
    [`${LIVE}/${SESSION}/main.json`]: JSON.stringify({
      version: 1,
      session_id: SESSION,
      repo: REPO,
      round: 1,
      max_rounds: 4,
      reviewer: 'stub',
      model: '',
      runs: 2,
      runs_ended: [],
      state: 'running',
      started_ms: NOW - 30_000,
      beat_ms: NOW + 2_000,
      beat_every_ms: 2000,
      review_timeout_s: 300,
    }),
  }
  const { clock } = await world($, on, files)
  const review = await drawn($)
  expect(review.marks).toContain('book')
  await review.ui.unmount()

  files[AUDIT] += line(guardRow())
  await clock.advance(1000)
  const both = await drawn($)
  expect(both.text).toContain('stopped a command')
  expect(both.text).toContain('commit in review')
  expect(both.marks).toEqual(['offer-mark'])
})

const doc = { started_ms: 1, ended_ms: 2, findings: [], thread_id: '', repo: '/r' } as unknown as ReviewView['doc']
const reviewOf = (kind: ReviewView['kind']): ReviewView => ({ kind, doc, elapsedMs: 0 }) as ReviewView
const offer: FixView = { kind: 'offer', type: 'guard', key: 'guard:g', hook: 'g', rule: '', tool: 'Bash', count: 1, rowIds: ['x'], atMs: 1 }
const sleeping: FixView = { kind: 'sleeping', type: 'guard', key: 'guard:h', target: 'h', startedMs: 0, untilMs: 60_000, passed: 0 }
const recovered: FixView = { kind: 'recovered', type: 'check', key: 'check:/r', repo: '/r', atMs: 1 }

test('the band shows the most urgent state in full: an offer, a running review, a held one, a countdown, then a short state', () => {
  const full = (views: ReviewView[], fixes: FixView[]) => {
    const shown = compose(views, fixes)
    return shown?.full.type === 'fix' ? shown.full.view.kind : shown?.full.views[0]?.kind
  }
  expect(full([reviewOf('running')], [offer, sleeping])).toBe('offer')
  expect(full([reviewOf('running')], [sleeping])).toBe('running')
  expect(full([reviewOf('held')], [sleeping])).toBe('held')
  expect(full([reviewOf('passed')], [sleeping])).toBe('sleeping')
  expect(full([reviewOf('passed')], [recovered])).toBe('passed')
  expect(full([], [recovered])).toBe('recovered')
  expect(compose([], [])).toBeNull()

  const shown = compose([reviewOf('passed')], [sleeping, offer, recovered])
  const lines = statusLines(shown?.hidden ?? [], () => '', true, 0)
  expect(lines.map(l => (l.kind === 'more' ? l.text : `${l.who}: ${l.state}`))).toEqual(['Review: commit passed', '+2 more'])
})

type Drawing = { alt: string; width: number; height: number; source: string }

/** The band's Svg elements, its card and its text, drawn once on desktop at `columns`. */
async function desktopBand($: Engine, columns = 120) {
  const ui = (await $.ui.mount({ ...BAND, surface: 'desktop', props: { ...BAND.props, bodyColumns: columns } })) as unknown as {
    findAll(q: { type: string }): Promise<{ text: string; key?: string; props: Record<string, unknown> }[]>
    unmount(): Promise<void>
  }
  const svgs = (await ui.findAll({ type: 'Svg' })).map(s => s.props as Drawing)
  const cards = (await ui.findAll({ type: 'Box' })).filter(b => b.props.borderStyle === 'round').map(b => b.props)
  const text = (await ui.findAll({ type: 'Text' })).map(t => t.text).join('\n')
  const buttons = (await ui.findAll({ type: 'Button' })).map(b => b.key)
  await ui.unmount()
  return { svgs, cards, text, buttons }
}

test('on desktop the offer and the countdown draw a card with the guard mark, its own shades and no rail glyph', async ($, on) => {
  const files: Record<string, string> = { [AUDIT]: '' }
  const { clock } = await world($, on, files)
  files[AUDIT] += line(guardRow())
  await clock.advance(1000)

  const offer = await desktopBand($)
  expect(offer.text).not.toContain('▌')
  expect(offer.text).toContain('stopped a command')
  expect(offer.buttons).toEqual(['steer-sleep', 'steer-dismiss'])
  expect(offer.cards).toHaveLength(1)
  expect(/^#[0-9a-f]{8}$/.test(String(offer.cards[0]?.backgroundColor))).toBe(true)
  // The wash, the sky, the rail and the shield, each with an alt and its dark shades.
  expect(offer.svgs).toHaveLength(4)
  for (const svg of offer.svgs) {
    expect(svg.alt).not.toBe('')
    expect(svg.source).toContain('@media (prefers-color-scheme:dark)')
  }
  const shield = offer.svgs[3] as Drawing
  expect(shield.alt).toBe('guard stopped a call')
  expect(shield.height).toBe(60)
  expect(shield.source).toContain('@keyframes as')
  // The sky's light crosses once for the block, not again and again.
  expect(offer.svgs[1]?.source).toContain('1 both')

  // A narrow band leaves the mark out.
  expect((await desktopBand($, 60)).svgs).toHaveLength(3)

  const at = await clock.now()
  const record = `${SLEEP}/${await repoKey(SESSION)}/guard-${await repoKey('no-direct-main-hook')}.json`
  files[record] = JSON.stringify({ version: 1, kind: 'guard', target: 'no-direct-main-hook', session: SESSION, by: 'user', started_ms: at, until_ms: at + 300_000 })
  await clock.advance(1000)
  const asleep = await desktopBand($)
  expect(asleep.text).toContain('no-direct-main-hook sleeps')
  expect(asleep.text).toContain('5 min left')
  expect(asleep.buttons).toEqual(['steer-wake'])
  const small = asleep.svgs[3] as Drawing
  expect(small.alt).toBe('guard sleeps')
  expect(small.height).toBe(18)
  expect(small.source).not.toContain('@keyframes')
  // A narrow countdown leaves its small mark out too.
  expect((await desktopBand($, 60)).svgs.map(d => d.alt)).toEqual(['steer', 'steer', 'steer'])
})
