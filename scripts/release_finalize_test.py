#!/usr/bin/env python3
"""Publication must not freeze an incomplete or unverified release."""
import hashlib
import importlib.util
import json
import itertools
import os
import sys
import subprocess
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
SPEC = importlib.util.spec_from_file_location('release_finalize', Path(__file__).with_name('release-finalize.py'))
RELEASE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RELEASE)


def assets():
    names = ['checksums.txt', 'checksums.txt.sig']
    archives = ['microfat_0.2.3_linux_amd64.tar.gz', 'microfat_0.2.3_linux_arm64.tar.gz']
    names += [name + suffix for name in archives for suffix in ('', '.spdx.json', '.cyclonedx.json')]
    return [dict(name=name, size=1) for name in names]


class PublicationTests(unittest.TestCase):
    def test_recovery_requires_successful_exact_tag_cohort(self):
        run = dict(id=1, run_attempt=1, head_sha='a' * 40, head_branch='v0.2.3', status='completed',
                   path='.github/workflows/benchmarks.yml')
        names = [f'Benchmark sustained ({a}, {w}, {i})' for a, w, i in
                 itertools.product(('amd64', 'arm64'), ('mixed', 'cpu', 'memory'), ('standard', 'heavy'))]
        names += [f'Benchmark compatibility ({r})' for r in ('ubuntu-24.04', 'ubuntu-24.04-arm')]
        names += ['Independently verify hosted release matrix']
        for failure in (None, 'measurement', 'verification', 'tag', 'branch', 'event', 'attempt'):
            with self.subTest(failure=failure):
                jobs = dict(jobs=[dict(name=name, conclusion='success') for name in names])
                source = dict(run)
                if failure == 'measurement':
                    jobs['jobs'][0]['conclusion'] = 'failure'
                if failure == 'verification':
                    jobs['jobs'][-1]['conclusion'] = 'failure'
                if failure == 'branch':
                    source['head_branch'] = 'main'
                if failure == 'attempt':
                    source['run_attempt'] = 2

                def gh(*args):
                    if '/jobs?' in args[1]:
                        return json.dumps(jobs)
                    if '/commits/' in args[1]:
                        return json.dumps(dict(sha='b' * 40 if failure == 'tag' else 'a' * 40))
                    return json.dumps(source)

                env = dict(GITHUB_EVENT_NAME='push' if failure == 'event' else 'workflow_dispatch',
                           GITHUB_REF='refs/heads/main')
                with patch.dict(os.environ, env), patch.object(RELEASE, 'gh', side_effect=gh):
                    if failure:
                        with self.assertRaises(SystemExit):
                            RELEASE.recovery_source('owner/repo', '1', '1')
                    else:
                        self.assertEqual(('v0.2.3', 'a' * 40), RELEASE.recovery_source('owner/repo', '1', '1'))


    def test_publication_requires_complete_build_and_assets(self):
        for failure in (None, 'failed-build', 'running-build', 'wrong-source', 'wrong-tag', 'wrong-event',
                        'published', 'missing-sbom', 'empty-signature', 'checksum', 'upload', 'asset-conflict'):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                archive = root / 'evidence.tar.gz'
                archive.write_bytes(b'fixture')
                digest = hashlib.sha256(b'fixture').hexdigest()
                archive.with_name(archive.name + '.sha256').write_text(f'{digest}  {archive.name}\n')
                run = dict(head_sha='a' * 40, head_branch='v0.2.3', event='push',
                           path='.github/workflows/release.yml', status='completed', conclusion='success')
                release = dict(draft=True, tag_name='v0.2.3', assets=assets())
                if failure == 'failed-build':
                    run['conclusion'] = 'failure'
                if failure == 'running-build':
                    run['status'] = 'in_progress'
                if failure == 'wrong-source':
                    run['head_sha'] = 'b' * 40
                if failure == 'wrong-tag':
                    run['head_branch'] = 'v0.2.4'
                if failure == 'published':
                    release['draft'] = False
                if failure == 'missing-sbom':
                    release['assets'].pop()
                if failure == 'empty-signature':
                    release['assets'][1]['size'] = 0
                if failure == 'checksum':
                    archive.write_bytes(b'changed')
                if failure == 'asset-conflict':
                    release['assets'].append(dict(name=archive.name, size=1, digest='sha256:wrong'))
                calls = []

                def gh(*args):
                    calls.append(args)
                    if args[:2] == ('release', 'view'):
                        return '123'
                    if args[0] == 'api':
                        return json.dumps(dict(workflow_runs=[run]) if '/actions/' in args[1] else release)
                    if args[:2] == ('release', 'upload') and failure == 'upload':
                        raise RuntimeError('upload failed')
                    return ''

                env = dict(RELEASE_TAG='v0.2.3', GITHUB_SHA='a' * 40, GH_REPO='owner/repo',
                           GITHUB_EVENT_NAME='workflow_dispatch' if failure == 'wrong-event' else 'push',
                           GITHUB_REF='refs/tags/v0.2.3')
                with patch.dict(os.environ, env), patch.object(RELEASE, 'gh', side_effect=gh):
                    if failure:
                        with self.assertRaises((SystemExit, RuntimeError)):
                            RELEASE.finalize(root)
                        self.assertFalse(any(call[:2] == ('release', 'edit') for call in calls))
                    else:
                        RELEASE.finalize(root)
                        self.assertEqual(('release', 'edit', 'v0.2.3', '--draft=false', '--latest'), calls[-1])
                        self.assertEqual(2, sum(call[:2] == ('release', 'upload') for call in calls))


if __name__ == '__main__':
    unittest.main()
