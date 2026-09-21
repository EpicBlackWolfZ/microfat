#!/usr/bin/env python3
"""Regression checks for the audit's fail-closed trust and crash boundaries."""
import hashlib
import importlib.util
import io
import itertools
import json
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock, patch

sys.dont_write_bytecode = True
SPEC = importlib.util.spec_from_file_location("audit_release", Path(__file__).with_name("audit-release.py"))
AUDIT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(AUDIT)


class AuditTests(unittest.TestCase):
    def test_corrupt_product_must_reject_without_crashing(self):
        for code, stderr, accepted in ((1, "invalid checksum", True), (0, "", False),
                                       (-11, "", False), (2, "panic: invalid index", False),
                                       (2, "fatal error: out of memory", False)):
            with self.subTest(code=code, stderr=stderr), tempfile.TemporaryDirectory() as directory:
                commands = AUDIT.Commands(Path(directory))
                result = subprocess.CompletedProcess(["product"], code, "", stderr)
                with patch.object(AUDIT.subprocess, "run", return_value=result):
                    if accepted:
                        commands.run(["product"], success=False)
                    else:
                        with self.assertRaises(ValueError):
                            commands.run(["product"], success=False)

    def test_signature_and_complete_manifest_precede_extraction(self):
        for failure in (None, "signature", "missing", "duplicate", "tampered", "symlink", "schema"):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                args = SimpleNamespace(dist=root, version="0.2.4", source="a" * 40)
                names = [f"microfat_0.2.4_linux_{arch}.tar.gz{suffix}" for arch, suffix in
                         itertools.product(("amd64", "arm64"), ("", ".spdx.json", ".cyclonedx.json"))]
                rows = []
                for name in names:
                    data = b"asset"
                    if name.endswith(".spdx.json"):
                        data = json.dumps({"spdxVersion": "SPDX-2.3"}).encode()
                    if name.endswith(".cyclonedx.json"):
                        data = json.dumps({"bomFormat": "CycloneDX", "specVersion": "1.5"}).encode()
                        if failure == "schema":
                            data = json.dumps({"spdxVersion": "SPDX-2.3"}).encode()
                    (root / name).write_bytes(data)
                    rows.append(hashlib.sha256(data).hexdigest() + "  " + name)
                if failure == "missing":
                    rows.pop()
                if failure == "duplicate":
                    rows[-1] = rows[0]
                if failure == "tampered":
                    (root / names[0]).write_bytes(b"changed")
                if failure == "symlink":
                    (root / "target").write_bytes(b"asset")
                    (root / names[0]).unlink()
                    (root / names[0]).symlink_to("target")
                (root / "checksums.txt").write_text("\n".join(rows) + "\n")
                commands = Mock()
                if failure == "signature":
                    commands.run.side_effect = subprocess.CalledProcessError(1, ["cosign"])
                if failure:
                    with self.assertRaises((ValueError, subprocess.CalledProcessError)):
                        AUDIT.authenticate(args, commands)
                else:
                    self.assertEqual(set(names), set(AUDIT.authenticate(args, commands)))
                commands.run.assert_called_once()
                self.assertIn("--certificate-github-workflow-sha", commands.run.call_args.args[0])

    def test_archive_rejects_links_traversal_and_duplicate_entries(self):
        for name, kind in (("../escape", "file"), ("/absolute", "file"), ("link", "symlink"), ("same", "duplicate")):
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                args = SimpleNamespace(dist=root, output=root / "output", version="0.2.4", arch="amd64")
                args.output.mkdir()
                with tarfile.open(root / "microfat_0.2.4_linux_amd64.tar.gz", "w:gz") as archive:
                    member = tarfile.TarInfo(name)
                    if kind == "symlink":
                        member.type, member.linkname = tarfile.SYMTYPE, "../escape"
                        archive.addfile(member)
                    else:
                        member.size = 1
                        archive.addfile(member, io.BytesIO(b"x"))
                        if kind == "duplicate":
                            archive.addfile(member, io.BytesIO(b"x"))
                with self.assertRaises(ValueError):
                    AUDIT.extract(args)
                self.assertEqual([], list((args.output / "products").iterdir()))


if __name__ == "__main__":
    unittest.main()
