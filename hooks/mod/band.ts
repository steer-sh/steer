// Decides what Steer's one band shows when several states are live: the most urgent in
// full, and one status line under it for each other, two at most.

import { clock, hiddenViews, plural, severityCounts } from './review'
import type { ReviewView } from './review'
import type { FixView } from './sleep'

/** What the band shows in full: the session's reviews, drawn as the review band, or one guard or check state. */
export type Full = { type: 'review'; views: ReviewView[] } | { type: 'fix'; view: FixView }

/** A state the band lists under the full one. */
export type Hidden = { type: 'review'; view: ReviewView } | { type: 'fix'; view: FixView }

/**
 * How urgent a state is, lowest first: an offer to sleep, a running review, a held
 * review, a sleeping countdown, then the short states (a review that passed or was
 * handed back, a check that passes again), which take the band only alone.
 */
function urgency(full: Full): number {
  if (full.type === 'fix') return full.view.kind === 'offer' ? 0 : full.view.kind === 'sleeping' ? 3 : 4
  const kind = full.views[0]?.kind
  return kind === 'running' ? 1 : kind === 'held' ? 2 : 4
}

/**
 * The band's full state and the states it lists, or null when none is live. Reviews
 * keep their own order (sessionViews), so they count as one state, ranked by the one
 * the review band would draw. The listed states are offers, then reviews, then
 * countdowns, then the short states.
 */
export function compose(views: readonly ReviewView[], fixes: readonly FixView[]): { full: Full; hidden: Hidden[] } | null {
  // Reviews first, so a review keeps the band from a state as urgent as its own.
  const candidates: Full[] = views.length > 0 ? [{ type: 'review', views: [...views] }] : []
  for (const view of fixes) candidates.push({ type: 'fix', view })
  if (candidates.length === 0) return null
  const full = candidates.reduce((best, c) => (urgency(c) < urgency(best) ? c : best))
  const reviews: Hidden[] =
    full.type === 'review'
      ? hiddenViews(full.views).map(view => ({ type: 'review', view }))
      : views.length > 0
        ? [views[0] as ReviewView, ...hiddenViews(views)].map(view => ({ type: 'review', view }))
        : []
  const others = fixes.filter(v => full.type !== 'fix' || v !== full.view)
  const of = (kinds: FixView['kind'][]): Hidden[] => others.filter(v => kinds.includes(v.kind)).map(view => ({ type: 'fix', view }))
  return { full, hidden: [...of(['offer']), ...reviews, ...of(['sleeping']), ...of(['recovered'])] }
}

/** Status lines the band gives to the states it lists. */
export const STATUS_LINES = 2

/** One line for a listed state, or the count of the ones past the last line. */
export type StatusLine =
  | { kind: ReviewView['kind'] | FixView['kind']; type: 'review' | 'guard' | 'check'; who: string; state: string; tail: string }
  | { kind: 'more'; text: string }

const capital = (text: string) => text.charAt(0).toUpperCase() + text.slice(1)

/** The last part of a repository path. */
const repoName = (repo: string) => repo.split(/[\\/]/).filter(Boolean).pop() ?? ''

/** How long a sleep has left: m:ss with a ticking clock, else whole minutes. */
export function timeLeft(untilMs: number, now: number, clocks: boolean): string {
  const left = Math.max(0, untilMs - now)
  return clocks ? `${clock(left)} left` : `${Math.max(1, Math.ceil(left / 60_000))} min left`
}

/** What a countdown has let through: commands for a guard, turns for check.sh. */
export const passedText = (view: FixView & { kind: 'sleeping' }) =>
  view.type === 'guard' ? `${plural(view.passed, 'command')} passed` : `${plural(view.passed, 'turn')} ended`

function lineOf(h: Hidden, name: (v: ReviewView) => string, clocks: boolean, now: number): StatusLine {
  if (h.type === 'review') {
    const v = h.view
    const who = capital(name(v)) || 'Review'
    const tail = v.kind === 'held' || v.kind === 'handed' ? severityCounts(v.doc.findings) : v.kind === 'running' && clocks ? clock(v.elapsedMs) : ''
    const state = {
      running: 'commit in review',
      held: 'commit held',
      handed: 'commit not held',
      passed: 'commit passed',
      unreviewed: 'commit not reviewed',
    }[v.kind]
    return { kind: v.kind, type: 'review', who, state, tail }
  }
  const v = h.view
  const who = v.type === 'guard' ? 'Guard' : 'Check'
  if (v.kind === 'offer')
    return v.type === 'guard'
      ? { kind: 'offer', type: 'guard', who: `${who} ${v.hook}`, state: 'stopped a command', tail: '' }
      : { kind: 'offer', type: 'check', who: `${who} ${repoName(v.repo)}`, state: 'kept the turn open', tail: '' }
  if (v.kind === 'sleeping')
    return {
      kind: 'sleeping',
      type: v.type,
      who: v.type === 'guard' ? `${who} ${v.target}` : `${who} ${repoName(v.target)}`,
      state: v.type === 'guard' ? 'sleeps' : 'check.sh sleeps',
      tail: `${timeLeft(v.untilMs, now, clocks)} · ${passedText(v)}`,
    }
  return { kind: 'recovered', type: 'check', who: `${who} ${repoName(v.repo)}`, state: 'check.sh passes now', tail: '' }
}

/**
 * The band's status lines for `hidden`: one each up to STATUS_LINES, else one each
 * before the last, which counts the rest. `clocks` adds a running review's elapsed
 * time and a sleep's seconds.
 */
export function statusLines(hidden: readonly Hidden[], name: (v: ReviewView) => string, clocks: boolean, now: number): StatusLine[] {
  const line = (h: Hidden) => lineOf(h, name, clocks, now)
  if (hidden.length <= STATUS_LINES) return hidden.map(line)
  const named = hidden.slice(0, STATUS_LINES - 1)
  const rest = hidden.slice(STATUS_LINES - 1)
  const reviews = rest.flatMap(item => (item.type === 'review' ? [item.view] : []))
  // The review wording counts held and running reviews alone; any other state is counted plainly.
  if (reviews.length < rest.length || reviews.some(v => v.kind !== 'held' && v.kind !== 'running'))
    return [...named.map(line), { kind: 'more', text: `+${rest.length} more` }]
  const held = reviews.filter(v => v.kind === 'held').length
  const parts = [held ? `${plural(held, 'commit')} held` : '', rest.length - held ? `${rest.length - held} in review` : '']
  return [...named.map(line), { kind: 'more', text: `+${rest.length} more: ${parts.filter(Boolean).join(', ')}` }]
}
