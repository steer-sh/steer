#!/usr/bin/env python3
"""The six saved scenarios the report-comprehension pass reads.

Each scenario is one `steer status --json` snapshot plus the refreshed queue the
skill reads beside it, built over `base_status.json` — a real snapshot with its
inventory emptied and its paths scrubbed. The scenarios cover the six decisions a
reader arrives with: a new suggestion, an installed guard with observed recurrence,
reviewer identities that changed between windows, missing review decisions, a
suggestion that was declined, and a guard to remove.

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
        "attribution": {"applicable": False, "log_present": False,
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
)

STALE_COUNT = _record(
    id="stale-test-count-claim", **{"class": "stale test-count claim"},
    problem="A check prints a literal test count and the number goes stale the next "
            "time a test is added.",
    intervention="check.sh refuses a printed literal count.",
    match=r"test count|literal count",
    tier="check", target="check.sh",
    scope={"version": 1, "repo": "steer", "path": r"\.go$"},
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
    attr = rec["attribution"]
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
                                                else ["reviewer-example/3/prompt-example"]),
                       "after": (after_ids or (["scan-example|codex:adapter-example"] if session
                                               else ["reviewer-example/3/prompt-example"]))
                                if rec["after"]["units"] else []},
        "declined": rec["status"] == "rejected",
    }
    if rec.get("hook_id"):
        rec["observation"]["firing"]["hook_id"] = rec["hook_id"]
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
    obs = rec["observation"]
    if obs["recurrence"]["occurred"]:
        return "inspect"
    if rec["scope"] is None or any(c["unknown"] for c in
                                   (rec["before_coverage"], rec["after_coverage"])):
        return "repair"
    if obs["firing"]["applicable"] and not obs["firing"]["log_present"]:
        return "repair"
    return "observe"


def _queued(records):
    keys = ("id", "class", "problem", "intervention", "match", "corpus", "unit",
            "tier", "target", "hook_id", "before", "after", "observation", "scope",
            "before_coverage", "after_coverage", "signal_kind", "attribution")
    return [{k: r[k] for k in keys if k in r} for r in records if _action(r) != "observe"]


def _queue(interventions=(), candidates=(), unscored=()):
    return {
        "version": 5,
        "interventions": _queued(interventions),
        "candidates": list(candidates),
        "unscored": list(unscored),
        "corpora": [
            {"corpus": "review-findings", "present": True, "size": 41},
            {"corpus": "transcript-friction", "present": True, "size": 214},
            {"corpus": "auto-denials", "present": False, "size": 0},
        ],
    }


def _snapshot(records=(), candidates=(), unresolved=0):
    snap = _base()
    snap["interventions"] = [copy.deepcopy(r) for r in records]
    counts = {}
    for r in snap["interventions"]:
        action = _action(r)
        counts[action] = counts.get(action, 0) + 1
    snap["summary"] = {"total": len(snap["interventions"]), "counts": counts}
    snap["attention"] = {
        "interventions": _queued(snap["interventions"]),
        "candidates": list(candidates),
        "unscored": [],
    }
    snap["posture"]["unresolved_rows"] = unresolved
    return snap


def _new_suggestion():
    cands = [
        {"corpus": "transcript-friction", "pattern": "python: command not found",
         "count": 6, "sessions": 3, "signal_kind": "tool_failures"},
        {"corpus": "review-findings", "undecided_chains": 5,
         "dump": "steer commit-gate mine findings --kind accepted"},
    ]
    return _snapshot(candidates=cands), _queue(candidates=cands)


def _awaiting_evidence():
    rec = copy.deepcopy(BARE_PYTHON)
    rec.update(
        before={"hits": 3, "units": 10, "rate": 0.3},
        after={"hits": 1, "units": 4, "rate": 0.25},
        before_coverage=_coverage(11, 10, ineligible=1),
        after_coverage=_coverage(5, 4, partial_exposure=1),
        attribution={"applicable": True, "log_present": True,
                     "hook_id": "no-bare-python", "firings": 2, "sessions": 2},
    )
    _observe(rec)
    return _snapshot([rec]), _queue([rec])


def _changed_comparison():
    rec = copy.deepcopy(STALE_COUNT)
    rec.update(
        before={"hits": 4, "units": 82, "rate": 0.0488},
        after={"hits": 1, "units": 29, "rate": 0.0345},
        before_coverage=_coverage(84, 82, ineligible=2),
        after_coverage=_coverage(30, 29, ineligible=1),
    )
    _observe(rec, ["claude/opus-4.6/2 runs/prompt-6f21"],
             ["claude/opus-5/2 runs/prompt-6f21"])
    return _snapshot([rec]), _queue([rec])


def _missing_decisions():
    rec = copy.deepcopy(STALE_COUNT)
    rec.update(
        before={"hits": 4, "units": 82, "rate": 0.0488},
        after={"hits": 0, "units": 0, "rate": 0.0},
        before_coverage=_coverage(84, 82, ineligible=2),
        after_coverage=_coverage(9, 0, unknown=9, unknown_evidence=9, pending=2),
    )
    _observe(rec)
    return _snapshot([rec], unresolved=7), _queue([rec])


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
    return _snapshot([rec]), _queue([rec])


def _remove_a_guard():
    rec = copy.deepcopy(BARE_PYTHON)
    rec.update(
        before={"hits": 2, "units": 60, "rate": 0.0333},
        after={"hits": 8, "units": 44, "rate": 0.1818},
        before_coverage=_coverage(60, 60), after_coverage=_coverage(44, 44),
        attribution={"applicable": True, "log_present": True,
                     "hook_id": "no-bare-python", "firings": 31, "sessions": 12},
    )
    _observe(rec)
    return _snapshot([rec]), _queue([rec])


# SCENARIOS maps a name to a builder returning (status snapshot, queue).
SCENARIOS = {
    "new-suggestion": _new_suggestion,
    "awaiting-evidence": _awaiting_evidence,
    "changed-comparison": _changed_comparison,
    "missing-decisions": _missing_decisions,
    "declined-suggestion": _declined_suggestion,
    "remove-a-guard": _remove_a_guard,
}


def write(name, outdir):
    """Write one scenario's two files into outdir and return their paths."""
    snap, queue = SCENARIOS[name]()
    outdir = Path(outdir)
    outdir.mkdir(parents=True, exist_ok=True)
    status_path = outdir / "status.json"
    queue_path = outdir / "queue.json"
    status_path.write_text(json.dumps(snap, indent=2) + "\n")
    queue_path.write_text(json.dumps(queue, indent=2) + "\n")
    return status_path, queue_path
