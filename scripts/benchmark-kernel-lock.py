#!/usr/bin/env python3
"""Verify the locked kernel against Ubuntu-signed repository metadata before booting it."""
import gzip
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import urllib.request


def download(url, output):
    with urllib.request.urlopen(url, timeout=60) as response:
        output.write_bytes(response.read())


def main():
    output = Path(sys.argv[1])
    lock = json.loads(Path('benchmarks/kernel.lock.json').read_text())
    base = 'https://security.ubuntu.com/ubuntu/'
    distribution = base + 'dists/' + lock['suite'] + '/'
    release = output / 'InRelease'
    download(distribution + 'InRelease', release)
    subprocess.run(['gpgv', '--keyring', '/usr/share/keyrings/ubuntu-archive-keyring.gpg', str(release)], check=True)
    relative = lock['component'] + '/binary-' + lock['architecture'] + '/Packages.gz'
    signed = release.read_text().split('SHA256:\n', 1)[1].split('\nSHA', 1)[0]
    expected = next(line.split()[0] for line in signed.splitlines() if line.split() and line.split()[-1] == relative)
    packages = output / 'Packages.gz'
    download(distribution + relative, packages)
    if hashlib.sha256(packages.read_bytes()).hexdigest() != expected:
        raise SystemExit('repository index checksum mismatch')
    blocks = gzip.decompress(packages.read_bytes()).decode().split('\n\n')
    entries = [dict(line.split(': ', 1) for line in block.splitlines() if ': ' in line and not line.startswith(' '))
               for block in blocks]
    package = next(entry for entry in entries if entry.get('Package') == lock['package'] and entry.get('Version') == lock['version'])
    if package['SHA256'] != lock['sha256'] or base + package['Filename'] != lock['url']:
        raise SystemExit('kernel lock differs from signed metadata')
    archive = output / 'kernel.deb'
    download(lock['url'], archive)
    if hashlib.sha256(archive.read_bytes()).hexdigest() != lock['sha256']:
        raise SystemExit('kernel package checksum mismatch')


if __name__ == '__main__':
    main()
