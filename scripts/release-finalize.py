#!/usr/bin/env python3
"""Publish only after the tag build and verified hosted evidence are complete."""
import argparse
import importlib.util
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess


def gh(*args):
    return subprocess.check_output(['gh', *args], text=True)


def validate_build(runs, tag, source):
    matches = [run for run in runs if run.get('head_sha') == source and run.get('head_branch') == tag
               and run.get('event') == 'push' and run.get('path') == '.github/workflows/release.yml']
    if len(matches) != 1 or matches[0].get('status') != 'completed' or matches[0].get('conclusion') != 'success':
        raise SystemExit('The exact tag Release workflow must have completed successfully before publication')


def validate_assets(release):
    if not release.get('draft') or release.get('immutable'):
        raise SystemExit('Release must be an unpublished draft')
    assets = release.get('assets', [])
    names = {asset['name'] for asset in assets if asset.get('size', 0) > 0}
    if len(names) != len(assets) or not {'checksums.txt', 'checksums.txt.sig'} <= names:
        raise SystemExit('Missing, empty or duplicate signed release assets')
    version = release['tag_name'].removeprefix('v')
    archives = {f'microfat_{version}_linux_amd64_v{level}.tar.gz' for level in range(1, 5)}
    archives.update(f'microfat_{version}_linux_arm64_{level}.tar.gz' for level in ('v8.0', 'v8.2', 'v9.0'))
    archives.update(f'microfat-stub{profile}_{version}_linux_{arch}_{level}.tar.gz'
                    for profile in ('', '-minimal') for arch, level in (('amd64', 'v1'), ('arm64', 'v8.0')))
    archives.update(f'microfat_linux_{arch}_fat.tar.gz' for arch in ('amd64', 'arm64'))
    required = archives | {name + suffix for name in archives for suffix in ('.spdx.json', '.cyclonedx.json')}
    if not required <= names:
        raise SystemExit(f'Missing archives or SBOMs: {sorted(required - names)}')


def recovery_source(repo, run_id, attempt):
    if os.environ.get('GITHUB_EVENT_NAME') != 'workflow_dispatch' or os.environ.get('GITHUB_REF') != 'refs/heads/main':
        raise SystemExit('Recovery requires explicit dispatch from trusted main')
    if not re.fullmatch(r'[1-9][0-9]*', run_id) or not re.fullmatch(r'[1-9][0-9]*', attempt):
        raise SystemExit('Invalid measurement run or attempt')
    endpoint = f'repos/{repo}/actions/runs/{run_id}/attempts/{attempt}'
    run = json.loads(gh('api', endpoint))
    jobs = json.loads(gh('api', endpoint + '/jobs?per_page=100'))
    spec = importlib.util.spec_from_file_location('evidence', Path(__file__).with_name('benchmark-evidence.py'))
    evidence = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(evidence)
    source = evidence.source_revision(run, jobs, run_id, attempt)
    verified = [job for job in jobs['jobs'] if job['name'] == 'Independently verify hosted release matrix']
    if len(verified) != 1 or verified[0].get('conclusion') != 'success':
        raise SystemExit('Independent archive verification must have succeeded')
    tag = run['head_branch']
    if not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?', tag):
        raise SystemExit('Recovery source must be a release tag')
    commit = json.loads(gh('api', f'repos/{repo}/commits/{tag}'))
    if commit['sha'] != source:
        raise SystemExit('Tag differs from verified measurement source')
    return tag, source


def finalize(root, recovery_run=None, recovery_attempt='1'):
    repo = os.environ['GH_REPO']
    if recovery_run:
        tag, source = recovery_source(repo, recovery_run, recovery_attempt)
    else:
        tag, source = os.environ['RELEASE_TAG'], os.environ['GITHUB_SHA']
    if not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?', tag):
        raise SystemExit('Invalid release tag')
    if not recovery_run and (os.environ.get('GITHUB_EVENT_NAME') != 'push' or os.environ.get('GITHUB_REF') != 'refs/tags/' + tag):
        raise SystemExit('Publication requires a tag push')
    runs = json.loads(gh('api', f'repos/{repo}/actions/workflows/release.yml/runs?event=push&head_sha={source}&per_page=100'))
    validate_build(runs['workflow_runs'], tag, source)
    release_id = int(gh('release', 'view', tag, '--json', 'databaseId', '--jq', '.databaseId'))
    release = json.loads(gh('api', f'repos/{repo}/releases/{release_id}'))
    if release.get('tag_name') != tag:
        raise SystemExit('Release tag mismatch')
    validate_assets(release)
    archives = list(root.glob('*.tar.gz'))
    if len(archives) != 1:
        raise SystemExit('Expected exactly one verified evidence archive')
    archive = archives[0]
    checksum = archive.with_name(archive.name + '.sha256')
    with archive.open('rb') as stream:
        digest = hashlib.file_digest(stream, 'sha256').hexdigest()
    if checksum.read_text().split() != [digest, archive.name]:
        raise SystemExit('Evidence checksum mismatch')
    # A retry may retain an already uploaded asset, but may never replace one.
    existing = {asset['name']: asset for asset in release['assets']}
    for path in (archive, checksum):
        if path.name in existing:
            with path.open('rb') as stream:
                expected = 'sha256:' + hashlib.file_digest(stream, 'sha256').hexdigest()
            if existing[path.name].get('digest') != expected:
                raise SystemExit('Existing evidence asset differs or has no verifiable digest')
        else:
            gh('release', 'upload', tag, str(path))
    gh('release', 'edit', tag, '--draft=false', '--latest')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('root', type=Path)
    parser.add_argument('--recovery-run')
    parser.add_argument('--recovery-attempt', default='1')
    args = parser.parse_args()
    finalize(args.root, args.recovery_run, args.recovery_attempt)
