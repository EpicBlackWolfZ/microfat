#!/usr/bin/env python3
"""Publish only after the tag build and verified hosted evidence are complete."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys


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


def finalize(root):
    tag, source, repo = os.environ['RELEASE_TAG'], os.environ['GITHUB_SHA'], os.environ['GH_REPO']
    if not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?', tag):
        raise SystemExit('Invalid release tag')
    if os.environ.get('GITHUB_EVENT_NAME') != 'push' or os.environ.get('GITHUB_REF') != 'refs/tags/' + tag:
        raise SystemExit('Publication requires a tag push')
    runs = json.loads(gh('api', f'repos/{repo}/actions/workflows/release.yml/runs?event=push&head_sha={source}&per_page=100'))
    validate_build(runs['workflow_runs'], tag, source)
    release = json.loads(gh('api', f'repos/{repo}/releases/tags/{tag}'))
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
    finalize(Path(sys.argv[1]))
