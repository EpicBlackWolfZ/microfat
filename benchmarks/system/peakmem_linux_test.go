//go:build linux

package system

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testParentBallastBytes     = 64 * 1024 * 1024
	testPageStep               = 4096
	testStaticPayloadBytes     = 64 * 1024
	testIsolatedRSSCeiling     = 8 * 1024 * 1024
	testParentMinExpectedRSS   = 32 * 1024 * 1024
	testPayloadMarker          = "isolated-peak-ok\n"
	testCustomExitCode         = 7
	testArm64InsnBytes         = 4
	testArm64ImmShift          = 5
	testSignalShift            = 0
	testRawSignalKill          = 9
	testTimeoutWindow          = 80 * time.Millisecond
	testIsolatedCleanupTimeout = 5 * time.Second
)

func buildTinyStaticELF(t *testing.T, arch string, exitCode byte, infiniteLoop bool) string {
	t.Helper()
	var machine elf.Machine
	var code []byte
	switch arch {
	case trampolineArchAMD64:
		machine = elf.EM_X86_64
		if infiniteLoop {
			code = []byte{0xeb, 0xfe} // jmp $
		} else {
			code = []byte{
				0xb8, 1, 0, 0, 0, // mov eax, SYS_write
				0xbf, 1, 0, 0, 0, // mov edi, 1
				0x48, 0x8d, 0x35, 0x13, 0, 0, 0, // lea rsi, [rip + 19]
				0xba, byte(len(testPayloadMarker)), 0, 0, 0, // mov edx, len
				0x0f, 0x05, // syscall
				0xb8, 231, 0, 0, 0, // mov eax, SYS_exit_group
				0xbf, exitCode, 0, 0, 0, // mov edi, exitCode
				0x0f, 0x05, // syscall
			}
		}
	case trampolineArchARM64:
		machine = elf.EM_AARCH64
		var insns []uint32
		if infiniteLoop {
			insns = []uint32{0x14000000} // b .
		} else {
			insns = []uint32{
				0xd2800020, // mov x0, #1
				0x100000e1, // adr x1, .+28
				0xd2800002 | (uint32(len(testPayloadMarker)) << testArm64ImmShift), // mov x2, #len
				0xd2800808, // mov x8, #64 (SYS_write)
				0xd4000001, // svc #0
				0xd2800000 | (uint32(exitCode) << testArm64ImmShift), // mov x0, #exitCode
				0xd2800bc8, // mov x8, #94 (SYS_exit_group)
				0xd4000001, // svc #0
			}
		}
		code = make([]byte, len(insns)*testArm64InsnBytes)
		for idx, insn := range insns {
			binary.LittleEndian.PutUint32(code[idx*testArm64InsnBytes:], insn)
		}
	default:
		t.Skipf("static ELF test fixture not defined for %s", arch)
	}

	image := make([]byte, testStaticPayloadBytes)
	copy(image, []byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB)})
	image[elfIdentVersionOffset] = byte(elf.EV_CURRENT)

	put := binary.LittleEndian
	put.PutUint16(image[elfTypeOffset:], uint16(elf.ET_EXEC))
	put.PutUint16(image[elfMachineOffset:], uint16(machine))
	put.PutUint32(image[elfVersionOffset:], uint32(elf.EV_CURRENT))
	put.PutUint64(image[elfEntryOffset:], trampolineLoadAddress+trampolineCodeOffset)
	put.PutUint64(image[elfPhoffOffset:], trampolineELFHeaderSize)
	put.PutUint16(image[elfEhsizeOffset:], trampolineELFHeaderSize)
	put.PutUint16(image[elfPhentsizeOffset:], trampolineProgramHeaderSize)
	put.PutUint16(image[elfPhnumOffset:], 1)

	ph := image[trampolineELFHeaderSize:]
	put.PutUint32(ph, uint32(elf.PT_LOAD))
	put.PutUint32(ph[phFlagsOffset:], uint32(elf.PF_R|elf.PF_X))
	put.PutUint64(ph[phVaddrOffset:], trampolineLoadAddress)
	put.PutUint64(ph[phPaddrOffset:], trampolineLoadAddress)
	put.PutUint64(ph[phFileszOffset:], testStaticPayloadBytes)
	put.PutUint64(ph[phMemszOffset:], testStaticPayloadBytes)
	put.PutUint64(ph[phAlignOffset:], trampolineCodeOffset)

	copy(image[trampolineCodeOffset:], code)
	if !infiniteLoop {
		copy(image[trampolineCodeOffset+len(code):], testPayloadMarker)
	}

	outPath := filepath.Join(t.TempDir(), "tiny-payload")
	require.NoError(t, os.WriteFile(outPath, image, trampolineMode))
	return outPath
}

func TestRunIsolatedEliminatesParentVmHWMInheritance(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("unsupported Linux architecture %s", runtime.GOARCH)
	}

	// Allocate and dirty 64 MiB in the Go test parent so parent VmHWM >= 64 MiB.
	ballast := make([]byte, testParentBallastBytes)
	for offset := 0; offset < len(ballast); offset += testPageStep {
		ballast[offset] = byte(offset>>8) ^ 0x5a
	}

	trampolinePath, err := WriteTrampolineELF(t.TempDir(), runtime.GOARCH)
	require.NoError(t, err)
	payloadPath := buildTinyStaticELF(t, runtime.GOARCH, 0, false)

	// 1. Direct child via standard os/exec inherits the Go parent's CLONE_VFORK|CLONE_VM high-water floor.
	directCmd := exec.Command(payloadPath)
	directOut, err := directCmd.CombinedOutput()
	require.NoError(t, err)
	assert.Equal(t, testPayloadMarker, string(directOut))
	directUsage, ok := directCmd.ProcessState.SysUsage().(*syscall.Rusage)
	require.True(t, ok)
	directRSSBytes := directUsage.Maxrss * kibibyte
	assert.GreaterOrEqual(t, directRSSBytes, int64(testParentMinExpectedRSS),
		"direct os/exec wait4 Maxrss should reflect the high-RSS Go parent floor")

	// 2. Isolated execution through the 2-page static ELF trampoline reports only the child's own footprint.
	cgroupDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(cgroupDir, "memory.peak"), []byte("1048576\n"), controlMode))

	isolatedRes, err := RunIsolated(context.Background(), trampolinePath, IsolatedSpec{
		Path:        payloadPath,
		Args:        []string{"--arg-check"},
		Env:         []string{"MICROFAT_ISOLATED_TEST=1"},
		CgroupPaths: []string{cgroupDir},
	})
	runtime.KeepAlive(ballast)

	require.NoError(t, err)
	t.Logf("direct wait4 MaxRSS=%d bytes (%.2f MiB), isolated trampoline wait4 MaxRSS=%d bytes (%.2f MiB)",
		directRSSBytes, float64(directRSSBytes)/(kibibyte*kibibyte),
		isolatedRes.MaxRSSBytes, float64(isolatedRes.MaxRSSBytes)/(kibibyte*kibibyte))
	assert.Equal(t, 0, isolatedRes.ExitCode)
	assert.Greater(t, isolatedRes.PID, 0)
	assert.Equal(t, testPayloadMarker, string(isolatedRes.Stdout))
	assert.Empty(t, isolatedRes.Stderr)
	assert.GreaterOrEqual(t, isolatedRes.MaxRSSBytes, int64(0))
	assert.Less(t, isolatedRes.MaxRSSBytes, int64(testIsolatedRSSCeiling),
		"isolated grandchild MaxRSSBytes (%d) must not inherit the 64 MiB Go parent VmHWM (%d)",
		isolatedRes.MaxRSSBytes, directRSSBytes)
	require.NotNil(t, isolatedRes.CgroupPeak)
	assert.Equal(t, int64(1048576), *isolatedRes.CgroupPeak)
	assert.Equal(t, "cgroup/memory.peak", isolatedRes.CgroupSource)

	// 3. Also verify a real dynamic binary (/bin/sh) reports positive non-zero RSS (< 16 MiB) well below parent 64+ MiB floor.
	if shPath, lookErr := exec.LookPath("sh"); lookErr == nil {
		shRes, runErr := RunIsolated(context.Background(), trampolinePath, IsolatedSpec{
			Path: shPath,
			Args: []string{"-c", "printf sh-ok"},
		})
		require.NoError(t, runErr)
		assert.Equal(t, "sh-ok", string(shRes.Stdout))
		assert.Greater(t, shRes.MaxRSSBytes, int64(0))
		assert.Less(t, shRes.MaxRSSBytes, int64(testParentMinExpectedRSS))
	}
}

func TestRunIsolatedErrorAndCancellationPaths(t *testing.T) {
	t.Parallel()
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("unsupported Linux architecture %s", runtime.GOARCH)
	}

	trampolinePath, err := WriteTrampolineELF(t.TempDir(), runtime.GOARCH)
	require.NoError(t, err)

	// Relative paths rejected fail-fast.
	failPayload := buildTinyStaticELF(t, runtime.GOARCH, testCustomExitCode, false)
	_, err = RunIsolated(context.Background(), "relative/trampoline", IsolatedSpec{Path: failPayload})
	require.ErrorContains(t, err, "must be absolute")
	_, err = RunIsolated(context.Background(), trampolinePath, IsolatedSpec{Path: "relative/target"})
	require.ErrorContains(t, err, "must be absolute")

	// Missing trampoline binary fails cmd.Start cleanly without leaking FDs.
	_, err = RunIsolated(context.Background(), filepath.Join(t.TempDir(), "missing-trampoline"), IsolatedSpec{Path: failPayload})
	require.Error(t, err)

	// Missing target executable propagates exact kernel ENOENT over FD 4.
	_, err = RunIsolated(context.Background(), trampolinePath, IsolatedSpec{
		Path: filepath.Join(t.TempDir(), "missing-target"),
	})
	require.ErrorIs(t, err, syscall.ENOENT)

	// Non-zero exit code returns *IsolatedExitError while preserving resource observation.
	res, err := RunIsolated(context.Background(), trampolinePath, IsolatedSpec{Path: failPayload})
	var exitErr *IsolatedExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, testCustomExitCode, exitErr.ExitCode)
	assert.Equal(t, testCustomExitCode, res.ExitCode)
	assert.Less(t, res.MaxRSSBytes, int64(testIsolatedRSSCeiling))

	// Non-trampoline executable produces an incomplete wait4 report and fails closed.
	noopPayload := buildTinyStaticELF(t, runtime.GOARCH, 0, false)
	_, err = RunIsolated(context.Background(), noopPayload, IsolatedSpec{Path: noopPayload})
	require.ErrorContains(t, err, "incomplete isolated wait4 report")

	// Context timeout terminates both trampoline and grandchild process group cleanly.
	loopPayload := buildTinyStaticELF(t, runtime.GOARCH, 0, true)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeoutWindow)
	defer cancel()
	_, err = RunIsolated(ctx, trampolinePath, IsolatedSpec{Path: loopPayload})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestDecodeIsolatedReportAndLimitedBuffer(t *testing.T) {
	t.Parallel()

	var report [trampolineReportBytes]byte
	binary.LittleEndian.PutUint64(report[0:reportStatusOffset], 4321)
	// Raw Linux wait status for termination by SIGKILL (9) has low 7 bits = 9.
	binary.LittleEndian.PutUint32(report[reportStatusOffset:reportStatusOffset+4], testRawSignalKill<<testSignalShift)
	binary.LittleEndian.PutUint64(report[reportMaxRSSOffset:reportMaxRSSOffset+8], 512)

	decoded, err := decodeIsolatedReport(IsolatedResult{}, report[:])
	var exitErr *IsolatedExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, testRawSignalKill, exitErr.Signal)
	assert.Equal(t, 4321, decoded.PID)
	assert.Equal(t, int64(512*kibibyte), decoded.MaxRSSBytes)

	var lb limitedBuffer
	lb.limit = 4
	n, writeErr := lb.Write([]byte("ok"))
	require.NoError(t, writeErr)
	assert.Equal(t, 2, n)
	_, writeErr = lb.Write([]byte("overflow"))
	require.ErrorIs(t, writeErr, process.ErrOutputLimit)
	require.ErrorIs(t, lb.err, process.ErrOutputLimit)
}

func TestRunIsolatedPipeOutputLimitAndCancelBranches(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("unsupported Linux architecture %s", runtime.GOARCH)
	}

	assert.NoError(t, cancelIsolatedProcess(nil))
	assert.NoError(t, cancelIsolatedProcess(&exec.Cmd{}))

	oldKill := killProcessGroup
	t.Cleanup(func() { killProcessGroup = oldKill })
	killProcessGroup = func(int, syscall.Signal) error {
		return syscall.ESRCH
	}
	err := cancelIsolatedProcess(&exec.Cmd{Process: &os.Process{Pid: 42}})
	require.ErrorIs(t, err, os.ErrProcessDone)
	killProcessGroup = oldKill

	trampolinePath, err := WriteTrampolineELF(t.TempDir(), runtime.GOARCH)
	require.NoError(t, err)
	payloadPath := buildTinyStaticELF(t, runtime.GOARCH, 0, false)

	oldPipe := newIsolatedPipe
	t.Cleanup(func() { newIsolatedPipe = oldPipe })

	// 1. First pipe creation failure.
	newIsolatedPipe = func() (*os.File, *os.File, error) {
		return nil, nil, syscall.EMFILE
	}
	_, err = RunIsolated(context.Background(), trampolinePath, IsolatedSpec{Path: payloadPath})
	require.ErrorIs(t, err, syscall.EMFILE)

	// 2. Second pipe creation failure closes the first pipe pair cleanly.
	calls := 0
	newIsolatedPipe = func() (*os.File, *os.File, error) {
		calls++
		if calls == 1 {
			return oldPipe()
		}
		return nil, nil, syscall.ENFILE
	}
	_, err = RunIsolated(context.Background(), trampolinePath, IsolatedSpec{Path: payloadPath})
	require.ErrorIs(t, err, syscall.ENFILE)
	newIsolatedPipe = oldPipe

	// 3. Stdout exceeding maxIsolatedOutput triggers closeErr return path.
	oldMax := maxIsolatedOutput
	t.Cleanup(func() { maxIsolatedOutput = oldMax })
	maxIsolatedOutput = 4
	_, err = RunIsolated(context.Background(), trampolinePath, IsolatedSpec{Path: payloadPath})
	require.ErrorIs(t, err, process.ErrOutputLimit)
}

func TestRunIsolatedPropagatesWaitDelay(t *testing.T) {
	t.Parallel()
	if runtime.GOARCH != trampolineArchAMD64 && runtime.GOARCH != trampolineArchARM64 {
		t.Skipf("unsupported Linux architecture %s", runtime.GOARCH)
	}
	shPath, err := exec.LookPath("sh")
	require.NoError(t, err)
	trampolinePath, err := WriteTrampolineELF(t.TempDir(), runtime.GOARCH)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), testIsolatedCleanupTimeout)
	defer cancel()

	// The target exits successfully, but its background child keeps the output pipe open.
	// Preserve cmd.Wait's ErrWaitDelay instead of reporting a successful measurement.
	res, err := RunIsolated(ctx, trampolinePath, IsolatedSpec{
		Path: shPath,
		Args: []string{"-c", "sleep 30 & exit 0"},
	})
	require.ErrorIs(t, err, exec.ErrWaitDelay)
	assert.Equal(t, 0, res.ExitCode)
	assert.Greater(t, res.PID, 0)
}

func TestRunIsolatedOutputLimitCancelsWorkload(t *testing.T) {
	if runtime.GOARCH != trampolineArchAMD64 && runtime.GOARCH != trampolineArchARM64 {
		t.Skipf("unsupported Linux architecture %s", runtime.GOARCH)
	}
	shPath, err := exec.LookPath("sh")
	require.NoError(t, err)
	trampolinePath, err := WriteTrampolineELF(t.TempDir(), runtime.GOARCH)
	require.NoError(t, err)
	oldMax := maxIsolatedOutput
	maxIsolatedOutput = 4
	t.Cleanup(func() { maxIsolatedOutput = oldMax })

	for _, output := range []string{"stdout", "stderr"} {
		t.Run(output, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testIsolatedCleanupTimeout)
			defer cancel()
			script := "printf overflow; exec sleep 30"
			if output == "stderr" {
				script = "printf overflow >&2; exec sleep 30"
			}
			_, runErr := RunIsolated(ctx, trampolinePath, IsolatedSpec{Path: shPath, Args: []string{"-c", script}})
			require.ErrorIs(t, runErr, process.ErrOutputLimit)
			require.NoError(t, ctx.Err(), "output overflow must cancel the workload before the caller's deadline")
		})
	}
}
