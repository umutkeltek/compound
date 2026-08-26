#!/usr/bin/env python3
"""Render at most three consequential operator decisions per cycle."""

import json
import sys

MAX_DECISIONS = 3


def compact_key(group: dict) -> str:
    area = str(group.get("area") or "decision").replace("-", "_")
    key = str(group.get("key") or "")
    tail = key.split("|")[-1].replace("product:", "").replace("capability://", "")
    label = f"{area}/{tail}" if tail and tail != area else area
    return label[:36]


def main() -> int:
    try:
        report = json.load(sys.stdin)
        counts = report.get("counts") or {}
        groups = [g for g in report.get("groups", []) if g.get("band") == "needs_you"]
        visible = groups[:MAX_DECISIONS]
        omitted = len(groups) - len(visible)
        print(
            f"DECIDE {len(visible)} of {len(groups)} groups / "
            f"{counts.get('blocked_on_you', 0)} blocked. Reply: <number> <word>."
        )
        print("No reply: d=H7 means HOLD after 7d; no repeated approval prompt this cycle.")
        for index, group in enumerate(visible, start=1):
            action = group.get("action") or {}
            authority = action.get("policy_authority") or {}
            choices = "DO|HOLD" if action and authority.get("allowed") is True else "REVIEW|HOLD|DROP"
            count = int(group.get("count") or 1)
            print(f"{index:02d} {compact_key(group)} x{count} {choices} d=H7")
        if omitted:
            print(f"DEFERRED {omitted} lower-ranked groups remain ledger-visible, not re-asked this cycle.")
        return 0
    except (ValueError, TypeError, json.JSONDecodeError) as exc:
        print(f"attention-letter ERROR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
