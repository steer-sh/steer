#!/usr/bin/env python3
"""The saved scenarios the report-comprehension pass reads.

Each scenario is one `steer status --json` snapshot, the `steer fix score --json`
rows and the refreshed queue the skill reads beside it, built over
`base_status.json` — a real snapshot with its
inventory emptied and its paths scrubbed. The scenarios cover the decisions a
reader arrives with: a new suggestion, an installed guard with observed recurrence,
reviewer identities that changed between windows, missing review decisions, a
suggestion that was declined, a guard to remove, and a first run with session
history alone.

Nothing here judges the output. A person reads the reports side by side; this
file only pins what the model is shown.
"""

import copy
import json
from pathlib import Path

BASE = Path(__file__).with_name("base_status.json")


def _base():
    return json.loads(BASE.read_text())


def _coverage(total, eligible, **kw):
    cov = {
        "total": total, "eligible": eligible, "ineligible": 0, "unknown": 0,
        "unknown_scope": 0, "unknown_evidence": 0, "pending": 0,
        "partial_exposure": 0,
    }
    cov.update(kw)
    return cov


def _record(**kw):
    rec = {
        "id": "a-record", "class": "a class", "problem": "", "intervention": "",
        "match": "", "tier": "check", "target": "check.sh", "status": "active",
        "corpus": "review-findings", "unit": "chain",
        "before": {"hits": 0, "units": 0, "rate": 0.0},
        "after": {"hits": 0, "units": 0, "rate": 0.0},
        "scope": {"version": 1, "all": True},
        "before_coverage": _coverage(0, 0), "after_coverage": _coverage(0, 0),
        # A build input for the observation's firing block, never a stored field:
        # the score row carries the join inside `observation.firing`.
        "_firing": {"applicable": False, "log_present": False,
                    "hook_id": "", "firings": 0, "sessions": 0},
    }
    rec.update(kw)
    return rec


BARE_PYTHON = _record(
    id="bare-python-call", **{"class": "bare python call"},
    problem="The agent runs `python` where this project only has `python3`, and the "
            "command dies with command not found.",
    intervention="A pre-tool guard refuses a bare `python` invocation and asks for "
                 "`python3` instead.",
    match=r"python: command not found",
    tier="steer", target="/home/example/.steer/hooks/no-bare-python.py",
    hook_id="no-bare-python", corpus="transcript-friction", unit="session",
    signal_kind="tool_failures",
    scope={"version": 2, "harness": ["claude-code", "codex"]},
    artifact={"id": "no-bare-python", "scope": "global", "state": "present"},
)

STALE_COUNT = _record(
    id="stale-test-count-claim", **{"class": "stale test-count claim"},
    problem="A check prints a literal test count and the number goes stale the next "
            "time a test is added.",
    intervention="check.sh refuses a printed literal count.",
    match=r"test count|literal count",
    tier="check", target="check.sh",
    scope={"version": 1, "repo": "steer", "path": r"\.go$"},
    artifact={"id": "no-literal-test-count", "scope": "project", "repo": "steer",
              "state": "present"},
    # A build input for the observation's checks block: executions recorded here
    # in the after-window, all passing.
    _checks={"check_id": "no-literal-test-count", "executions": 5},
)


def _observe(rec, before_ids=None, after_ids=None):
    """Attach the same observation shape both field scorers emit."""
    split = rec.get("rejected_ts", 1787500000)
    observed = 1788253200

    def side(counts, first, last):
        return {"hits": counts["hits"], "eligible": counts["units"],
                "first_activity_ts": first if counts["units"] else 0,
                "last_activity_ts": last if counts["units"] else 0}

    session = rec["unit"] == "session"
    attr = rec["_firing"]
    hits = rec["after"]["hits"]
    rec["observation"] = {
        "counts": {"split_ts": split, "observed_ts": observed, "unit": rec["unit"],
                   "before": side(rec["before"], split - 864000, split - 100),
                   "after": side(rec["after"], split + 100, observed - 100)},
        "recurrence": {"occurred": hits > 0, "matches": hits},
        "firing": {"applicable": attr["applicable"], "log_present": attr["log_present"],
                   "scope": "eligible_units" if session else "window_period",
                   "class_scoped": session, "rows": attr["firings"],
                   "units": attr["sessions"]},
        "coverage": {"before": rec["before_coverage"], "after": rec["after_coverage"],
                     "zero_eligible_after": rec["after"]["units"] == 0},
        "provenance": {"kind": "parser" if session else "reviewer",
                       "before": before_ids or (["scan-example|codex:adapter-example"] if session
                                                else ["reviewer-example/3/prompt-example (provider claude-code)"]),
                       "after": (after_ids or (["scan-example|codex:adapter-example"] if session
                                               else ["reviewer-example/3/prompt-example (provider claude-code)"]))
                                if rec["after"]["units"] else []},
        "declined": rec["status"] == "rejected",
    }
    if rec.get("hook_id"):
        rec["observation"]["firing"]["hook_id"] = rec["hook_id"]
    if rec.get("_checks"):
        runs = rec["_checks"]["executions"]
        rec["observation"]["checks"] = {
            "applicable": True, "attribution": "check-id", "stream_present": True,
            "other_host": False, "check_id": rec["_checks"]["check_id"],
            "repo": rec["scope"]["repo"], "scope": "window_period", "class_scoped": False,
            "executions": runs, "outcomes": {"passed": runs} if runs else {},
            "blocked": 0, "changed_detector": 0, "units": 0,
            "newest_ts": observed - 300 if runs else 0}
    if attr["firings"]:
        rec["observation"]["firing"]["newest_ts"] = observed - 200
    if hits:
        rec["observation"]["recurrence"].update(
            example_kind="cluster_key" if session else "adjudicated_finding",
            example="python: command not found" if session else rec["problem"],
            example_ts=observed - 100,
        )
    return rec


def _action(rec):
    """status.ActionFor: unknown coverage is disclosed, never a repair; only an
    absent guard log or a missing scope asks for one."""
    obs = rec["observation"]
    if obs["recurrence"]["occurred"]:
        return "inspect"
    if rec["scope"] is None:
        return "repair"
    if obs["firing"]["applicable"] and not obs["firing"]["log_present"]:
        return "repair"
    return "observe"


def _queued(records):
    keys = ("id", "class", "problem", "intervention", "match", "corpus", "unit",
            "tier", "target", "hook_id", "before", "after", "observation", "artifact",
            "scope", "before_coverage", "after_coverage", "signal_kind")
    return [{k: r[k] for k in keys if k in r} for r in records if _action(r) != "observe"]


def _summary(rec):
    """The status v27 row: the record without its observation, match or coverage."""
    keys = ("id", "class", "problem", "intervention", "tier", "target", "status",
            "hook_id", "scope", "corpus", "unit", "signal_kind", "before", "after")
    row = {k: rec[k] for k in keys if k in rec}
    row["artifact"] = rec.get("artifact")
    row["recurred"] = rec["observation"]["recurrence"]["occurred"]
    return row


def _score(rec):
    """The `steer fix score --json` row: the detail, with `chains` as the unit count."""
    keys = ("id", "class", "tier", "target", "status", "hook_id", "scope",
            "before_coverage", "after_coverage", "observation", "reason", "reopen",
            "signal_kind")
    row = {k: rec[k] for k in keys if k in rec}
    for side in ("before", "after"):
        inc = rec[side]
        row[side] = {"hits": inc["hits"], "chains": inc["units"], "rate": inc["rate"]}
    return row


def _queue(interventions=(), candidates=(), unscored=(), review_size=211):
    return {
        "version": 15,
        "interventions": _queued(interventions),
        "candidates": list(candidates),
        "unscored": list(unscored),
        "checks": [],
        "corpora": [
            {"corpus": "review-findings", "present": review_size > 0,
             "size": review_size},
            {"corpus": "transcript-friction", "present": True, "size": 465},
        ],
    }


def _snapshot(records=(), candidates=(), unresolved=0):
    snap = _base()
    counts = {}
    for r in records:
        action = _action(r)
        counts[action] = counts.get(action, 0) + 1
    snap["interventions"] = [_summary(r) for r in records]
    snap["summary"] = {"total": len(records), "counts": counts}
    snap["attention"] = {
        "interventions": [r["id"] for r in _queued(records)],
        "unscored": [],
        # A scenario's pile is new to the reader, so steer status reads it unread.
        "candidates": [dict(c, unread=True) for c in candidates],
        "checks": [],
    }
    snap["posture"]["unresolved_rows"] = unresolved
    # Blocks come from the records' guards alone; the base snapshot's count
    # belongs to guards no scenario lists.
    firings = sum(r["_firing"]["firings"] for r in records)
    snap["telemetry"]["audit_firings"] = firings
    for h in snap["telemetry"]["harnesses"]:
        h["firings"] = firings if h["harness"] == "claude-code" else 0
    bound = [r for r in records if "checks" in r["observation"]]
    if bound:
        installed = min(r["observation"]["counts"]["split_ts"] for r in bound)
        snap["checks"] = {
            "repo": "/home/example/steer", "manifest": True, "adopted": True,
            "adopted_at": installed - 200,
            "runner": [{"harness": "claude-code", "scope": "global", "registered": True,
                        "effective": True, "trust": "not-applicable"}],
            "checks": [{"id": r["_checks"]["check_id"], "kind": "script",
                        "binding": "scripts/no-literal-count.sh", "validation": "current",
                        "validated_at": installed - 100, "execution": "passed",
                        "executed_at": r["observation"]["checks"]["newest_ts"]}
                       for r in bound],
            "latest_run": None,
        }
    return snap


def _scores(records=()):
    return [_score(r) for r in records]


def _new_suggestion():
    cands = [
        {"corpus": "transcript-friction", "pattern": "python: command not found",
         "count": 6, "sessions": 3, "signal_kind": "tool_failures"},
        {"corpus": "review-findings", "undecided_chains": 5,
         "dump": "steer commit-gate mine findings --kind accepted"},
    ]
    return _snapshot(candidates=cands), _scores(), _queue(candidates=cands)


def _awaiting_evidence():
    rec = copy.deepcopy(BARE_PYTHON)
    rec.update(
        before={"hits": 3, "units": 10, "rate": 0.3},
        after={"hits": 1, "units": 4, "rate": 0.25},
        before_coverage=_coverage(11, 10, ineligible=1),
        after_coverage=_coverage(5, 4, partial_exposure=1),
        _firing={"applicable": True, "log_present": True,
                 "hook_id": "no-bare-python", "firings": 2, "sessions": 2},
    )
    _observe(rec)
    return _snapshot([rec]), _scores([rec]), _queue([rec])


def _changed_comparison():
    rec = copy.deepcopy(STALE_COUNT)
    rec.update(
        before={"hits": 4, "units": 82, "rate": 0.0488},
        after={"hits": 1, "units": 29, "rate": 0.0345},
        _checks={"check_id": "no-literal-test-count", "executions": 40},
        before_coverage=_coverage(84, 82, ineligible=2),
        after_coverage=_coverage(30, 29, ineligible=1),
    )
    _observe(rec, ["claude/opus-4.6/2 runs/prompt-6f21"],
             ["claude/opus-5/2 runs/prompt-6f21"])
    return _snapshot([rec]), _scores([rec]), _queue([rec])


def _missing_decisions():
    rec = copy.deepcopy(STALE_COUNT)
    rec.update(
        before={"hits": 4, "units": 82, "rate": 0.0488},
        after={"hits": 0, "units": 0, "rate": 0.0},
        before_coverage=_coverage(84, 82, ineligible=2),
        after_coverage=_coverage(9, 0, unknown=9, unknown_evidence=9, pending=2),
    )
    _observe(rec)
    return _snapshot([rec], unresolved=7), _scores([rec]), _queue([rec])


def _declined_suggestion():
    rec = _record(
        id="within-chain-re-raised-finding",
        **{"class": "within-chain re-raised finding"},
        problem="The reviewer raises a concern an earlier round of the same review "
                "already settled.",
        intervention="no suitable intervention was identified",
        match=r"already (addressed|settled)",
        tier="none", target="", status="rejected",
        reason="The tell is the reviewer's own framing, which changes with the model.",
        reopen="the rate of re-raised findings triples, or the reviewer stops "
               "restating settled concerns in its own words",
        rejected_ts=1756000000,
        before={"hits": 3, "units": 60, "rate": 0.05},
        after={"hits": 9, "units": 41, "rate": 0.2195},
        before_coverage=_coverage(60, 60), after_coverage=_coverage(41, 41),
    )
    _observe(rec)
    return _snapshot([rec]), _scores([rec]), _queue([rec])


def _remove_a_guard():
    rec = copy.deepcopy(BARE_PYTHON)
    rec.update(
        before={"hits": 2, "units": 60, "rate": 0.0333},
        after={"hits": 8, "units": 44, "rate": 0.1818},
        before_coverage=_coverage(60, 60), after_coverage=_coverage(44, 44),
        _firing={"applicable": True, "log_present": True,
                 "hook_id": "no-bare-python", "firings": 31, "sessions": 12},
    )
    _observe(rec)
    return _snapshot([rec]), _scores([rec]), _queue([rec])


def _first_run():
    """Session history only: the commit gate was just armed, so no review has run,
    and no fix or guard is installed."""
    cands = [
        {"corpus": "transcript-friction",
         "pattern": "run the tests before you say it is done",
         "count": 7, "sessions": 4, "signal_kind": "user_corrections",
         "examples": ["you said it was done but go test fails",
                      "run the tests before you say it is done",
                      "the tests fail again, check them before you stop"]},
        {"corpus": "transcript-friction", "pattern": "python: command not found",
         "count": 6, "sessions": 3, "signal_kind": "tool_failures"},
    ]
    snap = _snapshot(candidates=cands)
    snap["corpora"] = [
        {"corpus": "review-findings", "present": False, "size": 0,
         "path": "/home/example/.steer/state/commit-review"},
        {"corpus": "transcript-friction", "present": True, "size": 465,
         "path": "/home/example/.steer/state/transcript-sessions"},
    ]
    tel = snap["telemetry"]
    tel["review_chains"] = 0
    tel["audit_firings"] = 0
    for h in tel["harnesses"]:
        h["chains"] = 0
        h["firings"] = 0
    for row in snap["posture"]["commit_gate"] + snap["posture"]["resolver"]:
        if "observed" in row:
            row["observed"] = 0
    for m in snap["posture"]["masters"]:
        m.pop("observed", None)
    readiness = snap["posture"]["readiness"]
    readiness["unverified"] = [
        line.split(" · ")[0] + " · never observed" if " · " in line else line
        for line in readiness["unverified"]]
    readiness["note"] = "no review observed yet — that is expected until the first commit in scope"
    return snap, _scores(), _queue(candidates=cands, review_size=0)


def _busy_report():
    records = []
    addenda = []
    for ident, problem, hits, after in (
        ("untracked-document", "The reviewer claims an untracked document is committed.", 6, 72),
        ("message-inventory", "The reviewer asks the commit message to list every changed file.", 11, 58),
        ("build-claim", "The reviewer incorrectly claims the change does not build.", 4, 91),
    ):
        text = "A review prompt entry addresses this claim."
        rec = _record(id=ident, problem=problem, intervention=text, tier="prompt",
                      before={"hits": hits, "units": 412, "rate": hits / 412},
                      after={"hits": 0, "units": after, "rate": 0},
                      before_coverage=_coverage(412, 412), after_coverage=_coverage(after, after),
                      artifact={"id": ident, "scope": "global", "state": "present"})
        records.append(_observe(rec))
        addenda.append({"id": ident, "scope": "global", "text": text, "state": "present"})
    # A fresh check: bound, validated and running here, with no eligible review
    # work since its install.
    fresh = copy.deepcopy(STALE_COUNT)
    fresh.update(before={"hits": 4, "units": 82, "rate": 4 / 82},
                 before_coverage=_coverage(82, 82))
    records.append(_observe(fresh))
    repair = copy.deepcopy(BARE_PYTHON)
    repair.update(after={"hits": 1, "units": 34, "rate": 1 / 34},
                  after_coverage=_coverage(34, 34),
                  _firing={"applicable": True, "log_present": True,
                           "hook_id": "no-bare-python", "firings": 0, "sessions": 0})
    _observe(repair)
    repair["observation"]["recurrence"]["example"] = "python helper prints command not found as a test fixture"
    records.append(repair)
    # The Windows finding reaches the report as a review pile; supporting_evidence
    # holds the dump's content.
    cands = [{"corpus": "review-findings", "undecided_chains": 2,
              "dump": "steer commit-gate mine findings --kind accepted"}]
    snap = _snapshot(records, candidates=cands)
    snap["addenda"] = addenda
    return snap, _scores(records), _queue(records, candidates=cands)


def _agent_feedback():
    rec = copy.deepcopy(BARE_PYTHON)
    rec.update(before={"hits": 3, "units": 41, "rate": 3 / 41}, before_coverage=_coverage(41, 41),
               after={"hits": 0, "units": 34, "rate": 0}, after_coverage=_coverage(34, 34),
               _firing={"applicable": True, "log_present": True,
                        "hook_id": "no-bare-python", "firings": 1, "sessions": 1})
    _observe(rec)
    return _snapshot([rec]), _scores([rec]), _queue([rec])


def _guard_without_record():
    # A guard installed through /steer: its stored cases pass, and no record
    # tracks a problem against a baseline.
    snap = _snapshot()
    snap["hooks"] = [{
        "name": "no-bare-python", "scope": "global", "source": "generated",
        "rule": "never use bare python in bash commands | use python3",
        "fixtures": {"applicable": True, "state": "current", "cases": 14,
                     "verified_at": "2026-10-05T14:02:11Z", "shadowed": 0},
    }]
    return snap, _scores(), _queue()


def supporting_evidence(name):
    """Saved audit output and inspected facts supplied instead of live commands."""
    if name == "busy-report":
        return {"inspected_match": {
            "id": "bare-python-call", "belongs_to_problem": False,
            "context": "The command invoked python3 and printed a diagnostic as fixture data. "
                       "It never invoked the bare python command.",
        }, "review_findings": {
            "source": "the review candidate's dump command, summarized",
            "problem": "Two Go tests set HOME but not USERPROFILE; Windows home lookup uses USERPROFILE.",
            "review_sequences": 2, "repository": "example-project",
            "files_needing_cleanup": 22, "windows_ci": "not established by available evidence",
        }}
    if name.startswith("agent-feedback"):
        firing = {"firing_id": "example-firing", "hook_name": "no-bare-python",
                  "harness": "claude-code", "applies": "current",
                  "reasons": [{"reason": "This command invokes python inside a container where it exists.",
                               "recorded_at": "2026-09-01T09:00:00Z"}]}
        confirmed = name == "agent-feedback-confirmed"
        if confirmed:
            firing["judgment"] = "incorrect"
        return {"contests": {"guards": [{
            "hook_name": "no-bare-python", "store_dir": "/home/example/.steer/hooks",
            "firings": 1, "not_offered": 0, "contested": 1,
            "unread": 0 if confirmed else 1, "contested_excluded": 0,
            "contested_firings": [firing],
        }]}, "inspection": ({"blocked_action": "docker run example-image python -V",
                              "finding": "The image contains python; the guard applied the host's restriction inside the container.",
                              "continuation_inspected": False} if confirmed else None)}
    return None


# SCENARIOS maps a name to a builder returning (status snapshot, score rows, queue).
SCENARIOS = {
    "new-suggestion": _new_suggestion,
    "awaiting-evidence": _awaiting_evidence,
    "changed-comparison": _changed_comparison,
    "missing-decisions": _missing_decisions,
    "declined-suggestion": _declined_suggestion,
    "remove-a-guard": _remove_a_guard,
    "first-run": _first_run,
    "busy-report": _busy_report,
    "agent-feedback": _agent_feedback,
    "agent-feedback-confirmed": _agent_feedback,
    "guard-without-record": _guard_without_record,
}


def write(name, outdir):
    """Write one scenario's three files into outdir and return their paths."""
    snap, scores, queue = SCENARIOS[name]()
    outdir = Path(outdir)
    outdir.mkdir(parents=True, exist_ok=True)
    status_path = outdir / "status.json"
    score_path = outdir / "score.json"
    queue_path = outdir / "queue.json"
    status_path.write_text(json.dumps(snap, indent=2) + "\n")
    score_path.write_text(json.dumps(scores, indent=2) + "\n")
    queue_path.write_text(json.dumps(queue, indent=2) + "\n")
    return status_path, score_path, queue_path
