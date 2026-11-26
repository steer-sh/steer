import { expect, mock, test } from 'claude-code/testing'
import type { Engine } from 'claude-code/testing'
import type { On } from 'claude-code'

import { parseGuardRow, repoKey, rowsOf, ruleParts, splitLines, tailAt } from './blocks'
import type { CheckExecution, CheckRun, GuardBlock } from './blocks'

const HOME = '/home/u'
const SESSION = 'sess-1'
const REPO = '/repo'
// sha256("/repo"), first 8 bytes: the directory cli/internal/checks names for REPO.
const REPO_KEY = '816fc349d3faebf8'
const AUDIT = `${HOME}/.steer/hooks/steer-audit.jsonl`
const CHECKS = `${HOME}/.steer/state/checks`
const RUNS = `${CHECKS}/${REPO_KEY}/runs.jsonl`
const EXECUTIONS = `${CHECKS}/${REPO_KEY}/executions.jsonl`
const NOW = 1_700_000_000_000

const iso = (ms: number) => new Date(ms).toISOString()
const bytes = (text: string) => new TextEncoder().encode(text)

const guardRow = (over: Record<string, unknown> = {}) => ({
  timestamp: iso(NOW + 500),
  rule: 'never commit on main in this repository | work on a branch',
  hook_name: 'no-direct-main-hook',
  enforcement: 'block',
  session_id: SESSION,
  thread_id: '',
  repository: REPO,
  tool_name: 'Bash',
  firing_id: 'f1',
  evidence: { payload: { prompt_id: 'p1' } },
  ...over,
})

const stopRun = (over: Record<string, unknown> = {}) => ({
  version: 2,
  run_id: 'r1',
  repo: REPO,
  source: 'stop',
  session: SESSION,
  finished: iso(NOW + 500),
  outcome: 'failed',
  disposition: 'block',
  ...over,
})

const block = (over: Partial<GuardBlock> = {}): GuardBlock => ({
  firingId: 'f1',
  hook: 'h',
  rule: '',
  atMs: 1,
  session: SESSION,
  thread: '',
  repo: REPO,
  tool: 'Bash',
  promptId: 'p1',
  ...over,
})

const run = (over: Partial<CheckRun> = {}): CheckRun => ({
  runId: 'r1',
  repo: REPO,
  session: SESSION,
  finishedMs: 2,
  outcome: 'failed',
  disposition: 'block',
  ...over,
})

test('a read keeps the bytes after the last newline for the next one', () => {
  const first = splitLines(tailAt(0, 1024), bytes('{"a":1}\n{"b":'))
  expect(first.lines).toEqual(['{"a":1}'])
  const second = splitLines(first.tail, bytes('2}\n'))
  expect(second.lines).toEqual(['{"b":2}'])
  expect(second.tail.offset).toBe(16)
})

test('a first read from inside a file drops the partial line, even when it spans reads', () => {
  const tail = tailAt(5000, 1000)
  expect(tail.offset).toBe(4000)
  const first = splitLines(tail, bytes('end of an old row'))
  expect(first.lines).toEqual([])
  const second = splitLines(first.tail, bytes('"}\n{"new":1}\n'))
  expect(second.lines).toEqual(['{"new":1}'])
})

test('the check state key is the one cli/internal/checks derives from the repository path', async () => {
  expect(await repoKey(REPO)).toBe(REPO_KEY)
})

test('a rule splits into what it forbids and what to do instead', () => {
  expect(ruleParts('never push to main | open a pull request')).toEqual({ never: 'never push to main', instead: 'open a pull request' })
  expect(ruleParts('never push to main')).toEqual({ never: 'never push to main', instead: '' })
})

test('rows follow the session in time order: a guard block, a check block with its failed checks, then one pass', () => {
  const executions: CheckExecution[] = [
    { runId: 'r1', session: SESSION, checkId: 'go-test', outcome: 'failed' },
    { runId: 'r1', session: SESSION, checkId: 'lint', outcome: 'passed' },
    { runId: 'r1', session: SESSION, checkId: 'cached', outcome: 'passed-cached' },
    { runId: 'r1', session: SESSION, checkId: 'skipped-one', outcome: 'skipped' },
  ]
  const rows = rowsOf(
    [block({ atMs: 1 }), block({ firingId: 'other', session: 'sess-2' })],
    [run({ finishedMs: 2 }), run({ runId: 'r2', finishedMs: 3, outcome: 'passed', disposition: 'allow' }), run({ runId: 'r3', finishedMs: 4, outcome: 'passed', disposition: 'allow' })],
    executions,
    SESSION,
  )
  expect(rows.map(r => r.id)).toEqual(['guard-f1', 'check-r1', 'check-pass-r2'])
  const failed = rows[1]
  expect(failed?.kind === 'check-failed' ? failed.failed : []).toEqual(['go-test', 'skipped-one'])
})

test('a passing run with no block before it gets no row', () => {
  expect(rowsOf([], [run({ disposition: 'allow', outcome: 'passed' })], [], SESSION)).toEqual([])
})

/** Answers the files, environment, clock and session the mod reads; `files` stays live, so a test appends to it. */
async function world($: Engine, on: On, files: Record<string, string>, placeFailures: ('throw' | 'unplaced')[] = []) {
  const clock = mock.clock(on, { now: NOW })
  mock.env(on, { HOME })
  files[`${HOME}/.steer/state/checks/.steer-state`] = ''
  files[`${REPO}/.git`] = ''
  const placed: string[] = []
  on('fs.exists', (_, e) => ({ value: e.path in files }))
  on('fs.list', (_, e) => {
    const names = new Set(
      Object.keys(files)
        .filter(p => p.startsWith(`${e.path}/`))
        .map(p => p.slice(e.path.length + 1).split('/')[0] ?? ''),
    )
    return { value: [...names].map(name => ({ name, kind: 'dir' as const, size: 0, mtimeMs: NOW, isLink: false })) }
  })
  on('fs.stat', (_, e) => {
    const found = files[e.path]
    if (found === undefined && e.path !== REPO) throw new Error('missing')
    return { value: { kind: e.path === REPO ? ('dir' as const) : ('file' as const), size: bytes(found ?? '').length, mtimeMs: NOW, isLink: false, realPath: e.path } }
  })
  on('fs.read', (_, e) => {
    const found = files[e.path]
    if (found === undefined) throw new Error('missing')
    const all = bytes(found)
    const offset = e.offset ?? 0
    const part = all.subarray(offset, offset + (e.length ?? all.length))
    return { value: { base64: (part as Uint8Array & { toBase64(): string }).toBase64() } }
  })
  on('session.id', () => ({ value: SESSION }))
  on('session.cwd', () => ({ value: `${REPO}/sub` }))
  on('session.start', (_, e) => ({ cwd: e.cwd }))
  on('ui.blit', () => ({ value: {} }))
  const failures = [...placeFailures]
  on('ui.place', (_, e) => {
    const failure = failures.shift()
    if (failure === 'throw') throw new Error('no surface')
    if (failure === 'unplaced') return { value: { isPlaced: false as const, reason: 'no surface' } }
    placed.push(e.id)
    return { value: { isPlaced: true as const, requestId: `row-${e.id}` } }
  })
  on('ui.render', ($, e) => {
    const { Text } = $.ui.resolve(e)
    return <Text>engine</Text>
  })
  await $.session.start({ cwd: REPO, surface: 'terminal', isInteractive: true })
  await clock.advance(1000)
  return { clock, placed }
}

const line = (doc: unknown) => `${JSON.stringify(doc)}\n`

const texts = async (ui: { findAll(q: { type: string }): Promise<{ text: string }[]> }) =>
  (await ui.findAll({ type: 'Text' })).map(t => t.text).join('\n')

const row = (id: string, surface: 'terminal' | 'desktop') =>
  ({ plugin: 'steer', component: 'Placed', requestId: `row-${id}`, surface, props: { id, plugin: 'steer' } }) as const

test('a guard block after the mod starts gets a row with the rule and a shield; an earlier one gets none', async ($, on) => {
  const files: Record<string, string> = { [AUDIT]: line(guardRow({ firing_id: 'old', timestamp: iso(NOW - 60_000) })) }
  const { clock, placed } = await world($, on, files)
  files[AUDIT] += line(guardRow({ thread_id: 'agent-7' }))
  await clock.advance(1000)
  expect(placed).toEqual(['guard-f1'])

  const terminal = await $.ui.mount(row('guard-f1', 'terminal'))
  const drawn = await texts(terminal)
  expect(drawn).toContain('GUARD')
  expect(drawn).toContain('before a tool runs')
  expect(drawn).toContain('no-direct-main-hook')
  expect(drawn).toContain('Steer stopped a Bash call. Rule: never commit on main in this repository.')
  expect(drawn).toContain('The subagent should work on a branch.')
  expect((await terminal.findAll({ type: 'Raster' })).map(r => r.key)).toEqual(['mark'])
  await terminal.unmount()

  const desktop = await $.ui.mount(row('guard-f1', 'desktop'))
  expect(await desktop.findAll({ type: 'Raster' })).toHaveLength(0)
  await desktop.unmount()
})

test('a check Stop block names the failed check, and the next passing run closes it', async ($, on) => {
  const files: Record<string, string> = { [RUNS]: '', [EXECUTIONS]: '' }
  const { clock, placed } = await world($, on, files)
  files[EXECUTIONS] += line({ run_id: 'r1', session: SESSION, check_id: 'go-test', outcome: 'failed' })
  files[RUNS] += line(stopRun())
  await clock.advance(1000)
  // A run read in the same poll as its executions names them, whichever file grew last.
  const first = await $.ui.mount(row('check-r1', 'terminal'))
  expect(await texts(first)).toContain('go-test did not pass')
  await first.unmount()
  files[RUNS] += line(stopRun({ run_id: 'r2', finished: iso(NOW + 9_000), outcome: 'passed', disposition: 'allow' }))
  await clock.advance(1000)
  expect(placed).toEqual(['check-r1', 'check-pass-r2'])

  const failed = await texts(await $.ui.mount(row('check-r1', 'terminal')))
  expect(failed).toContain('CHECK')
  expect(failed).toContain('when the turn ends')
  expect(failed).toContain('check.sh · repo')
  expect(failed).toContain('Steer kept the turn open: go-test did not pass.')
  const passed = await texts(await $.ui.mount(row('check-pass-r2', 'terminal')))
  expect(passed).toContain('check.sh passes again.')
})

test('a check block in a repository the session reached later gets a row, and so does an integration failure', async ($, on) => {
  const other = `${CHECKS}/0123456789abcdef/runs.jsonl`
  const files: Record<string, string> = {}
  const { clock, placed } = await world($, on, files)
  files[other] = line(stopRun({ repo: '/other', outcome: 'passed' }))
  await clock.advance(10_000)
  expect(placed).toEqual(['check-r1'])
  const drawn = await texts(await $.ui.mount(row('check-r1', 'terminal')))
  expect(drawn).toContain('Steer kept the turn open: a declared check left no result.')
})

test('a row whose placement failed, or that no surface drew, is placed at a later poll', async ($, on) => {
  const files: Record<string, string> = { [AUDIT]: '' }
  const { clock, placed } = await world($, on, files, ['throw', 'unplaced'])
  files[AUDIT] += line(guardRow())
  await clock.advance(1000)
  await clock.advance(1000)
  expect(placed).toEqual([])
  await clock.advance(1000)
  expect(placed).toEqual(['guard-f1'])
})

test('a block row from a master that writes no firing ID is still a guard block', () => {
  const { firing_id: _, ...old } = guardRow()
  expect(parseGuardRow(JSON.stringify(old))?.firingId).toBe(`${iso(NOW + 500)}-no-direct-main-hook`)
})

test('a check row placed before its executions were read names the failed check once they are', async ($, on) => {
  const files: Record<string, string> = { [RUNS]: '' }
  const { clock, placed } = await world($, on, files)
  files[RUNS] += line(stopRun())
  await clock.advance(1000)
  expect(placed).toEqual(['check-r1'])
  files[EXECUTIONS] = line({ run_id: 'r1', session: SESSION, check_id: 'go-test', outcome: 'failed' })
  await clock.advance(1000)
  expect(await texts(await $.ui.mount(row('check-r1', 'terminal')))).toContain('go-test did not pass')
})
