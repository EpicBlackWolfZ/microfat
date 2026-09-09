#!/usr/bin/env python3
"""Union atomic Go profiles by source block, rejecting inconsistent instrumentation."""
import sys
import os
from decimal import Decimal
from pathlib import Path


def merge(paths):
    blocks = {}
    layouts = {}
    for path in paths:
        lines = Path(path).read_text().splitlines()
        if not lines or lines[0] != "mode: atomic":
            raise ValueError(f"{path}: expected atomic coverage")
        current = {}
        for line in lines[1:]:
            location, statements, count = line.split()
            statements, count = int(statements), int(count)
            if statements < 0 or count < 0:
                raise ValueError(f"{path}: invalid block {location}")
            current[location] = statements
            previous = blocks.get(location)
            if previous is not None and previous[0] != statements:
                raise ValueError(f"{path}: inconsistent block {location}")
            blocks[location] = (statements, max(count, previous[1] if previous else 0))
        files = {}
        for location, statements in current.items():
            filename = location.rsplit(":", 1)[0]
            files.setdefault(filename, {})[location] = statements
        for filename, layout in files.items():
            if filename in layouts and layouts[filename] != layout:
                raise ValueError(f"{path}: incompatible source layout for {filename}")
            layouts[filename] = layout
    if not blocks:
        raise ValueError("empty coverage input")
    return "mode: atomic\n" + "".join(
        f"{location} {statements} {count}\n"
        for location, (statements, count) in sorted(blocks.items())
    )


def coverage_counts(profile):
    covered = total = 0
    for row in profile.splitlines()[1:]:
        _, statements, count = row.split()
        total += int(statements)
        if int(count) > 0:
            covered += int(statements)
    return covered, total


def meets_threshold(covered, total, threshold):
    return total > 0 and Decimal(covered * 100) >= Decimal(str(threshold)) * total


if __name__ == "__main__":
    if len(sys.argv) < 4:
        raise SystemExit("usage: coverage.py OUTPUT DEFAULT_PROFILE MINIMAL_PROFILE")
    profile = merge(sys.argv[2:])
    Path(sys.argv[1]).write_text(profile)
    covered, total = coverage_counts(profile)
    threshold = os.environ.get("COVERAGE_THRESHOLD", "95")
    print(f"Exact coverage: {covered}/{total} statements ({100 * covered / total:.6f}%)")
    if not meets_threshold(covered, total, threshold):
        raise SystemExit(f"coverage below {threshold}% (without rounding)")
