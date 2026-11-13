// Reads the live review file the commit gate writes (cli/internal/gate/live.go) and
// decides what of it a band may show.

export type Severity = 'BLOCKING' | 'SUBSTANTIVE' | 'NITPICK'

export type LiveFinding = { severity: Severity; file: string; claim: string }

export type LiveReview = {
  session_id: string
  round: number
  max_rounds: number
  final: boolean
  reviewer: string
  model: string
  runs: number
  /** The 1-based positions of the runs that returned, with how each ended. */
  runs_ended: { run: number; outcome: string }[]
  state: 'running' | 'ended'
  started_ms: number
  beat_ms: number
  beat_every_ms: number
  review_timeout_s: number
  ended_ms: number
  outcome: string
  answered: number
  findings: LiveFinding[]
}

/** What the band shows for a review, or nothing. */
export type ReviewView =
  | { kind: 'running'; doc: LiveReview; elapsedMs: number }
  | { kind: 'held'; doc: LiveReview; elapsedMs: number }
  | { kind: 'handed'; doc: LiveReview; elapsedMs: number }
  | { kind: 'passed'; doc: LiveReview; elapsedMs: number }
  | { kind: 'unreviewed'; doc: LiveReview; elapsedMs: number }

const LIVE_VERSION = 1

// A running file is stale once its writer has missed this many beats.
const MISSED_BEATS = 5
// The gate's hook outlives the review timeout by this margin at most.
const HOOK_MARGIN_MS = 120_000

/** How long each ended state stays on the band. */
export const SHOW_MS = {
  held: 10 * 60_000,
  handed: 20_000,
  passed: 6_000,
  unreviewed: 8_000,
} as const

const SEVERITIES: readonly Severity[] = ['BLOCKING', 'SUBSTANTIVE', 'NITPICK']

const isNumber = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)
const isString = (v: unknown): v is string => typeof v === 'string'

/** The session id as the gate spells it in a directory name. */
export const sessionSlug = (id: string) => id.replace(/[^A-Za-z0-9_-]/g, '').slice(0, 64)

/** Parses one live review file; null for anything that is not a version it knows. */
export function parseLive(text: string): LiveReview | null {
  let raw: unknown
  try {
    raw = JSON.parse(text)
  } catch {
    return null
  }
  if (!raw || typeof raw !== 'object') return null
  const d = raw as Record<string, unknown>
  if (d.version !== LIVE_VERSION) return null
  if (d.state !== 'running' && d.state !== 'ended') return null
  if (!isString(d.session_id) || !isString(d.reviewer)) return null
  for (const key of ['round', 'max_rounds', 'runs', 'started_ms', 'beat_ms', 'beat_every_ms', 'review_timeout_s']) {
    if (!isNumber(d[key])) return null
  }
  const findings: LiveFinding[] = []
  for (const f of Array.isArray(d.findings) ? d.findings : []) {
    if (!f || typeof f !== 'object') continue
    const { severity, file, claim } = f as Record<string, unknown>
    if (!SEVERITIES.includes(severity as Severity) || !isString(file) || !isString(claim)) continue
    findings.push({ severity: severity as Severity, file, claim })
  }
  const runsEnded: LiveReview['runs_ended'] = []
  for (const r of Array.isArray(d.runs_ended) ? d.runs_ended : []) {
    if (!r || typeof r !== 'object') continue
    const { run, outcome } = r as Record<string, unknown>
    if (isNumber(run) && isString(outcome)) runsEnded.push({ run, outcome })
  }
  return {
    session_id: d.session_id,
    round: d.round as number,
    max_rounds: d.max_rounds as number,
    final: d.final === true,
    reviewer: d.reviewer,
    model: isString(d.model) ? d.model : '',
    runs: d.runs as number,
    runs_ended: runsEnded,
    state: d.state,
    started_ms: d.started_ms as number,
    beat_ms: d.beat_ms as number,
    beat_every_ms: d.beat_every_ms as number,
    review_timeout_s: d.review_timeout_s as number,
    ended_ms: isNumber(d.ended_ms) ? d.ended_ms : 0,
    outcome: isString(d.outcome) ? d.outcome : '',
    answered: isNumber(d.answered) ? d.answered : 0,
    findings,
  }
}

/** What one file shows at `now`; null for a stale file and for an ended one past its time. */
export function viewOf(doc: LiveReview, now: number): ReviewView | null {
  if (doc.state === 'running') {
    const beatLimit = Math.max(doc.beat_every_ms, 1000) * MISSED_BEATS
    const runLimit = doc.review_timeout_s * 1000 + HOOK_MARGIN_MS
    if (now - doc.beat_ms > beatLimit || now - doc.started_ms > runLimit) return null
    return { kind: 'running', doc, elapsedMs: Math.max(0, now - doc.started_ms) }
  }
  if (!doc.ended_ms) return null
  const kind = endedKind(doc)
  if (!kind || now - doc.ended_ms > SHOW_MS[kind]) return null
  return { kind, doc, elapsedMs: Math.max(0, doc.ended_ms - doc.started_ms) }
}

function endedKind(doc: LiveReview): 'held' | 'handed' | 'passed' | 'unreviewed' | null {
  switch (doc.outcome) {
    case 'deny':
      return 'held'
    case 'clean':
      return 'passed'
    case 'nitpick':
      return 'handed'
    case 'ungated':
      return 'unreviewed'
    case 'cap':
      if (doc.answered === 0) return 'unreviewed'
      return doc.findings.length > 0 ? 'handed' : 'passed'
    default:
      return null
  }
}

/** The one view to show among a session's files: a running review first, then the newest ended one. */
export function pickView(docs: readonly LiveReview[], sessionId: string, now: number): ReviewView | null {
  let best: ReviewView | null = null
  for (const doc of docs) {
    if (doc.session_id !== sessionId) continue
    const view = viewOf(doc, now)
    if (!view) continue
    if (!best || rank(view) > rank(best)) best = view
  }
  return best
}

const rank = (v: ReviewView) => (v.kind === 'running' ? 1e15 + v.doc.started_ms : v.doc.ended_ms)

/** Findings ordered BLOCKING, SUBSTANTIVE, NITPICK, each level in the order written. */
export const bySeverity = (findings: readonly LiveFinding[]) =>
  SEVERITIES.flatMap(level => findings.filter(f => f.severity === level))

/** "2 blocking, 1 nitpick": the count of each level present. */
export function severityCounts(findings: readonly LiveFinding[]): string {
  return SEVERITIES.map(level => [level, findings.filter(f => f.severity === level).length] as const)
    .filter(([, n]) => n > 0)
    .map(([level, n]) => `${n} ${level.toLowerCase()}`)
    .join(', ')
}

/** Elapsed time as m:ss. */
export function clock(ms: number): string {
  const s = Math.floor(ms / 1000)
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

export const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`
