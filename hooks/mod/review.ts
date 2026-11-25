// Reads the live review file the commit gate writes (cli/internal/gate/live.go) and
// decides what of it a band may show.

export type Severity = 'BLOCKING' | 'SUBSTANTIVE' | 'NITPICK'

export type LiveFinding = { severity: Severity; file: string; claim: string }

export type LiveReview = {
  session_id: string
  /** The subagent thread the commit came from; empty on the main thread. */
  thread_id: string
  /** The repository directory the commit ran in. */
  repo: string
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
    thread_id: isString(d.thread_id) ? d.thread_id : '',
    repo: isString(d.repo) ? d.repo : '',
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

/** Every view a session's files show at `now`, the band's own first: running reviews by newest start, then ended ones by newest end. */
export function sessionViews(docs: readonly LiveReview[], sessionId: string, now: number): ReviewView[] {
  const views: ReviewView[] = []
  for (const doc of docs) {
    if (doc.session_id !== sessionId) continue
    const view = viewOf(doc, now)
    if (view) views.push(view)
  }
  return views.sort((a, b) => rank(b) - rank(a))
}

const rank = (v: ReviewView) => (v.kind === 'running' ? 1e15 + v.doc.started_ms : v.doc.ended_ms)

/** Status lines the band gives to the reviews it hides. */
export const STATUS_LINES = 2

/** The reviews behind the band's own that get a status line: held ones first, then running ones, each oldest first. */
export function hiddenViews(views: readonly ReviewView[]): ReviewView[] {
  const order = (v: ReviewView) => (v.kind === 'held' ? 0 : 1)
  return views
    .slice(1)
    .filter(v => v.kind === 'held' || v.kind === 'running')
    .sort((a, b) => order(a) - order(b) || a.doc.started_ms - b.doc.started_ms)
}

/** Gives each subagent thread a number the first time one of its reviews is seen, in the order they started. */
export function numberThreads(views: readonly ReviewView[], numbers: Map<string, number>): void {
  for (const view of [...views].sort((a, b) => a.doc.started_ms - b.doc.started_ms)) {
    const id = view.doc.thread_id
    if (id && !numbers.has(id)) numbers.set(id, numbers.size + 1)
  }
}

/** The last part of a repository path: a worktree's own folder name. */
const repoName = (repo: string) => repo.split(/[\\/]/).filter(Boolean).pop() ?? ''

/**
 * Who made the commit `view` reviews, as "subagent · wt-auth" or "main thread · steer";
 * empty for the main thread while it is the only review. A subagent carries its number
 * only while another subagent's review names the same repository.
 */
export function whoOf(view: ReviewView, views: readonly ReviewView[], numbers: ReadonlyMap<string, number>): string {
  const { thread_id: thread, repo } = view.doc
  if (!thread && views.length < 2) return ''
  const place = repoName(repo)
  const twin =
    thread !== '' && views.some(o => o.doc.thread_id !== '' && o.doc.thread_id !== thread && repoName(o.doc.repo) === place)
  const n = twin ? numbers.get(thread) : undefined
  const agent = thread ? `subagent${n ? ` ${n}` : ''}` : 'main thread'
  return place ? `${agent} · ${place}` : agent
}

/** The agent that acts on the review's result, as a sentence's subject. */
export function actorOf(who: string, doc: LiveReview): string {
  if (!doc.thread_id) return 'Claude'
  const agent = who.split(' · ')[0] ?? ''
  return agent === 'subagent' || agent === '' ? 'The subagent' : capital(agent)
}

const capital = (text: string) => text.charAt(0).toUpperCase() + text.slice(1)

/** One line for a hidden review, or the count of the ones past the last line. */
export type StatusLine =
  | { kind: 'held' | 'running'; who: string; state: string; tail: string }
  | { kind: 'more'; text: string }

/**
 * The band's status lines for `hidden`: one each up to STATUS_LINES, else one each
 * before the last, which counts the rest. `clocks` adds a running review's elapsed time.
 */
export function statusLines(hidden: readonly ReviewView[], name: (v: ReviewView) => string, clocks: boolean): StatusLine[] {
  const line = (v: ReviewView): StatusLine =>
    v.kind === 'held'
      ? { kind: 'held', who: capital(name(v)), state: 'commit held', tail: severityCounts(v.doc.findings) }
      : { kind: 'running', who: capital(name(v)), state: 'commit in review', tail: clocks ? clock(v.elapsedMs) : '' }
  if (hidden.length <= STATUS_LINES) return hidden.map(line)
  const named = hidden.slice(0, STATUS_LINES - 1)
  const rest = hidden.slice(STATUS_LINES - 1)
  const held = rest.filter(v => v.kind === 'held').length
  const parts = [held ? `${plural(held, 'commit')} held` : '', rest.length - held ? `${rest.length - held} in review` : '']
  return [...named.map(line), { kind: 'more', text: `+${rest.length} more: ${parts.filter(Boolean).join(', ')}` }]
}

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
