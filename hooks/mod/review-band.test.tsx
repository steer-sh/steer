import { expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'
import type { On } from 'claude-code'

const HOME = '/home/u'
const SESSION = 'sess-1'
const LIVE = `${HOME}/.steer/state/commit-review/live`
const NOW = 1_700_000_000_000

const BAND = {
  plugin: 'steer',
  component: 'AbovePrompt',
  requestId: 'band',
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

const running = (over: Record<string, unknown> = {}) => ({
  version: 1,
  session_id: SESSION,
  repo: '/repo',
  round: 2,
  max_rounds: 4,
  reviewer: 'stub-provider',
  model: 'stub-model',
  runs: 3,
  runs_ended: [{ run: 2, outcome: 'ok', elapsed_ms: 900 }],
  state: 'running',
  started_ms: NOW - 102_000,
  beat_ms: NOW - 1_000,
  beat_every_ms: 2000,
  review_timeout_s: 300,
  pid: 1,
  ...over,
})

const ended = (outcome: string, over: Record<string, unknown> = {}) =>
  running({ state: 'ended', ended_ms: NOW - 1_000, outcome, answered: 3, runs_ended: [], ...over })

const FINDINGS = [
  { severity: 'NITPICK', file: 'c.ts:3', claim: 'a name is unclear' },
  { severity: 'SUBSTANTIVE', file: 'export.ts:12', claim: 'the CSV writer drops the header' },
  { severity: 'BLOCKING', file: 'routes.ts:41', claim: 'the export route has no requireAuth' },
]

/** Answers the file system, environment, clock and session the mod reads, and starts the session. */
async function world($: Engine, on: On, files: Record<string, unknown>, session = SESSION) {
  const clock = mock.clock(on, { now: NOW })
  mock.env(on, { HOME })
  const text: Record<string, string> = { [`${HOME}/.steer/state/commit-review/.steer-state`]: '' }
  for (const [path, doc] of Object.entries(files)) text[path] = typeof doc === 'string' ? doc : JSON.stringify(doc)
  on('fs.exists', (_, e) => ({ value: e.path in text }))
  on('fs.list', (_, e) => ({
    value: Object.keys(text)
      .filter(p => p.startsWith(`${e.path}/`) && !p.slice(e.path.length + 1).includes('/'))
      .map(p => ({ name: p.slice(e.path.length + 1), kind: 'file' as const, size: 1, mtimeMs: NOW, isLink: false })),
  }))
  on('fs.read', (_, e) => {
    const found = text[e.path]
    if (found === undefined) throw new Error('missing')
    return { value: found }
  })
  on('session.id', () => ({ value: session }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('ui.blit', () => ({ value: {} }))
  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
  await $.session.start({ cwd: '/', surface: 'terminal', isInteractive: true })
  await clock.advance(1000)
  return clock
}

const texts = async (ui: { findAll(q: { type: string }): Promise<{ text: string }[]> }) =>
  (await ui.findAll({ type: 'Text' })).map(t => t.text).join('\n')

test('a running review shows its round, reviewer, elapsed time and runs', async ($, on) => {
  await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: running() })

  const terminal = await $.ui.mount({ ...BAND, surface: 'terminal' })
  const drawn = await texts(terminal)
  expect(drawn).toContain('REVIEW')
  expect(drawn).toContain('round 2 of 4 · stub-provider · stub-model · 1:43')
  expect(drawn).toContain('Steer holds the commit while 3 runs read the diff.')
  expect(drawn).toContain('run 1 ')
  expect(drawn).toContain(' done')
  expect(drawn).toContain('Claude waits for the result.')
  const rasters = await terminal.findAll({ type: 'Raster' })
  expect(rasters.map(r => r.key)).toEqual(['book', 'run-1', 'run-2', 'run-3'])
  await terminal.unmount()

  for (const surface of ['desktop', 'vscode'] as const) {
    const ui = await $.ui.mount({ ...BAND, surface })
    const words = await texts(ui)
    expect(words).toContain('round 2 of 4 · stub-provider · stub-model')
    expect(words).toContain('run 2 done')
    expect(words).toContain('run 3 reading')
    expect(await ui.findAll({ type: 'Raster' })).toHaveLength(0)
    await ui.unmount()
  }
})

test('a narrow terminal leaves the marks out', async ($, on) => {
  await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: running() })
  const ui = await $.ui.mount({ ...BAND, props: { ...BAND.props, bodyColumns: 60 }, surface: 'terminal' })
  expect(await ui.findAll({ type: 'Raster' })).toHaveLength(0)
  expect(await texts(ui)).toContain('round 2 of 4 · stub-provider · 1:43')
  await ui.unmount()
})

test('the final review says it does not hold the commit', async ($, on) => {
  await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: running({ final: true, round: 5 }) })
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  const drawn = await texts(ui)
  expect(drawn).toContain('final review · stub-provider')
  expect(drawn).toContain('This review does not hold the commit.')
  await ui.unmount()
})

test('a held commit lists its findings by severity and counts the rest', async ($, on) => {
  await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: ended('deny', { findings: FINDINGS }) })
  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await $.ui.mount({ ...BAND, surface })
    const drawn = await texts(ui)
    expect(drawn).toContain('commit held · 3 findings: 1 blocking, 1 substantive, 1 nitpick')
    expect(drawn).toContain('routes.ts:41  the export route has no requireAuth')
    expect(drawn).toContain('export.ts:12  the CSV writer drops the header')
    expect(drawn.indexOf('routes.ts:41')).toBeLessThan(drawn.indexOf('export.ts:12'))
    expect(drawn).not.toContain('c.ts:3')
    expect(drawn).toContain('…1 more finding. Claude now fixes or rejects each finding.')
    expect((await ui.findAll({ type: 'Raster' })).map(r => r.key)).toEqual(surface === 'terminal' ? ['book'] : [])
    expect(await ui.findAll({ type: 'Button' })).toHaveLength(0)
    await ui.unmount()
  }
})

test('a passed commit shows for a few seconds, then the band leaves', async ($, on) => {
  const clock = await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: ended('clean') })
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  const drawn = await texts(ui)
  expect(drawn).toContain('commit passed')
  expect(drawn).toContain('3 runs, no findings')
  expect(await ui.findAll({ type: 'Raster' })).toHaveLength(0)
  await ui.unmount()

  await clock.advance(10_000)
  const later = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await texts(later)).toBe('engine')
  await later.unmount()
})

test('a round no reviewer answered says the commit was not reviewed', async ($, on) => {
  await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: ended('ungated', { answered: 0 }) })
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await texts(ui)).toContain('commit not reviewed')
  await ui.unmount()
})

test('findings handed over without a hold say the commit is not held', async ($, on) => {
  await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: ended('nitpick', { findings: FINDINGS.slice(0, 1) }) })
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  const drawn = await texts(ui)
  expect(drawn).toContain('commit not held · 1 finding: 1 nitpick')
  expect(drawn).toContain('Claude now fixes or rejects each finding.')
  await ui.unmount()
})

test('nothing draws for another session, a stale file or an unreadable one', async ($, on) => {
  await world($, on, {
    // Another session's directory, and another session's file in this directory.
    [`${LIVE}/other/a-1.json`]: running({ session_id: 'other' }),
    [`${LIVE}/${SESSION}/a-1.json`]: running({ session_id: 'other' }),
    // A gate that died: its last beat is long past.
    [`${LIVE}/${SESSION}/a-2.json`]: running({ beat_ms: NOW - 60_000 }),
    // A review past its timeout, whatever its beat says.
    [`${LIVE}/${SESSION}/a-3.json`]: running({ started_ms: NOW - 3_600_000 }),
    [`${LIVE}/${SESSION}/a-4.json`]: ended('clean', { ended_ms: NOW - 60_000 }),
    [`${LIVE}/${SESSION}/a-5.json`]: '{"version":1,',
    [`${LIVE}/${SESSION}/a-6.json`]: running({ version: 2 }),
    [`${LIVE}/${SESSION}/.live-1.tmp`]: running(),
  })
  for (const surface of ['terminal', 'desktop'] as const) {
    const ui = await $.ui.mount({ ...BAND, surface })
    expect(await texts(ui)).toBe('engine')
    await ui.unmount()
  }
})

test('a session with no review draws nothing', async ($, on) => {
  await world($, on, {})
  const ui = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await texts(ui)).toBe('engine')
  await ui.unmount()
})
