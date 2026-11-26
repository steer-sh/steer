// Reads the sleeps the user set (cli/internal/sleep) and decides the band's guard and
// check states: an offer to sleep after each block, a countdown while one sleeps, and
// a short line when check.sh passes again.

import type { BlockRow, CheckRun, SleptCall } from './blocks'

/** One live or ended sleep, as its record file holds it. */
export type SleepRecord = { kind: 'guard' | 'check'; target: string; session: string; startedMs: number; untilMs: number }

/** The guard that never sleeps (cli/internal/protection, HookName). */
export const SELF_PROTECTION = 'steer-self-protection'

/** How long a sleep from the band lasts. */
export const SLEEP_MINUTES = 5

// The record format and the longest sleep a reader accepts (cli/internal/sleep, Version and MaxMinutes).
const SLEEP_VERSION = 1
const MAX_SLEEP_MS = 60 * 60_000

/** How long the band notes that check.sh passes again. */
export const RECOVERED_MS = 6_000

const isNumber = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)

/** Parses one sleep record; null for anything a reader of cli/internal/sleep treats as no sleep. */
export function parseSleep(text: string): SleepRecord | null {
  let raw: unknown
  try {
    raw = JSON.parse(text)
  } catch {
    return null
  }
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return null
  const d = raw as Record<string, unknown>
  if (d.version !== SLEEP_VERSION || (d.kind !== 'guard' && d.kind !== 'check')) return null
  if (typeof d.target !== 'string' || !d.target || typeof d.session !== 'string') return null
  if (!isNumber(d.started_ms) || !isNumber(d.until_ms) || d.until_ms - d.started_ms > MAX_SLEEP_MS) return null
  return { kind: d.kind, target: d.target, session: d.session, startedMs: d.started_ms, untilMs: d.until_ms }
}

/** A guard or check state the band can show. `key` names its target: one state per target. */
export type FixView =
  | { kind: 'offer'; type: 'guard'; key: string; hook: string; rule: string; tool: string; repos: string[]; count: number; rowIds: string[]; atMs: number }
  | {
      kind: 'offer'
      type: 'check'
      key: string
      repo: string
      failed: string[]
      allFailed: boolean
      outcome: string
      count: number
      rowIds: string[]
      atMs: number
    }
  | { kind: 'sleeping'; type: 'guard' | 'check'; key: string; target: string; startedMs: number; untilMs: number; passed: number }
  | { kind: 'recovered'; type: 'check'; key: string; repo: string; atMs: number }

const keyOf = (type: 'guard' | 'check', target: string) => `${type}:${target}`

/**
 * The session's guard and check states at `now`: a countdown for each live sleep; an
 * offer for each guard or repository with a block no Dismiss `closed`, recorded after
 * the last prompt (`closedAt`) and after the target's latest sleep began, newest first,
 * counting its open blocks (a check pass ends a repository's offer); and a short line
 * for a check pass in the last RECOVERED_MS. The self-protection guard gets no offer.
 */
export function fixViews(input: {
  rows: readonly BlockRow[]
  slept: readonly SleptCall[]
  runs: readonly CheckRun[]
  sleeps: readonly SleepRecord[]
  closed: ReadonlySet<string>
  closedAt: number
  session: string
  now: number
}): FixView[] {
  const { session, now } = input
  const mine = input.sleeps.filter(r => r.session === session)
  const live = mine.filter(r => r.startedMs <= now && now < r.untilMs)
  const sleeping = new Set(live.map(r => keyOf(r.kind, r.target)))
  // A sleep answers every block of its target before it began, however it was set.
  const answered = new Map<string, number>()
  for (const r of mine) answered.set(keyOf(r.kind, r.target), Math.max(answered.get(keyOf(r.kind, r.target)) ?? 0, r.startedMs))
  const open = (key: string, at: number) => at > input.closedAt && at > (answered.get(key) ?? -Infinity)
  // Every repository each guard blocked in, closed blocks too: a sleep is session-wide.
  const blockedIn = new Map<string, string[]>()
  for (const row of input.rows) {
    if (row.kind !== 'guard' || !row.block.repo) continue
    const repos = blockedIn.get(row.block.hook) ?? []
    if (!repos.includes(row.block.repo)) blockedIn.set(row.block.hook, [...repos, row.block.repo])
  }
  const offers = new Map<string, FixView & { kind: 'offer' }>()
  const recovered: FixView[] = []
  for (const row of input.rows) {
    if (row.kind === 'check-passed') {
      const key = keyOf('check', row.run.repo)
      offers.delete(key)
      if (now - row.run.finishedMs < RECOVERED_MS && !sleeping.has(key))
        recovered.push({ kind: 'recovered', type: 'check', key, repo: row.run.repo, atMs: row.run.finishedMs })
      continue
    }
    if (row.kind === 'check-unfixed') continue
    if (input.closed.has(row.id)) continue
    if (row.kind === 'guard') {
      const { block } = row
      const key = keyOf('guard', block.hook)
      if (block.hook === SELF_PROTECTION || sleeping.has(key) || !open(key, block.atMs)) continue
      const had = offers.get(key)
      offers.set(key, {
        kind: 'offer',
        type: 'guard',
        key,
        hook: block.hook,
        rule: block.rule,
        tool: block.tool,
        repos: blockedIn.get(block.hook) ?? [],
        count: (had?.count ?? 0) + 1,
        rowIds: [...(had?.rowIds ?? []), row.id],
        atMs: block.atMs,
      })
      continue
    }
    const key = keyOf('check', row.run.repo)
    if (sleeping.has(key) || !open(key, row.run.finishedMs)) continue
    const had = offers.get(key)
    offers.set(key, {
      kind: 'offer',
      type: 'check',
      key,
      repo: row.run.repo,
      failed: row.failed,
      allFailed: row.allFailed,
      outcome: row.run.outcome,
      count: (had?.count ?? 0) + 1,
      rowIds: [...(had?.rowIds ?? []), row.id],
      atMs: row.run.finishedMs,
    })
  }
  const countdowns: FixView[] = live
    .sort((a, b) => b.startedMs - a.startedMs)
    .map(r => ({
      kind: 'sleeping',
      type: r.kind,
      key: keyOf(r.kind, r.target),
      target: r.target,
      startedMs: r.startedMs,
      untilMs: r.untilMs,
      passed:
        r.kind === 'guard'
          ? input.slept.filter(c => c.hook === r.target && c.session === session && c.atMs >= r.startedMs && c.atMs < r.untilMs).length
          : input.runs.filter(
              run => run.disposition === 'slept' && run.repo === r.target && run.session === session && run.finishedMs >= r.startedMs,
            ).length,
    }))
  return [...[...offers.values()].sort((a, b) => b.atMs - a.atMs), ...countdowns, ...recovered]
}
