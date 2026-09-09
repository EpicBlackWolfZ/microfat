#!/usr/bin/env python3
"""Enumerate supported cases explicitly; preserve successes, failures and raw evidence."""
import argparse
import itertools
import json
import os
import signal
from pathlib import Path
import subprocess
import sys


def cases(tier, workload):
    if tier == "compatibility":
        for version, profile, codec in itertools.product((1, 2), ("full", "minimal"), ("none", "lz4", "zstd")):
            for dictionary in ((False, True) if codec == "zstd" else (False,)):
                yield {"name": f"format{version}-{profile}-{codec}-dict{int(dictionary)}", "format": version,
                       "profile": profile, "codec": codec, "dictionary": dictionary, "workload": "mixed",
                       "blocks": 1, "duration_ms": 200, "warmup_ms": 0}
    else:
        for intensity in ("standard", "heavy"):
            yield {"name": f"{tier}-{workload}-{intensity}", "workload": workload,
                   "iterations": 4 if intensity == "standard" else 32,
                   "blocks": 20 if tier == "release" else 10,
                   "duration_ms": 30000 if tier == "release" else 3000,
                   "warmup_ms": 10000 if tier == "release" else 1000,
                   "sample_ms": 250 if tier == "release" else 25,
                   "exec_diagnostics": tier == "release"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("tier", choices=("compatibility", "nightly", "release"))
    parser.add_argument("--workload", choices=("mixed", "cpu", "memory"), default="mixed")
    parser.add_argument("--output", type=Path, default=Path(".work/benchmark-matrix"))
    parser.add_argument("--controls", type=Path, help="JSON object with explicit target/generator control settings")
    parser.add_argument("--list", action="store_true", help="Print the complete matrix without execution")
    args = parser.parse_args()
    matrix = list(cases(args.tier, args.workload))
    controls = json.loads(args.controls.read_text()) if args.controls else {}
    if set(controls) - {"target", "generator", "runner_identity"}:
        parser.error("control file may contain only target, generator and runner_identity settings")
    if args.tier == "release" and not args.list and not controls.get("runner_identity"):
        parser.error("release measurement requires an explicit qualified runner_identity and resource controls")
    if args.list:
        print(json.dumps(matrix, indent=2))
        return 0
    args.output.mkdir(parents=True, exist_ok=True)
    (args.output / "schedule.json").write_text(json.dumps(matrix, indent=2) + "\n")
    outcomes = []
    for scenario in matrix:
        scenario.update(controls)
        config = args.output / (scenario["name"] + ".json")
        config.write_text(json.dumps(scenario, indent=2) + "\n")
        env = dict(os.environ, BENCHMARK_CONFIG=str(config.resolve()),
                   BENCHMARK_OUTPUT=str((args.output / scenario["name"]).resolve()))
        try:
            with subprocess.Popen(["bash", "scripts/benchmark-ci.sh"], env=env, start_new_session=True) as run:
                try:
                    status = run.wait(timeout=6 * 60 * 60)
                except subprocess.TimeoutExpired:
                    # Ask the harness to finish cancelled evidence and reap its process groups.
                    os.killpg(run.pid, signal.SIGTERM)
                    try:
                        run.wait(timeout=30)
                    except subprocess.TimeoutExpired:
                        os.killpg(run.pid, signal.SIGKILL)
                        run.wait()
                    raise
            outcome = {"scenario": scenario["name"], "exit_code": status}
            if args.tier == "release" and status == 0:
                bundle = Path((args.output / scenario["name"] / "latest-bundle.txt").read_text().strip())
                evidence = json.loads((bundle / "raw.json").read_text())
                if not evidence["release_eligible"]:
                    outcome.update(exit_code=1, reason="experiment did not meet release qualification")
        except subprocess.TimeoutExpired:
            outcome = {"scenario": scenario["name"], "exit_code": 1, "reason": "matrix case timeout"}
        outcomes.append(outcome)
        (args.output / "outcomes.json").write_text(json.dumps(outcomes, indent=2) + "\n")
    return int(any(item["exit_code"] != 0 for item in outcomes))


if __name__ == "__main__":
    sys.exit(main())
