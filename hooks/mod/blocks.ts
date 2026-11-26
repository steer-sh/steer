// Reads the guard blocks and check Stop blocks Steer already records, and decides the
// transcript rows the mod places for them. Guard rows come from each guard store's
// steer-audit.jsonl (cli/internal/guard/runtime.py, steer_audit_log); check rows from
// a repository's runs.jsonl and executions.jsonl (cli/internal/checks/run.go, execution.go).

/** One guard block, as its audit row records it. */
export type GuardBlock = {
  firingId: string
  hook: string
  rule: string
  atMs: number
  session: string
  thread: string
  repo: string
  tool: string
  /** The user prompt the blocked call belongs to: one turn. */
  promptId: string
}

/** One check.sh run the Stop runner made, as runs.jsonl records it. */
export type CheckRun = {
  runId: string
  repo: string
  session: string
  finishedMs: number
  outcome: string
  disposition: string
}

/** One declared check's result inside a run, as executions.jsonl records it. */
export type CheckExecution = { runId: string; session: string; checkId: string; outcome: string }

/**
 * A transcript row the mod places, keyed by `id`. A check row carries `n`, the run's
 * place among this session's check.sh runs in its repository, and `place`, the
 * repository's name when the session's runs name more than one.
 */
export type BlockRow =
  | { kind: 'guard'; id: string; block: GuardBlock }
  | { kind: 'check-failed'; id: string; run: CheckRun; failed: string[]; allFailed: boolean; n: number; place: string }
  | { kind: 'check-passed'; id: string; run: CheckRun; n: number; place: string }
  | { kind: 'check-unfixed'; id: string; run: CheckRun; n: number; place: string }

const isString = (v: unknown): v is string => typeof v === 'string'

// The execution outcomes that pass, as Execution.Passed reads them (cli/internal/checks/execution.go).
const PASSES = new Set(['passed', 'passed-cached'])
// The outcomes that fail, as Execution.Failed reads them; skipped and not-executed are neither.
const FAILS = new Set(['failed', 'error', 'timeout', 'unavailable'])

function record(line: string): Record<string, unknown> | null {
  try {
    const raw: unknown = JSON.parse(line)
    return raw && typeof raw === 'object' && !Array.isArray(raw) ? (raw as Record<string, unknown>) : null
  } catch {
    return null
  }
}

const str = (d: Record<string, unknown>, key: string) => (isString(d[key]) ? (d[key] as string) : '')

const timeOf = (v: unknown) => {
  const ms = isString(v) ? Date.parse(v) : NaN
  return Number.isFinite(ms) ? ms : 0
}

/** One call a sleeping guard let through, as its audit row records it. */
export type SleptCall = { hook: string; session: string; atMs: number }

/** Parses one audit row; null for a row that is not a sleeping guard's pass. */
export function parseSleptRow(line: string): SleptCall | null {
  const d = record(line)
  if (!d || d.enforcement !== 'slept') return null
  const hook = str(d, 'hook_name')
  return hook ? { hook, session: str(d, 'session_id'), atMs: timeOf(d.timestamp) } : null
}

/** Parses one audit row; null for a row that is not a guard block. */
export function parseGuardRow(line: string): GuardBlock | null {
  const d = record(line)
  if (!d || d.enforcement !== 'block') return null
  const hook = str(d, 'hook_name')
  if (!hook) return null
  // A master older than firing identifiers writes none: its row is named by its time and guard.
  const firingId = str(d, 'firing_id') || `${str(d, 'timestamp')}-${hook}`
  const evidence = d.evidence && typeof d.evidence === 'object' ? (d.evidence as Record<string, unknown>) : {}
  const payload = evidence.payload && typeof evidence.payload === 'object' ? (evidence.payload as Record<string, unknown>) : {}
  return {
    firingId,
    hook,
    rule: str(d, 'rule'),
    atMs: timeOf(d.timestamp),
    session: str(d, 'session_id'),
    thread: str(d, 'thread_id'),
    repo: str(d, 'repository'),
    tool: str(d, 'tool_name'),
    promptId: str(payload, 'prompt_id'),
  }
}

/** Parses one runs.jsonl row; null for a row that is not a Stop run. */
export function parseCheckRun(line: string): CheckRun | null {
  const d = record(line)
  if (!d || d.source !== 'stop') return null
  const runId = str(d, 'run_id')
  if (!runId) return null
  return {
    runId,
    repo: str(d, 'repo'),
    session: str(d, 'session'),
    finishedMs: timeOf(d.finished),
    outcome: str(d, 'outcome'),
    disposition: str(d, 'disposition'),
  }
}

/** Parses one executions.jsonl row; null for a row that belongs to no run. */
export function parseExecution(line: string): CheckExecution | null {
  const d = record(line)
  if (!d) return null
  const runId = str(d, 'run_id')
  const checkId = str(d, 'check_id')
  if (!runId || !checkId) return null
  return { runId, session: str(d, 'session'), checkId, outcome: str(d, 'outcome') }
}

/** A run that kept the turn open. */
const isStopBlock = (run: CheckRun) => run.disposition === 'block'

/** A run that let the turn end on a passing check.sh. */
const isStopPass = (run: CheckRun) => run.disposition === 'allow' && run.outcome === 'passed'

/** A run that ran check.sh: not refused by the repository lock and not skipped while it slept. */
const ranOracle = (run: CheckRun) => run.outcome !== 'not-executed'

/** The last part of a repository path. */
export const repoName = (repo: string) => repo.split(/[\\/]/).filter(Boolean).pop() ?? ''

/**
 * The rows a session's records call for, in the order they happened. A guard row per
 * block; a check row per Stop block, naming the declared checks that did not pass
 * (any outcome but a pass, as the runner reads it) and whether each of them failed; and
 * after a block in the same repository, one unfixed row for a yield on the same
 * failure, and one pass row for the next passing run, also after a yield. Another run
 * that lets the turn end (a timeout, an error, a sleep) ends the block with no row.
 */
export function rowsOf(
  guards: readonly GuardBlock[],
  runs: readonly CheckRun[],
  executions: readonly CheckExecution[],
  session: string,
): BlockRow[] {
  const rows: { at: number; row: BlockRow }[] = []
  for (const block of guards) {
    if (block.session === session) rows.push({ at: block.atMs, row: { kind: 'guard', id: `guard-${block.firingId}`, block } })
  }
  const failedIn = new Map<string, string[]>()
  const notFailing = new Set<string>()
  for (const ex of executions) {
    if (PASSES.has(ex.outcome)) continue
    if (!FAILS.has(ex.outcome)) notFailing.add(ex.runId)
    const names = failedIn.get(ex.runId) ?? []
    if (!names.includes(ex.checkId)) names.push(ex.checkId)
    failedIn.set(ex.runId, names)
  }
  // Each repository's story since its last block: blocked, or ended with check.sh not passing.
  const story = new Map<string, 'blocked' | 'ended'>()
  const mine = [...runs].filter(run => run.session === session).sort((a, b) => a.finishedMs - b.finishedMs)
  const several = new Set(mine.map(run => run.repo)).size > 1
  const counted = new Map<string, number>()
  for (const run of mine) {
    if (!ranOracle(run)) {
      // A run that did not execute check.sh (a sleep, a concurrent run) still ends the turn.
      if (story.get(run.repo) === 'blocked') story.set(run.repo, 'ended')
      continue
    }
    const n = (counted.get(run.repo) ?? 0) + 1
    counted.set(run.repo, n)
    const place = several ? repoName(run.repo) : ''
    const state = story.get(run.repo)
    if (isStopBlock(run)) {
      story.set(run.repo, 'blocked')
      const failed = failedIn.get(run.runId) ?? []
      const allFailed = !notFailing.has(run.runId)
      rows.push({ at: run.finishedMs, row: { kind: 'check-failed', id: `check-${run.runId}`, run, failed, allFailed, n, place } })
    } else if (isStopPass(run)) {
      if (state) rows.push({ at: run.finishedMs, row: { kind: 'check-passed', id: `check-pass-${run.runId}`, run, n, place } })
      story.delete(run.repo)
    } else if (state === 'blocked') {
      if (run.disposition === 'yield')
        rows.push({ at: run.finishedMs, row: { kind: 'check-unfixed', id: `check-unfixed-${run.runId}`, run, n, place } })
      story.set(run.repo, 'ended')
    }
  }
  return rows.sort((a, b) => a.at - b.at).map(r => r.row)
}

/** A rule's two parts: what it forbids, and what to do instead; either may be empty. */
export function ruleParts(rule: string): { never: string; instead: string } {
  const [head = '', ...rest] = rule.split('|')
  return { never: trimEnd(head), instead: trimEnd(rest.join('|')) }
}

const trimEnd = (text: string) => text.trim().replace(/[.;:]+$/, '')

/**
 * What the agent does next, from a rule's instead-part: "use python3 instead" reads
 * "continues with python3"; any other part is quoted after "continues:".
 */
export function nextStep(instead: string, noun = 'call'): string {
  if (!instead) return `continues without that ${noun}`
  const use = /^use\s+(.+?)(\s+instead)?$/i.exec(instead)
  if (use?.[1]) return `continues with ${use[1]}`
  return `continues: ${instead}`
}

/**
 * Where the next read of a file starts, the bytes after its last newline so far, and
 * whether the bytes before the next newline are the end of a line read from its middle.
 */
export type Tail = { offset: number; carry: Uint8Array; skip: boolean }

/** How a file seen for the first time, or one now shorter than read, is read: from at most `back` bytes before its end. */
export function tailAt(size: number, back: number): Tail {
  const offset = Math.max(0, size - back)
  return { offset, carry: new Uint8Array(0), skip: offset > 0 }
}

/** Splits newly read bytes into whole lines; bytes after the last newline wait in `carry`. */
export function splitLines(tail: Tail, added: Uint8Array): { lines: string[]; tail: Tail } {
  const bytes = new Uint8Array(tail.carry.length + added.length)
  bytes.set(tail.carry)
  bytes.set(added, tail.carry.length)
  const end = bytes.lastIndexOf(0x0a)
  const offset = tail.offset + added.length
  if (end < 0) return { lines: [], tail: { offset, carry: bytes, skip: tail.skip } }
  let lines = new TextDecoder().decode(bytes.subarray(0, end)).split('\n')
  if (tail.skip) lines = lines.slice(1)
  return { lines: lines.filter(l => l.trim() !== ''), tail: { offset, carry: bytes.slice(end + 1), skip: false } }
}

/**
 * The directory name under the check state that holds a repository's records (repoKey in
 * cli/internal/checks/receipts.go), and the name of a session's sleep directory and a
 * target's sleep record (Key in cli/internal/sleep).
 */
export async function repoKey(identity: string): Promise<string> {
  const sum = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(identity)))
  return Array.from(sum.subarray(0, 8), b => b.toString(16).padStart(2, '0')).join('')
}
