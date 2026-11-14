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

type Drawing = { alt: string; width: number; height: number; source: string }
const MISSING: Drawing = { alt: '(missing)', width: 0, height: 0, source: '' }

/** The Svg elements a band drew, in order: its wash, its sky, its rail, its book, then a pulse per run. */
const svgs = async (ui: { findAll(q: { type: string }): Promise<{ props: Record<string, unknown> }[]> }) => {
  const all = (await ui.findAll({ type: 'Svg' })).map(s => s.props as Drawing)
  return { wash: all[0] ?? MISSING, sky: all[1] ?? MISSING, rail: all[2] ?? MISSING, book: all[3] ?? MISSING, pulses: all.slice(4), all }
}

test('the desktop band draws a card, a book and a pulse per run, with no rail glyph', async ($, on) => {
  await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: running({ runs_ended: [{ run: 2, outcome: 'ok' }, { run: 3, outcome: 'timeout' }] }) })
  const ui = await $.ui.mount({ ...BAND, surface: 'desktop' })
  const drawn = await texts(ui)
  expect(drawn).not.toContain('▌')
  expect(drawn).toContain('Steer holds the commit while 3 runs read the diff.')
  expect(drawn).toContain('run 3 no answer')

  const cards = (await ui.findAll({ type: 'Box' })).filter(b => b.props.borderStyle === 'round')
  expect(cards.map(c => /^#[0-9a-f]{8}$/.test(String(c.props.backgroundColor)))).toEqual([true])
  expect(cards.map(c => /^#[0-9a-f]{8}$/.test(String(c.props.borderColor)))).toEqual([true])

  const { wash, sky, rail, book, pulses, all } = await svgs(ui)
  expect(all).toHaveLength(7)
  for (const svg of all) expect(svg.alt).not.toBe('')
  for (const layer of [wash, sky, rail]) expect(layer.source).not.toContain('preserveAspectRatio')
  expect(wash.source).toContain('@keyframes ab')
  expect(sky.source).toContain('@keyframes ah')
  // Each drawing carries its own shades for a dark app.
  for (const svg of all) expect(svg.source).toContain('@media (prefers-color-scheme:dark)')
  expect(book.alt).toBe('review running')
  expect([book.width, book.height]).toEqual([96, 60])
  expect(book.source).toContain('@keyframes al')
  // A reading run's pulse moves; a returned run's bar is still, whether or not the run answered.
  expect(pulses.map(p => p.source.includes('@keyframes'))).toEqual([true, false, false])
  expect(pulses[1]?.source).toBe(pulses[2]?.source)
  for (const svg of all) expect(svg.source.length).toBeLessThan(131072)
  await ui.unmount()

  const vscode = await $.ui.mount({ ...BAND, surface: 'vscode' })
  expect(await vscode.findAll({ type: 'Svg' })).toHaveLength(0)
  await vscode.unmount()
})

test('the desktop running band shows no clock, and a redraw continues its art', async ($, on) => {
  const clock = await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: running({ beat_ms: NOW + 60_000 }) })
  const first = await $.ui.mount({ ...BAND, surface: 'desktop' })
  expect(await texts(first)).not.toMatch(/\d:\d\d/)
  expect((await svgs(first)).book.source).toContain('.ag{animation:ag 5s ease-in-out 0s infinite}')
  await first.unmount()

  await clock.advance(2000)
  const later = await $.ui.mount({ ...BAND, surface: 'desktop' })
  expect((await svgs(later)).book.source).toContain('.ag{animation:ag 5s ease-in-out -2s infinite}')
  await later.unmount()

  const terminal = await $.ui.mount({ ...BAND, surface: 'terminal' })
  expect(await texts(terminal)).toMatch(/\d:\d\d/)
  await terminal.unmount()
})

test('a narrow desktop band lengthens its rail for wrapped runs, and each header part truncates', async ($, on) => {
  await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: running({ runs_ended: [{ run: 2, outcome: 'ok' }, { run: 3, outcome: 'timeout' }] }) })
  const wide = await $.ui.mount({ ...BAND, surface: 'desktop' })
  const wideRail = (await svgs(wide)).rail.height
  await wide.unmount()
  // Runs of 17, 14 and 19 columns in 30 columns of room: no two fit in a row, so each takes its own.
  const narrow = await $.ui.mount({ ...BAND, props: { ...BAND.props, bodyColumns: 34 }, surface: 'desktop' })
  const { all, rail } = await svgs(narrow)
  expect(rail.height).toBe(wideRail + 2 * 20)
  // Below the terminal's mark width the card keeps its ground and rail but leaves the book out.
  expect(all.map(s => s.alt)).not.toContain('review running')
  await narrow.unmount()
})

test('each desktop header part of a passed commit is plain text that truncates', async ($, on) => {
  await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: ended('clean') })
  const ui = await $.ui.mount({ ...BAND, surface: 'desktop' })
  const parts = (await ui.findAll({ type: 'Text' })).filter(t => t.text.startsWith(' · ') || t.text.startsWith('round ')) as {
    text: string
    props: Record<string, unknown>
  }[]
  expect(parts.length).toBe(3)
  for (const part of parts) expect(part.props.wrap).toBe('truncate')
  await ui.unmount()
})

test('a desktop finding row keeps its claim in plain text that truncates', async ($, on) => {
  await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: ended('deny', { findings: FINDINGS }) })
  const ui = await $.ui.mount({ ...BAND, surface: 'desktop' })
  const claims = (await ui.findAll({ type: 'Text' })).filter(t => t.text.startsWith('routes.ts:41')) as {
    props: Record<string, unknown>
  }[]
  expect(claims.length).toBeGreaterThan(0)
  for (const claim of claims) expect(claim.props.wrap).toBe('truncate')
  await ui.unmount()
})

const ENDED_BOOKS = [
  ['deny', { findings: FINDINGS }, 'commit held', 96],
  ['nitpick', { findings: FINDINGS.slice(0, 1) }, 'commit not held', 96],
  ['clean', {}, 'commit passed', 29],
  ['ungated', { answered: 0 }, 'commit not reviewed', 29],
] as const
// Each ended book's drawing without its colours, to tell the drawings apart.
const endedShapes = new Set<string>()

for (const [outcome, over, alt, width] of ENDED_BOOKS)
  test(`the desktop band draws its own book for ${alt}`, async ($, on) => {
    await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: ended(outcome, over) })
    const ui = await $.ui.mount({ ...BAND, surface: 'desktop' })
    const { book, pulses, all } = await svgs(ui)
    for (const svg of all) expect(svg.alt).not.toBe('')
    expect(book.alt).toBe(alt)
    expect(book.width).toBe(width)
    expect(pulses).toHaveLength(0)
    const drawn = await texts(ui)
    expect(drawn).toContain(alt)
    expect(drawn).not.toContain('▌')
    const shape = book.source.replace(/#[0-9a-f]{6}/g, '')
    expect(endedShapes.has(shape)).toBe(false)
    endedShapes.add(shape)
    await ui.unmount()
  })

test('the desktop label takes a blue that reads on both grounds; the terminal label keeps its own', async ($, on) => {
  await world($, on, { [`${LIVE}/${SESSION}/a-1.json`]: running() })
  const ui = await $.ui.mount({ ...BAND, surface: 'desktop' })
  const label = (await ui.findAll({ type: 'Text' })).find(t => t.text === 'REVIEW') as { props: Record<string, unknown> } | undefined
  expect(label?.props.color).toBe('#5f7cf2')
  await ui.unmount()

  const terminal = await $.ui.mount({ ...BAND, surface: 'terminal' })
  const plain = (await terminal.findAll({ type: 'Text' })).find(t => t.text === 'REVIEW') as { props: Record<string, unknown> } | undefined
  expect(plain?.props.color).toBe('#9badff')
  await terminal.unmount()
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
