#!/usr/bin/env python3
"""Exercise suite enumeration and local/CI wrapper failure propagation without workloads."""
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

sys.dont_write_bytecode = True

SPEC = importlib.util.spec_from_file_location("benchmark_matrix", Path(__file__).with_name("benchmark-matrix.py"))
MATRIX = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MATRIX)
REPOSITORY = Path(__file__).resolve().parents[1]


class MatrixTests(unittest.TestCase):
    def test_supported_product(self):
        cases = list(MATRIX.cases("compatibility", "mixed"))
        self.assertEqual(16, len(cases))
        self.assertEqual(16, len({case["name"] for case in cases}))
        for case in cases:
            self.assertIn(case["format"], (1, 2))
            if case["dictionary"]:
                self.assertEqual("zstd", case["codec"])

    def test_sustained_shards(self):
        for tier in ("nightly", "release"):
            for workload in ("mixed", "cpu", "memory"):
                with self.subTest(tier=tier, workload=workload):
                    cases = list(MATRIX.cases(tier, workload))
                    self.assertEqual(2, len(cases))
                    self.assertEqual({4, 32}, {case["iterations"] for case in cases})
                    self.assertTrue(all(case["workload"] == workload for case in cases))

    def test_release_requires_explicit_runner(self):
        result = subprocess.run(["python3", "scripts/benchmark-matrix.py", "release"],
                                cwd=REPOSITORY, capture_output=True, text=True, check=False)
        self.assertNotEqual(0, result.returncode)
        self.assertIn("runner_identity", result.stderr)
        result = subprocess.run(["python3", "scripts/benchmark-matrix.py", "release", "--list"],
                                cwd=REPOSITORY, capture_output=True, text=True, check=True)
        self.assertEqual(2, len(json.loads(result.stdout)))

    def test_wrapper_preserves_failed_run(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            tool = root / "microfat"
            tool.write_text('#!/bin/sh\ncase "$2" in\nrun) echo "$TEST_BUNDLE"; exit "$TEST_EXIT";;\n'
                            'verify) exit 0;;\nreport|gate) echo "synthetic summary";;\nesac\n')
            tool.chmod(0o700)
            bundle = root / "bundle"
            bundle.mkdir()
            for status in (0, 7):
                with self.subTest(status=status):
                    output = root / str(status)
                    env = dict(os.environ, BENCHMARK_BINARY=str(tool), BENCHMARK_OUTPUT=str(output),
                               TEST_BUNDLE=str(bundle), TEST_EXIT=str(status), BENCHMARK_BASE="", GITHUB_STEP_SUMMARY="")
                    result = subprocess.run(["bash", "scripts/benchmark-ci.sh"], cwd=REPOSITORY, env=env,
                                            capture_output=True, text=True, check=False)
                    self.assertEqual(status, result.returncode)
                    self.assertEqual("synthetic summary\n", (output / "summary.md").read_text())


if __name__ == "__main__":
    unittest.main()
