#!/usr/bin/env python3
"""Checks that every query in the Grafana dashboard is valid PromQL.

Run against a live Prometheus (the compose stack, or CI's). Queries are
reported as invalid only when Prometheus rejects them; a valid query that
returns nothing is fine, because a regtest node legitimately has no peers, no
fee history and no RPC errors.
"""
import json
import pathlib
import sys
import urllib.error
import urllib.parse
import urllib.request

DASHBOARD = pathlib.Path(__file__).resolve().parent.parent / "deploy/grafana/dashboards/bitcoin-node.json"
PROMETHEUS = "http://127.0.0.1:9090"
SUBSTITUTIONS = {
    "$job": "bitcoin",
    "$instance": "exporter:9332",
    "$__rate_interval": "1m",
}


def queries(panels):
    for panel in panels:
        yield from queries(panel.get("panels", []))
        for target in panel.get("targets", []):
            if expr := target.get("expr"):
                yield panel.get("title", "(untitled)"), expr


def main() -> int:
    dashboard = json.loads(DASHBOARD.read_text())

    invalid, empty, checked = [], [], 0
    for title, expr in queries(dashboard["panels"]):
        query = expr
        for placeholder, value in SUBSTITUTIONS.items():
            query = query.replace(placeholder, value)

        url = f"{PROMETHEUS}/api/v1/query?" + urllib.parse.urlencode({"query": query})
        try:
            with urllib.request.urlopen(url, timeout=10) as response:
                body = json.load(response)
        except urllib.error.HTTPError as err:
            invalid.append((title, query, json.load(err).get("error", str(err))))
            continue
        checked += 1

        if body.get("status") != "success":
            invalid.append((title, query, body.get("error", "unknown")))
        elif not body["data"]["result"]:
            empty.append((title, query))

    print(f"checked {checked} dashboard queries against {PROMETHEUS}")
    for title, query in empty:
        print(f"  no data (allowed): {title}: {query}")
    for title, query, why in invalid:
        print(f"  INVALID: {title}: {query}\n    {why}", file=sys.stderr)

    if invalid:
        print(f"\n{len(invalid)} invalid queries", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
