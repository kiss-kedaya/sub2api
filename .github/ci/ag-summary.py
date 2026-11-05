#!/usr/bin/env python3
"""Summarize `go ... -debug-actiongraph=FILE` output as a markdown table.

Experiment helper: shows how much of a go build/test step was real compile,
link or vet work (summed CmdReal, i.e. CPU-parallel time) versus cache hits,
and how long the test binaries themselves ran (wall time).

usage: ag-summary.py LABEL FILE [FILE...]
"""
import collections
import json
import re
import sys
from datetime import datetime

FIRST_PARTY = "github.com/Wei-Shaw/sub2api"


def parse_ts(value):
    # Go emits up to 9 fractional digits; datetime only takes 6.
    value = re.sub(r"(\.\d{6})\d+", r"\1", value)
    return datetime.fromisoformat(value)


def main(argv):
    label, files = argv[1], argv[2:]
    total = collections.Counter()
    executed = collections.Counter()
    cpu = collections.Counter()
    first_party = collections.Counter()
    test_wall = 0.0
    for path in files:
        try:
            with open(path, encoding="utf-8") as fh:
                actions = json.load(fh)
        except (OSError, ValueError) as err:
            print(f"<!-- skip {path}: {err} -->")
            continue
        for act in actions:
            mode = act.get("Mode") or "?"
            total[mode] += 1
            if mode == "test run":
                try:
                    delta = parse_ts(act["TimeDone"]) - parse_ts(act["TimeStart"])
                    test_wall += delta.total_seconds()
                except (KeyError, ValueError):
                    pass
            if act.get("Cmd"):
                executed[mode] += 1
                cpu[mode] += act.get("CmdReal", 0) / 1e9
                if (act.get("Package") or "").startswith(FIRST_PARTY):
                    first_party[mode] += 1
    print(f"**{label}**")
    print()
    print("| mode | actions | executed | executed (first-party) | cmd real s |")
    print("| --- | ---: | ---: | ---: | ---: |")
    for mode in sorted(total, key=lambda m: -cpu[m]):
        if not executed[mode] and mode not in ("build", "link", "vet"):
            continue
        print(
            f"| {mode} | {total[mode]} | {executed[mode]} | "
            f"{first_party[mode]} | {cpu[mode]:.1f} |"
        )
    print()
    print(f"test binaries wall: {test_wall:.1f}s")
    print()


if __name__ == "__main__":
    if len(sys.argv) < 3:
        sys.exit(__doc__)
    main(sys.argv)
