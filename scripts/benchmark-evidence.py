#!/usr/bin/env python3
"""Independently verify downloaded bundles, replay reports, and audit the declared matrix."""
import argparse
import hashlib
import itertools
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile


def execute(*args):
    return subprocess.check_output([str(Path('bin/microfat').resolve()), 'benchmark', *map(str, args)])


def scenario(exp):
    config = exp['config']
    intensity = {4: 'standard', 32: 'heavy'}.get(config['iterations'])
    return exp['environment']['host']['arch'], config['workload'], intensity


def source_revision(run, jobs, run_id, attempt):
    if not all(re.fullmatch(r'[1-9][0-9]*', value) for value in (run_id, attempt)) or \
       str(run.get('id')) != run_id or str(run.get('run_attempt')) != attempt or \
       run.get('path') != '.github/workflows/benchmarks.yml' or run.get('status') != 'completed' or \
       not re.fullmatch(r'[0-9a-f]{40}', run.get('head_sha', '')):
        raise SystemExit('invalid completed measurement run provenance')
    expected = {f'Benchmark sustained ({arch}, {workload}, {intensity})'
                for arch, workload, intensity in itertools.product(('amd64', 'arm64'), ('mixed', 'cpu', 'memory'), ('standard', 'heavy'))}
    expected.update(f'Benchmark compatibility ({runner})' for runner in ('ubuntu-24.04', 'ubuntu-24.04-arm'))
    measured = [job for job in jobs.get('jobs', []) if job.get('name') in expected]
    if len(measured) != len(expected) or {job['name'] for job in measured} != expected or \
       any(job.get('conclusion') != 'success' for job in measured):
        raise SystemExit('all 12 measurement and both compatibility jobs must pass in the requested attempt')
    return run['head_sha']


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('root', type=Path)
    modes = parser.add_mutually_exclusive_group(required=True)
    modes.add_argument('--calibration', action='store_true')
    modes.add_argument('--archive', action='store_true')
    modes.add_argument('--shard', action='store_true')
    modes.add_argument('--source-cohort', nargs=2, metavar=('RUN', 'ATTEMPT'))
    args = parser.parse_args()
    if args.source_cohort:
        print(source_revision(json.loads((args.root / 'source-run.json').read_text()),
                              json.loads((args.root / 'source-jobs.json').read_text()), *args.source_cohort))
        return
    bundles = sorted(path.parent for path in args.root.rglob('SHA256SUMS'))
    if not bundles:
        parser.error('no completed evidence bundles')
    observed = set()
    verified = []
    measurement_run = os.environ.get('EVIDENCE_RUN_ID', os.environ.get('GITHUB_RUN_ID'))
    measurement_attempt = os.environ.get('EVIDENCE_RUN_ATTEMPT', os.environ.get('GITHUB_RUN_ATTEMPT', '1'))
    for bundle in bundles:
        execute('verify', bundle)
        for fmt, name in (('json', 'report.json'), ('markdown', 'report.md')):
            # The CLI appends exactly one output newline to the renderer bytes.
            # Compare that framing explicitly; never strip or normalize report content.
            if execute('report', '--input', bundle, '--format', fmt) != (bundle / name).read_bytes() + b'\n':
                raise SystemExit(f'offline report replay differs: {bundle}/{name}')
        exp = json.loads((bundle / 'raw.json').read_text())
        if not args.calibration:
            verdict = json.loads(execute('qualify', '--input', bundle, '--policy', 'hosted-release'))
            if not verdict['publishable'] or exp['release_eligible']:
                raise SystemExit('hosted qualification failed')
            key = scenario(exp)
            if key in observed:
                raise SystemExit(f'duplicate release shard: {key}')
            observed.add(key)
            if exp['source_sha'] != os.environ.get('SOURCE_SHA', os.environ.get('GITHUB_SHA', exp['source_sha'])):
                raise SystemExit('source revision differs from requested workflow revision')
            runner = exp.get('runner', {})
            if measurement_run and (runner.get('run_id'), runner.get('attempt')) != (measurement_run, measurement_attempt):
                raise SystemExit('measurement run or attempt differs from the requested evidence cohort')
        verified.append(dict(bundle=str(bundle), source=exp['source_sha'],
                             checksum_manifest=hashlib.sha256((bundle / 'SHA256SUMS').read_bytes()).hexdigest()))
    if args.calibration:
        paths = Path('.work/calibration-bundles.json')
        paths.write_text(json.dumps([str(p.resolve()) for p in bundles]))
        policy = execute('calibrate', '--input', paths)
        Path('.work/calibration-policy.json').write_bytes(policy + b'\n')
        print(policy.decode())
    elif args.archive:
        expected = set(itertools.product(('amd64', 'arm64'), ('mixed', 'cpu', 'memory'), ('standard', 'heavy')))
        if observed != expected:
            raise SystemExit(f'release matrix mismatch; missing={expected-observed}, unexpected={observed-expected}')
        archive = Path('.work') / ('benchmark-evidence-' + os.environ['SOURCE_SHA'] + '-' + os.environ['GITHUB_RUN_ID'] + '-' + os.environ.get('GITHUB_RUN_ATTEMPT', '1') + '.tar.gz')
        manifest = args.root / 'verified-manifest.json'
        manifest.write_text(json.dumps(dict(evidence_class='hosted-comparative',
                            measurement_run_id=measurement_run, measurement_attempt=measurement_attempt,
                            verification_run_id=os.environ['GITHUB_RUN_ID'],
                            verification_attempt=os.environ.get('GITHUB_RUN_ATTEMPT', '1'),
                            verification_source_sha=os.environ.get('GITHUB_SHA', os.environ['SOURCE_SHA']),
                            limitation='Shared hosted VMs; no dedicated-hardware performance certification.', bundles=verified), indent=2)+'\n')
        with tarfile.open(archive, 'x:gz') as output:
            output.add(manifest, arcname='verified-manifest.json')
            for index, bundle in enumerate(bundles):
                output.add(bundle, arcname=f'bundles/{index:02d}')
        archive.with_suffix(archive.suffix + '.sha256').write_text(hashlib.sha256(archive.read_bytes()).hexdigest()+'  '+archive.name+'\n')
    if summary := os.environ.get('GITHUB_STEP_SUMMARY'):
        with open(summary, 'a') as output:
            output.write(f'\nVerified {len(bundles)} evidence bundles with byte-identical offline report replay.\n')
            if not args.calibration:
                output.write('Hosted comparative evidence; dedicated-hardware qualification remains false.\n')


if __name__ == '__main__':
    main()
