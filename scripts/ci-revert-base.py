#!/usr/bin/env python3
"""Resolve the base commit that revert-test compares a CI checkout against.

Run from the repository root of a full-history checkout. The workflow passes the
event's values in the environment:

  BASE_POLICY     "event" or "release"
  EVENT_NAME      github.event_name
  REF_NAME        github.ref_name
  DEFAULT_BRANCH  the repository's default branch
  BEFORE          github.event.before (push)
  PR_HEAD_SHA     github.event.pull_request.head.sha (pull_request)
  REPOSITORY      github.repository (release)

Policy "event" picks the base from the trigger:

  pull_request  the first parent of the merge commit GitHub checks out, whose
                second parent must be the pull request's head
  push          BEFORE exactly, fetched when a force push left it unreachable
  new branch    (BEFORE all zeros) the merge base with the default branch; a new
                default branch has no base

Policy "release" requires the tagged commit to be on the default branch and the
check workflow's push run for that exact commit to have succeeded; that run
checked the commit's own range, so the base is HEAD.

On success it prints the policy, base and head, and appends REVERT_TEST_BASE to
$GITHUB_ENV when that is set. Any base it cannot resolve exits 1: falling back to
HEAD would let revert-test check nothing.
"""

import json
import os
import subprocess
import sys

ZERO = "0" * 40
WORKFLOW = "check.yml"


class Unresolved(Exception):
    pass


def git(*args):
    result = subprocess.run(["git", *args], capture_output=True, text=True)
    if result.returncode != 0:
        raise Unresolved("git %s: %s" % (" ".join(args), result.stderr.strip()))
    return result.stdout.strip()


def has_commit(rev):
    return subprocess.run(["git", "cat-file", "-e", rev + "^{commit}"],
                          capture_output=True).returncode == 0


def need(name):
    value = os.environ.get(name, "").strip()
    if not value:
        raise Unresolved("%s is not set" % name)
    return value


def pull_request_base(head):
    parents = git("rev-list", "--parents", "-n", "1", head).split()[1:]
    if len(parents) != 2:
        raise Unresolved("HEAD %s is not the pull request's merge commit" % head[:12])
    if parents[1] != need("PR_HEAD_SHA"):
        raise Unresolved("the merge commit's second parent %s is not the pull request head %s"
                         % (parents[1][:12], need("PR_HEAD_SHA")[:12]))
    return parents[0], "first parent of the pull request merge commit"


def push_base(head):
    before = need("BEFORE")
    if before == ZERO:
        default = need("DEFAULT_BRANCH")
        if need("REF_NAME") == default:
            raise Unresolved("a new %s branch has no base to compare against" % default)
        return (git("merge-base", head, "refs/remotes/origin/" + default),
                "merge base of a new branch with %s" % default)
    if not has_commit(before):
        subprocess.run(["git", "fetch", "--no-tags", "--quiet", "origin", before],
                       capture_output=True)
        if not has_commit(before):
            raise Unresolved("the push's previous tip %s cannot be fetched" % before[:12])
    return before, "previous tip of the pushed branch"


def check_runs(repository, branch, head, status):
    query = ("repos/%s/actions/workflows/%s/runs?event=push&branch=%s&head_sha=%s"
             % (repository, WORKFLOW, branch, head))
    if status:
        query += "&status=" + status
    result = subprocess.run(["gh", "api", query], capture_output=True, text=True)
    if result.returncode != 0:
        raise Unresolved("gh api %s: %s" % (query, result.stderr.strip()))
    runs = json.loads(result.stdout).get("workflow_runs", [])
    return [run for run in runs if run.get("head_branch") == branch and run.get("head_sha") == head]


def release_base(head):
    default = need("DEFAULT_BRANCH")
    repository = need("REPOSITORY")
    if subprocess.run(["git", "merge-base", "--is-ancestor", head, "refs/remotes/origin/" + default],
                      capture_output=True).returncode != 0:
        raise Unresolved("the tagged commit %s is not on %s" % (head[:12], default))
    passed = check_runs(repository, default, head, "success")
    if passed:
        return head, "release of a commit whose %s push run passed: %s" % (WORKFLOW, passed[0].get("html_url"))
    runs = check_runs(repository, default, head, None)
    if any(run.get("status") != "completed" for run in runs):
        raise Unresolved("the %s push run for %s has not finished; rerun the release after it completes"
                         % (WORKFLOW, head[:12]))
    if runs:
        raise Unresolved("the %s push run for %s did not pass" % (WORKFLOW, head[:12]))
    raise Unresolved("no %s push run on %s exists for %s" % (WORKFLOW, default, head[:12]))


def resolve():
    head = git("rev-parse", "HEAD")
    policy = need("BASE_POLICY")
    if policy == "release":
        base, reason = release_base(head)
    elif policy != "event":
        raise Unresolved("unknown BASE_POLICY %r" % policy)
    elif need("EVENT_NAME") == "pull_request":
        base, reason = pull_request_base(head)
    elif need("EVENT_NAME") == "push":
        base, reason = push_base(head)
    else:
        raise Unresolved("no base policy for event %r" % os.environ["EVENT_NAME"])
    return policy, base, head, reason


def main():
    try:
        policy, base, head, reason = resolve()
    except Unresolved as error:
        print("ci-revert-base: %s" % error, file=sys.stderr)
        return 1
    print("ci-revert-base: policy %s, base %s, head %s (%s)" % (policy, base, head, reason))
    env_file = os.environ.get("GITHUB_ENV")
    if env_file:
        with open(env_file, "a", encoding="utf-8") as handle:
            handle.write("REVERT_TEST_BASE=%s\n" % base)
    return 0


if __name__ == "__main__":
    sys.exit(main())
