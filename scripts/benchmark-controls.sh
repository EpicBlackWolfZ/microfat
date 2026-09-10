#!/usr/bin/env bash
# Delegate only a transient, benchmark-owned subtree; leave the workflow runner outside it.
set -euo pipefail
output="${1:?output controls JSON required}"
root="/sys/fs/cgroup/microfat-ci-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-1}-$$"
test "$(stat -fc %T /sys/fs/cgroup)" = cgroup2fs
sudo mkdir "$root"
trap 'sudo rmdir "$root" 2>/dev/null || true' ERR
printf '+cpu +memory\n' | sudo tee "$root/cgroup.subtree_control" >/dev/null
sudo chown "$(id -u):$(id -g)" "$root" "$root/cgroup.procs" "$root/cgroup.subtree_control"
python3 - "$root" "$output" <<'PY'
import json,os,pathlib,sys
root,out=sys.argv[1:]
cpus=sorted(os.sched_getaffinity(0))
if len(cpus)<2:
    raise SystemExit('at least two allowed vCPUs required')
def options(cpu):
    return dict(affinity=[cpu],cgroup_root=root,version='v2',cpu_quota_us=100000,cpu_period_us=100000,memory_bytes=2*1024**3)
pathlib.Path(out).parent.mkdir(parents=True,exist_ok=True)
pathlib.Path(out).write_text(json.dumps(dict(target=options(cpus[0]),generator=options(cpus[-1])),indent=2)+'\n')
with open(os.environ.get('GITHUB_ENV','/dev/null'),'a') as f:
    f.write(f'MICROFAT_BENCH_CGROUP_ROOT={root}\nMICROFAT_BENCH_CGROUP_VERSION=v2\nMICROFAT_BENCH_REQUIRE_CONTROLS=1\n')
print(root)
PY
