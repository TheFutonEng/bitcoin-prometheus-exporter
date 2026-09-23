#!/usr/bin/env python3
"""Checks that Prometheus loaded every alert rule and evaluated it cleanly.

A rule group reports health "unknown" with a zero lastEvaluation until its
first evaluation, which happens up to one evaluation_interval after Prometheus
starts. Judging health before then reports every rule as broken, so this waits
for the group to evaluate before looking at it.
"""
import http.client
import json
import pathlib
import re
import sys
import time
import urllib.error
import urllib.request

# A Prometheus that is starting, restarting or mid-request can fail in several
# unrelated ways: refused, reset, timed out, half a response, or an error page
# that is not JSON. They all mean "not ready yet", so they are caught together.
TRANSIENT = (OSError, http.client.HTTPException, json.JSONDecodeError)

PROMETHEUS = "http://127.0.0.1:9090"
RULE_FILE = pathlib.Path(__file__).resolve().parent.parent / "deploy/prometheus/alerts.yml"
NEVER_EVALUATED = "0001-01-01T00:00:00Z"
TIMEOUT_SECONDS = 120


def fetch_groups():
    """Returns the rule groups, or None while Prometheus is not answering yet."""
    try:
        with urllib.request.urlopen(f"{PROMETHEUS}/api/v1/rules", timeout=10) as response:
            body = json.load(response)
    except TRANSIENT:
        return None
    if body.get("status") != "success":
        return None
    return body["data"]["groups"]


def declared_rule_count() -> int:
    """Counts the alerts in the rule file, to catch one that only half loaded."""
    return len(re.findall(r"^\s*-\s*alert:", RULE_FILE.read_text(), re.MULTILINE))


def main() -> int:
    expected = declared_rule_count()
    deadline = time.monotonic() + TIMEOUT_SECONDS
    groups = None

    while time.monotonic() < deadline:
        groups = fetch_groups()
        if groups and all(g.get("lastEvaluation", NEVER_EVALUATED) != NEVER_EVALUATED for g in groups):
            break
        time.sleep(2)
    else:
        print(f"rule groups did not evaluate within {TIMEOUT_SECONDS}s", file=sys.stderr)
        if groups:
            for group in groups:
                print(f"  {group['name']}: lastEvaluation={group.get('lastEvaluation')}", file=sys.stderr)
        return 1

    rules = [(g["name"], r) for g in groups for r in g["rules"]]
    print(f"{len(rules)} rules in {len(groups)} group(s):")
    for group_name, rule in rules:
        print(f"  [{rule['health']:>7}] {group_name}/{rule['name']} (state: {rule.get('state', '-')})")

    unhealthy = [(g, r) for g, r in rules if r["health"] != "ok"]
    for group_name, rule in unhealthy:
        detail = rule.get("lastError") or "no error reported"
        print(f"  UNHEALTHY: {group_name}/{rule['name']}: health={rule['health']}: {detail}", file=sys.stderr)

    if unhealthy:
        print(f"\n{len(unhealthy)} unhealthy rules", file=sys.stderr)
        return 1
    if len(rules) != expected:
        print(f"\nloaded {len(rules)} rules but {RULE_FILE.name} declares {expected}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
