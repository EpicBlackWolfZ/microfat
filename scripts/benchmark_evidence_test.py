#!/usr/bin/env python3
"""Exercise archival rejection and replay contracts without treating fixtures as measurements."""
import contextlib
import importlib.util
import io
import itertools
import json
import os
from pathlib import Path
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
SPEC = importlib.util.spec_from_file_location('benchmark_evidence', Path(__file__).with_name('benchmark-evidence.py'))
EVIDENCE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(EVIDENCE)


class EvidenceTests(unittest.TestCase):
    def setup_bundles(self, count=12):
        root = Path('evidence')
        for index, (arch, workload, intensity) in enumerate(itertools.product(('amd64', 'arm64'), ('mixed', 'cpu', 'memory'), (4, 32))):
            if index >= count:
                break
            bundle = root / str(index)
            bundle.mkdir(parents=True)
            (bundle / 'SHA256SUMS').write_text('synthetic checksum input')
            (bundle / 'report.json').write_bytes(b'json')
            (bundle / 'report.md').write_bytes(b'markdown')
            (bundle / 'raw.json').write_text(json.dumps(dict(source_sha='source', release_eligible=False,
                runner=dict(run_id='1', attempt='1'),
                environment=dict(host=dict(arch=arch)), config=dict(workload=workload, iterations=intensity))))
        return root

    @staticmethod
    def execute(*args):
        if args[0] == 'report':
            return args[-1].encode() + b'\n'
        if args[0] == 'qualify':
            return b'{"publishable":true}'
        return b''

    def invoke(self, root, **environment):
        env = dict(SOURCE_SHA='source', GITHUB_RUN_ID='1')
        env.update(environment)
        with patch.object(sys, 'argv', ['benchmark-evidence.py', str(root), '--archive']), \
             patch.dict(os.environ, env, clear=True), \
             patch.object(EVIDENCE, 'execute', side_effect=self.execute):
            EVIDENCE.main()

    def test_complete_archive_and_no_replacement(self):
        with tempfile.TemporaryDirectory() as temporary, contextlib.chdir(temporary):
            Path('.work').mkdir()
            root = self.setup_bundles()
            self.invoke(root)
            archive = next(Path('.work').glob('*.tar.gz'))
            with tarfile.open(archive) as data:
                self.assertIn('verified-manifest.json', data.getnames())
            self.assertTrue(archive.with_suffix('.gz.sha256').exists())
            with self.assertRaises(FileExistsError):
                self.invoke(root)

    def test_missing_and_invalid_evidence(self):
        for failure in ('missing', 'replay', 'newline', 'source', 'duplicate', 'strict-claim', 'run', 'attempt'):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as temporary, contextlib.chdir(temporary):
                Path('.work').mkdir()
                root = self.setup_bundles(11 if failure == 'missing' else 12)
                raw = root / '0/raw.json'
                exp = json.loads(raw.read_text())
                if failure == 'replay':
                    (root / '0/report.json').write_text('corrupted')
                if failure == 'newline':
                    (root / '0/report.json').write_text('json\n')
                if failure == 'source':
                    exp['source_sha'] = 'wrong'
                if failure == 'strict-claim':
                    exp['release_eligible'] = True
                if failure == 'duplicate':
                    exp = json.loads((root / '1/raw.json').read_text())
                if failure == 'run':
                    exp['runner']['run_id'] = 'other'
                if failure == 'attempt':
                    exp['runner']['attempt'] = '2'
                raw.write_text(json.dumps(exp))
                with self.assertRaises(SystemExit):
                    self.invoke(root)
                self.assertFalse(list(Path('.work').glob('*.tar.gz')))

    def test_reverification_preserves_measurement_cohort(self):
        with tempfile.TemporaryDirectory() as temporary, contextlib.chdir(temporary):
            Path('.work').mkdir()
            root = self.setup_bundles()
            self.invoke(root, GITHUB_RUN_ID='2', GITHUB_SHA='verifier', EVIDENCE_RUN_ID='1', EVIDENCE_RUN_ATTEMPT='1')
            archive = next(Path('.work').glob('*-2-1.tar.gz'))
            with tarfile.open(archive) as data:
                manifest = json.load(data.extractfile('verified-manifest.json'))
            self.assertEqual('1', manifest['measurement_run_id'])
            self.assertEqual('1', manifest['measurement_attempt'])
            self.assertEqual('2', manifest['verification_run_id'])
            self.assertEqual('verifier', manifest['verification_source_sha'])

    def test_empty_evidence(self):
        with tempfile.TemporaryDirectory() as temporary, contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit):
                self.invoke(Path(temporary))


if __name__ == '__main__':
    unittest.main()
