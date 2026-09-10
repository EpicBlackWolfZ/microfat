#!/usr/bin/env python3
"""Correct only unpublished v0.2.3 SBOM metadata; preserve signed binary bytes and the tag."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys

TAG = 'v0.2.3'
SOURCE = 'c0895b7a8426879bd952a20cc3c5f23f3300f8f5'


def run(*args):
    return subprocess.check_output(args, text=True)


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def verify_files(root, manifest):
    seen = set()
    for line in manifest.splitlines():
        expected, name = line.split()
        if Path(name).name != name or name in seen or digest(root / name) != expected:
            raise SystemExit('Original signed checksum mismatch or unsafe manifest')
        seen.add(name)
    if len(seen) != 35 or len([name for name in seen if name.endswith('.tar.gz')]) != 13:
        raise SystemExit('Unexpected original v0.2.3 manifest')


def validate_sboms(root):
    for suffix, key, value in (('.spdx.json', 'spdxVersion', 'SPDX-2.3'), ('.cyclonedx.json', 'bomFormat', 'CycloneDX')):
        files = list(root.glob('*.tar.gz' + suffix))
        if len(files) != 13 or any(json.loads(path.read_text()).get(key) != value for path in files):
            raise SystemExit('Missing, malformed or mislabeled SBOMs')


def require_draft(release):
    if release.get('tag_name') != TAG or not release.get('draft') or release.get('immutable'):
        raise SystemExit('Only the unpublished v0.2.3 draft may be corrected')
    if any(asset['name'] == 'checksums.txt.sig' for asset in release.get('assets', [])):
        raise SystemExit('Primary signature must be withheld to block concurrent publication')


def main(root):
    if os.environ.get('GITHUB_EVENT_NAME') != 'workflow_dispatch' or os.environ.get('GITHUB_REF') != 'refs/heads/main':
        raise SystemExit('Repair requires explicit dispatch from the trusted main branch')
    repo = os.environ['GH_REPO']
    release_id = int(run('gh', 'release', 'view', TAG, '--json', 'databaseId', '--jq', '.databaseId'))
    endpoint = f'repos/{repo}/releases/{release_id}'
    release = json.loads(run('gh', 'api', endpoint))
    require_draft(release)
    commit = json.loads(run('gh', 'api', f'repos/{repo}/commits/{TAG}'))
    if commit['sha'] != SOURCE:
        raise SystemExit('Unexpected tag source')
    root.mkdir()
    run('gh', 'release', 'download', TAG, '--dir', str(root))
    identity = f'https://github.com/{repo}/.github/workflows/release.yml@refs/tags/{TAG}'
    run('cosign', 'verify-blob', '--bundle', str(root / 'original-checksums.txt.sig'),
        '--certificate-identity', identity, '--certificate-oidc-issuer', 'https://token.actions.githubusercontent.com',
        str(root / 'original-checksums.txt'))
    original = (root / 'original-checksums.txt').read_text()
    verify_files(root, original)
    archives = sorted(root.glob('microfat*.tar.gz'))
    before = {path.name: digest(path) for path in archives}
    changed = []
    for archive in archives:
        target = archive.with_name(archive.name + '.cyclonedx.json')
        run('syft', str(archive), '--output', 'cyclonedx-json=' + str(target))
        changed.append(target)
    validate_sboms(root)
    if before != {path.name: digest(path) for path in archives}:
        raise SystemExit('Archive bytes changed during metadata correction')
    correction = root / 'sbom-correction.json'
    correction.write_text(json.dumps(dict(tag=TAG, source_sha=SOURCE, archive_sha256=before,
        workflow_run=os.environ['GITHUB_RUN_ID'], workflow_source_sha=os.environ['GITHUB_SHA'],
        original_checksums=original, original_signature_bundle=json.loads((root / 'original-checksums.txt.sig').read_text()),
        reason='CycloneDX filenames previously contained SPDX; archive bytes and tag are unchanged.'), indent=2) + '\n')
    files = sorted(archives + list(root.glob('*.tar.gz.*.json')) + [correction])
    checksums = root / 'checksums.txt'
    checksums.write_text(''.join(f'{digest(path)}  {path.name}\n' for path in files))
    signature = root / 'checksums.txt.sig'
    run('cosign', 'sign-blob', '--yes', '--bundle=' + str(signature), str(checksums))
    require_draft(json.loads(run('gh', 'api', endpoint)))
    run('gh', 'release', 'upload', TAG, '--clobber', *map(str, changed + [correction, checksums]))
    note = root / 'release-notes.md'
    note.write_text(release['body'] + '\n\n### SBOM metadata correction before publication\n\n'
        'CycloneDX SBOMs were regenerated from the original signed archives; the tag and binary bytes are unchanged. '
        'The corrected checksum manifest also covers all SBOMs and `sbom-correction.json`, which retains the original signed manifest. '
        f'[Correction workflow](https://github.com/{repo}/actions/runs/{os.environ["GITHUB_RUN_ID"]}).\n\n'
        'Verify `checksums.txt.sig` with Cosign issuer `https://token.actions.githubusercontent.com` and identity '
        f'`https://github.com/{repo}/.github/workflows/release-sbom-repair.yml@refs/heads/main`.\n')
    run('gh', 'release', 'edit', TAG, '--notes-file', str(note))
    for name in ('original-checksums.txt', 'original-checksums.txt.sig'):
        run('gh', 'release', 'delete-asset', TAG, name, '--yes')
    # Restore the required signature last, after every other corrected asset is uploaded.
    run('gh', 'release', 'upload', TAG, str(signature))
    print('Corrected SBOM metadata and signed manifest; release remains a draft for evidence completion.')


if __name__ == '__main__':
    main(Path(sys.argv[1]))
