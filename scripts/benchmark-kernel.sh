#!/usr/bin/env bash
# Functional cgroup v1 qualification in a networkless software-emulated Linux guest.
set -euo pipefail
root="$PWD/.work/kernel-v1"
mkdir -p "$root/rootfs/bin" "$root/rootfs/proc" "$root/rootfs/sys" "$root/rootfs/dev" "$root/rootfs/tmp"
python3 scripts/benchmark-kernel-lock.py "$root"
dpkg-deb -x "$root/kernel.deb" "$root/kernel"
dpkg-deb -x "$root/modules.deb" "$root/kernel"
cp /bin/busybox "$root/rootfs/bin/busybox"
CGO_ENABLED=0 go test -c -o "$root/rootfs/system.test" ./benchmarks/system
cat > "$root/rootfs/init" <<'INIT'
#!/bin/busybox sh
export PATH=/bin
/bin/busybox --install -s /bin
set -e
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev
mount -t tmpfs tmpfs /sys/fs/cgroup
mkdir -p /sys/fs/cgroup/cpu /sys/fs/cgroup/memory
mount -t cgroup -o cpu,cpuacct cpu /sys/fs/cgroup/cpu || poweroff -f
mount -t cgroup -o memory memory /sys/fs/cgroup/memory || poweroff -f
export MICROFAT_BENCH_CGROUP_ROOT=/sys/fs/cgroup/cpu
export MICROFAT_BENCH_MEMORY_ROOT=/sys/fs/cgroup/memory
export MICROFAT_BENCH_CGROUP_VERSION=v1
export MICROFAT_BENCH_REQUIRE_CONTROLS=1
uname -a
cat /proc/cgroups
status=0
/system.test -test.v -test.run='^Test(KernelControls|RealCgroupIntegration)$' -test.timeout=180s || status=$?
echo "MICROFAT_KERNEL_RESULT=$status"
sync
poweroff -f
INIT
chmod +x "$root/rootfs/init"
(cd "$root/rootfs" && find . -print0 | cpio --null -o --format=newc 2>/dev/null | gzip -1 > "$root/initramfs.gz")
qemu-system-x86_64 --version > "$root/emulator.txt"
cp benchmarks/kernel.lock.json "$root/lock.json"
cp "$root/kernel"/boot/config-* "$root/kernel-config.txt"
sha256sum "$root/rootfs/system.test" "$root/initramfs.gz" > "$root/guest-sha256.txt"
timeout 240 qemu-system-x86_64 -accel tcg -m 512 -smp 2 -nodefaults -no-reboot -nographic \
  -serial stdio -monitor none -nic none -kernel "$root/kernel"/boot/vmlinuz-* -initrd "$root/initramfs.gz" \
  -append 'console=ttyS0 rdinit=/init panic=-1 cgroup_enable=memory' > "$root/serial.log" 2>&1
cat "$root/serial.log"
python3 - "$root/serial.log" <<'PY'
import pathlib,sys
text=pathlib.Path(sys.argv[1]).read_text()
if text.count('MICROFAT_KERNEL_RESULT=0')!=1 or '--- SKIP:' in text or '--- FAIL:' in text:
    raise SystemExit('required real-kernel tests failed, skipped, or did not complete')
PY
