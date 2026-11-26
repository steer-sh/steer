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

/** A transcript row the mod places, keyed by `id`. */
export type BlockRow =
  | { kind: 'guard'; id: string; block: GuardBlock }
  | { kind: 'check-failed'; id: string; run: CheckRun; failed: string[] }
  | { kind: 'check-passed'; id: string; run: CheckRun }

const isString = (v: unknown): v is string => typeof v === 'string'

// The execution outcomes that pass, as Execution.Passed reads them (cli/internal/checks/execution.go).
const PASSES = new Set(['passed', 'passed-cached'])

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

/**
 * The rows a session's records call for, in the order they happened. A guard row per
 * block; a check row per Stop block, naming the declared checks that did not pass
 * (any outcome but a pass, as the runner reads it); and
 * one pass row for the first passing run after a block in the same repository.
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
  for (const ex of executions) {
    if (PASSES.has(ex.outcome)) continue
    const names = failedIn.get(ex.runId) ?? []
    if (!names.includes(ex.checkId)) names.push(ex.checkId)
    failedIn.set(ex.runId, names)
  }
  const blockedRepos = new Set<string>()
  for (const run of [...runs].sort((a, b) => a.finishedMs - b.finishedMs)) {
    if (run.session !== session) continue
    if (isStopBlock(run)) {
      blockedRepos.add(run.repo)
      rows.push({ at: run.finishedMs, row: { kind: 'check-failed', id: `check-${run.runId}`, run, failed: failedIn.get(run.runId) ?? [] } })
    } else if (isStopPass(run) && blockedRepos.delete(run.repo)) {
      rows.push({ at: run.finishedMs, row: { kind: 'check-passed', id: `check-pass-${run.runId}`, run } })
    }
  }
  return rows.sort((a, b) => a.at - b.at).map(r => r.row)
}

/** A rule's two parts: what it forbids, and what to do instead; either may be empty. */
export function ruleParts(rule: string): { never: string; instead: string } {
  const [head = '', ...rest] = rule.split('|')
  return { never: head.trim(), instead: rest.join('|').trim() }
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

/** The directory name under the check state that holds a repository's records (repoKey in cli/internal/checks/receipts.go). */
export async function repoKey(identity: string): Promise<string> {
  const sum = new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(identity)))
  return Array.from(sum.subarray(0, 8), b => b.toString(16).padStart(2, '0')).join('')
}
