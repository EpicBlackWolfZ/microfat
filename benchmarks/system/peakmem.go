package system

import (
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrCgroupPeakUnavailable indicates no valid cgroup peak-memory counter was readable before teardown.
var ErrCgroupPeakUnavailable = errors.New("cgroup peak memory counter unavailable")

const (
	trampolineELFHeaderSize     = 64
	trampolineProgramHeaderSize = 56
	trampolineCodeOffset        = 4096
	trampolineLoadAddress       = 0x400000
	trampolineBinaryName        = "microfat-peakmem-trampoline"
	trampolineMode              = 0o700

	elfIdentVersionOffset = 6
	elfTypeOffset         = 16
	elfMachineOffset      = 18
	elfVersionOffset      = 20
	elfEntryOffset        = 24
	elfPhoffOffset        = 32
	elfEhsizeOffset       = 52
	elfPhentsizeOffset    = 54
	elfPhnumOffset        = 56

	phFlagsOffset  = 4
	phVaddrOffset  = 16
	phPaddrOffset  = 24
	phFileszOffset = 32
	phMemszOffset  = 40
	phAlignOffset  = 48
)

// Static syscall-only Linux 64-bit trampolines (assembled from verified _start routines):
//
//  1. Parent Go process spawns this 2-page static ELF, replacing the inherited Go mm_struct.
//  2. Trampoline calls clone(SIGCHLD, 0, 0, 0, 0) from its tiny static address space so the
//     grandchild receives a fresh signal_struct (maxrss = 0) and duplicates only the 2-page
//     trampoline mm rather than the parent Go process's high-water mark.
//  3. Grandchild closes report FD 3, marks exec-error FD 4 FD_CLOEXEC, and calls execve(argv[1], &argv[1], envp).
//  4. Trampoline waits via wait4(pid, &status, 0, &rusage) and writes the 160-byte binary record to FD 3.
const (
	trampolineAMD64Hex = "4c8b24244983fc020f8c250100004c8b6c24104c8d7424104e8d7ce4084881ec" +
		"a000000031c0b9140000004889e7f348abb838000000bf1100000031f631d245" +
		"31d24531c00f054885c074670f88ba0000004889c348891c24b803000000bf04" +
		"0000000f05b83d0000004889df488d74240831d24c8d5424100f054883f8fc74" +
		"e44885c00f88a9000000b801000000bf030000004889e6baa00000000f05b803" +
		"000000bf030000000f05b8e700000031ff0f05b803000000bf030000000f05b8" +
		"48000000bf04000000be02000000ba010000000f05b83b0000004c89ef4c89f6" +
		"4c89fa0f0548f7d848890424b801000000bf040000004889e6ba080000000f05" +
		"b8e7000000bf7f0000000f0548f7d848890424b801000000bf040000004889e6" +
		"ba080000000f05b8e7000000bf7e0000000f05b8e7000000bf7d0000000f05"

	trampolineARM64Hex = "f30340f97f0a00f18b0a0054f40b40f9f5430091f6030091d60e138bd6220091" +
		"ff8302d1ff7f00a9ff7f01a9ff7f02a9ff7f03a9ff7f04a9ff7f05a9ff7f06a9" +
		"ff7f07a9ff7f08a9ff7f09a9200280d2e1031faae2031faae3031faae4031faa" +
		"881b80d2010000d4600300b42006f8b7f70300aaf70300f9e0037eb2280780d2" +
		"010000d4e00317aae1230091e2031faae3430091882080d2010000d41f1000b1" +
		"20ffff54a005f8b7e00740b2e1030091021480d2e8037ab2010000d4e00740b2" +
		"280780d2010000d4e0031faac80b80d2010000d4e00740b2280780d2010000d4" +
		"e0037eb2e1037fb2e20340b2280380d2010000d4e00314aae10315aae20316aa" +
		"a81b80d2010000d4e00300cbe00300f9e0037eb2e1030091e2037db2e8037ab2" +
		"010000d4e01b40b2c80b80d2010000d4e00300cbe00300f9e0037eb2e1030091" +
		"e2037db2e8037ab2010000d4e0177fb2c80b80d2010000d4a00f80d2c80b80d2" +
		"010000d4"
)

// IsolatedSpec configures a child command measured without parent VmHWM inheritance.
type IsolatedSpec struct {
	Path        string
	Args        []string
	Env         []string
	Dir         string
	CgroupPaths []string
}

// IsolatedResult records the kernel wait4 resource observation of an isolated grandchild process
// alongside optional leaf-cgroup memory.peak readback captured prior to cgroup teardown.
type IsolatedResult struct {
	PID          int
	ExitCode     int
	Signal       int
	WallDuration time.Duration
	UserTime     time.Duration
	SystemTime   time.Duration
	MaxRSSBytes  int64
	MinorFaults  int64
	MajorFaults  int64
	VoluntaryCSW int64
	InvolCSW     int64
	CgroupPeak   *int64
	CgroupSource string
	Stdout       []byte
	Stderr       []byte
}

// IsolatedExitError reports non-zero exit status or signal termination of an isolated child.
type IsolatedExitError struct {
	ExitCode int
	Signal   int
}

func (e *IsolatedExitError) Error() string {
	if e.Signal != 0 {
		return fmt.Sprintf("isolated child terminated by signal %d", e.Signal)
	}
	return fmt.Sprintf("isolated child exited with code %d", e.ExitCode)
}

// ReadCgroupPeakBytes reads the kernel high-water memory counter (memory.peak on cgroup v2 or
// memory.max_usage_in_bytes on cgroup v1) from leaf cgroup directories before teardown.
// It never subtracts or fabricates a guessed baseline floor.
func ReadCgroupPeakBytes(paths []string) (int64, string, error) {
	var peak int64
	var source string
	found := false
	for _, root := range paths {
		if root == "" {
			continue
		}
		for _, name := range []string{"memory.peak", "memory.max_usage_in_bytes"} {
			// #nosec G304 -- caller-owned cgroup directory and fixed kernel controller filename.
			data, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				continue
			}
			val, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
			if err != nil || val < 0 {
				continue
			}
			if !found || val > peak {
				peak = val
				source = "cgroup/" + name
				found = true
			}
		}
	}
	if !found {
		return 0, "", ErrCgroupPeakUnavailable
	}
	return peak, source, nil
}

// PeakMemoryBytes reads the retained leaf-cgroup peak memory counter before Sandbox.Close removes the cgroup.
func (s *Sandbox) PeakMemoryBytes() (int64, string, error) {
	if s == nil {
		return 0, "", ErrCgroupPeakUnavailable
	}
	return ReadCgroupPeakBytes(s.Paths)
}

// BuildTrampolineELF constructs a minimal static 64-bit Linux ELF executable for amd64 or arm64
// that forks from a 2-page static address space so grandchild wait4 ru_maxrss reflects only the
// target child's execution rather than a large Go parent's VmHWM.
func BuildTrampolineELF(arch string) ([]byte, error) {
	var machine elf.Machine
	var encoded string
	switch arch {
	case "amd64":
		machine = elf.EM_X86_64
		encoded = trampolineAMD64Hex
	case "arm64":
		machine = elf.EM_AARCH64
		encoded = trampolineARM64Hex
	default:
		return nil, fmt.Errorf("unsupported trampoline architecture %q", arch)
	}
	code, err := hex.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	totalSize := trampolineCodeOffset + len(code)
	unsignedSize := uint64(totalSize) // #nosec G115 -- bounded static code length above positive offset.
	image := make([]byte, totalSize)
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
	put.PutUint64(ph[phFileszOffset:], unsignedSize)
	put.PutUint64(ph[phMemszOffset:], unsignedSize)
	put.PutUint64(ph[phAlignOffset:], trampolineCodeOffset)

	copy(image[trampolineCodeOffset:], code)
	return image, nil
}

// WriteTrampolineELF writes the static peak-memory trampoline executable into dir.
func WriteTrampolineELF(dir, arch string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", errors.New("trampoline output directory must be absolute")
	}
	image, err := BuildTrampolineELF(arch)
	if err != nil {
		return "", err
	}
	outPath := filepath.Join(dir, trampolineBinaryName+"-"+arch)
	if err := os.WriteFile(outPath, image, trampolineMode); err != nil {
		return "", err
	}
	return outPath, nil
}
