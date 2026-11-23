// Command v3bench benchmarks Bend-parity block matching and compares Format v2 vs Format v3 across real Go ELF corpora.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/EpicBlackWolfZ/microfat/experiments/v3lab/compress"
	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

const (
	bendLeafCount      = 16384
	bendOuterSteps     = 256
	bendInnerSteps     = 64
	bendExpectedHits   = uint64(16777216)
	bendSeed1Mult      = uint32(2654435761)
	bendSeed1Add       = uint32(1)
	bendSeed2Mult      = uint32(2246822519)
	bendSeed2Add       = uint32(17)
	bendLCGMult        = uint32(1664525)
	bendLCGAdd         = uint32(1013904223)
	bendMask15         = uint32(15)
	bendShift4         = uint32(4)
	bendBenchRuns      = 3
	memfdDecodeRuns    = 5
	stubE2ERuns        = 15
	shortHashPrefixLen = 16
	v3DefaultDictSize  = 112 * 1024
	kib                = 1024
	chunk64K           = 64 * kib
	chunk128K          = 128 * kib
	chunk256K          = 256 * kib
	chunk512K          = 512 * kib
	workerCount1       = 1
	workerCount4       = 4
	workerCount16      = 16
	workerCount32      = 32
	dirPerm            = 0o755
	filePerm           = 0o755
	pctScale           = 100.0
	usPerMs            = 1000.0
	corpusBaseDir      = ".work/v3lab/corpus"
)

type corpusTarget struct {
	name    string
	pkg     string
	ldflags string
}

type v3BenchConfig struct {
	name       string
	chunkMode  compress.ChunkMode
	chunkSize  int
	bcjRIP     bool
	enableDict bool
}

func runBendParityWorkers(workers int) (uint64, time.Duration) {
	start := time.Now()
	totals := make([]uint64, workers)
	var wg sync.WaitGroup
	for wid := range workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			var hits uint64
			for idx := workerID; idx < bendLeafCount; idx += workers {
				baseStep := uint32(idx * bendOuterSteps) // #nosec G115 -- bounded by 16384*256
				for t := range uint32(bendOuterSteps) {
					step := baseStep + t
					s1 := step*bendSeed1Mult + bendSeed1Add
					s2 := step*bendSeed2Mult + bendSeed2Add
					for range bendInnerSteps {
						if (s1 & bendMask15) == ((s2 >> bendShift4) & bendMask15) {
							hits++
						}
						s1 = s1*bendLCGMult + bendLCGAdd
						s2 = s2*bendLCGMult + bendLCGAdd
					}
				}
			}
			totals[workerID] = hits
		}(wid)
	}
	wg.Wait()
	elapsed := time.Since(start)
	var total uint64
	for _, v := range totals {
		total += v
	}
	return total, elapsed
}

func runBendParitySuite() error {
	fmt.Println("=== Part 1: Go Goroutine Parity vs Bend block_match (268,435,456 checks) ===")
	workerCounts := []int{workerCount1, workerCount4, workerCount16, workerCount32}
	for _, w := range workerCounts {
		durations := make([]time.Duration, bendBenchRuns)
		for run := range bendBenchRuns {
			hits, elapsed := runBendParityWorkers(w)
			if hits != bendExpectedHits {
				return fmt.Errorf("bend parity mismatch for %d workers: got %d, want %d", w, hits, bendExpectedHits)
			}
			durations[run] = elapsed
		}
		slices.Sort(durations)
		med := durations[len(durations)/2]
		fmt.Printf("  go-workers=%-2d median=%.2f ms (runs: %.2f, %.2f, %.2f ms) hits=%d\n",
			w,
			float64(med.Microseconds())/usPerMs,
			float64(durations[0].Microseconds())/usPerMs,
			float64(durations[1].Microseconds())/usPerMs,
			float64(durations[2].Microseconds())/usPerMs,
			bendExpectedHits,
		)
	}
	return nil
}

func buildCorpusVariant(pkg, ldflags, level, outPath string) ([]byte, error) {
	args := []string{"build", "-trimpath"}
	if ldflags != "" {
		args = append(args, "-ldflags="+ldflags)
	}
	args = append(args, "-o", outPath, pkg)

	cmd := exec.Command("go", args...) // #nosec G204 -- fixed internal package build arguments
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64", "GOAMD64="+level)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go build %s (%s): %w\n%s", pkg, level, err, string(out))
	}
	data, err := os.ReadFile(outPath) // #nosec G304 -- reading newly built corpus binary path
	if err != nil {
		return nil, fmt.Errorf("read built variant %s: %w", outPath, err)
	}
	return data, nil
}

func benchV2Control(
	name string,
	stubPath string,
	stubSize int64,
	variantPaths map[string]string,
	outPath string,
	profile string,
	compression string,
	dictProfitable bool,
	rawSum int64,
) (int64, error) {
	fixedEpoch := int64(0)
	t0 := time.Now()
	_, err := pack.Pack(pack.Options{
		StubPath:       stubPath,
		OutputPath:     outPath,
		AppName:        "v3bench-ctrl",
		TargetOS:       "linux",
		TargetArch:     microarch.ArchAMD64,
		Variants:       variantPaths,
		Profile:        profile,
		Compression:    compression,
		DictProfitable: dictProfitable,
		Permissions:    filePerm,
		FormatVersion:  format.FormatVersion2,
		CreatedUnix:    &fixedEpoch,
	})
	packDur := time.Since(t0)
	if err != nil {
		return 0, fmt.Errorf("pack v2 %s: %w", name, err)
	}
	st, err := os.Stat(outPath)
	if err != nil {
		return 0, fmt.Errorf("stat %s: %w", outPath, err)
	}
	netBytes := st.Size() - stubSize
	ratioPct := (float64(netBytes) / float64(rawSum)) * pctScale
	fmt.Printf("  %-30s net=%9d B (full=%9d B) ratio=%.2f%% pack=%7.1f ms\n",
		name, netBytes, st.Size(), ratioPct, float64(packDur.Microseconds())/usPerMs)

	// Measure cold Format v2 Zstd memfd extract time on v3 for direct decode baseline.
	med1, err := measureV2MemfdCold(outPath, microarch.AMD64v3)
	if err != nil {
		return 0, fmt.Errorf("decode v2 %s: %w", name, err)
	}
	fmt.Printf("    -> cold v2->memfd+sha256 median=%.2f ms (1 worker)\n", float64(med1.Microseconds())/usPerMs)
	return netBytes, nil
}

func measureV2MemfdCold(path, level string) (time.Duration, error) {
	durations := make([]time.Duration, memfdDecodeRuns)
	for run := range memfdDecodeRuns {
		t0 := time.Now()
		f, err := os.Open(path) // #nosec G304 -- reading benchmark artifact created by v3bench
		if err != nil {
			return 0, err
		}
		st, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return 0, err
		}
		idx, err := format.ReadTrailerAndIndex(f, st.Size())
		if err != nil {
			_ = f.Close()
			return 0, err
		}
		var entry *format.VariantEntry
		for i := range idx.Variants {
			if idx.Variants[i].Level == level {
				entry = &idx.Variants[i]
				break
			}
		}
		if entry == nil {
			_ = f.Close()
			return 0, fmt.Errorf("missing level %s in %s", level, path)
		}
		var dictBytes []byte
		if idx.DictionarySize > 0 {
			dictBytes = make([]byte, idx.DictionarySize)
			if _, rErr := f.ReadAt(dictBytes, idx.DictionaryOffset); rErr != nil {
				_ = f.Close()
				return 0, rErr
			}
			dSum := sha256.Sum256(dictBytes)
			if hex.EncodeToString(dSum[:]) != idx.DictionarySHA256 {
				_ = f.Close()
				return 0, errors.New("v2 dict sha mismatch")
			}
		}
		c, err := codec.Get(entry.Compression)
		if err != nil {
			_ = f.Close()
			return 0, err
		}
		fd, err := unix.MemfdCreate("v2bench", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
		if err != nil {
			_ = f.Close()
			return 0, err
		}
		memFile := os.NewFile(uintptr(fd), "v2memfd")
		h := sha256.New()
		mw := io.MultiWriter(memFile, h)
		sec := io.NewSectionReader(f, entry.Offset, entry.CompressedSize)
		dErr := codec.DecompressWithOptionalDict(c, mw, sec, entry.UncompressedSize, dictBytes)
		_, sealErr := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS,
			unix.F_SEAL_SEAL|unix.F_SEAL_SHRINK|unix.F_SEAL_GROW|unix.F_SEAL_WRITE)
		_ = memFile.Close()
		_ = f.Close()
		if dErr != nil {
			return 0, dErr
		}
		if sealErr != nil {
			return 0, sealErr
		}
		if hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
			return 0, errors.New("v2 payload sha mismatch")
		}
		durations[run] = time.Since(t0)
	}
	slices.Sort(durations)
	return durations[len(durations)/2], nil
}

func measureV3MemfdCold(path, level string, workers int) (time.Duration, error) {
	durations := make([]time.Duration, memfdDecodeRuns)
	for run := range memfdDecodeRuns {
		t0 := time.Now()
		f, err := os.Open(path) // #nosec G304 -- reading benchmark artifact created by v3bench
		if err != nil {
			return 0, err
		}
		st, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return 0, err
		}
		arch, err := compress.ReadV3Archive(f, st.Size())
		if err != nil {
			_ = f.Close()
			return 0, err
		}
		fd, err := compress.DecodeVariantToSealedMemfd(arch, f, level, workers, true)
		_ = f.Close()
		if err != nil {
			return 0, err
		}
		_ = unix.Close(fd)
		durations[run] = time.Since(t0)
	}
	slices.Sort(durations)
	return durations[len(durations)/2], nil
}

func benchV3Config(
	cfg v3BenchConfig,
	variants map[string][]byte,
	outPath string,
	rawSum int64,
	v2NoDict int64,
	v2DictProf int64,
) error {
	t0 := time.Now()
	outFile, err := os.Create(outPath) // #nosec G304 -- creating benchmark artifact path under .work/v3lab
	if err != nil {
		return fmt.Errorf("create %s: %w", outPath, err)
	}
	_, stats, packErr := compress.PackV3(outFile, compress.V3PackOptions{
		TargetArch:     microarch.ArchAMD64,
		Variants:       variants,
		ChunkMode:      cfg.chunkMode,
		AvgChunkSize:   cfg.chunkSize,
		EnableBCJRIP:   cfg.bcjRIP,
		EnableDict:     cfg.enableDict,
		DictSize:       v3DefaultDictSize,
		ZstdLevel:      zstd.SpeedBestCompression,
		ParallelEncode: runtime.NumCPU(),
	})
	closeErr := outFile.Close()
	packDur := time.Since(t0)
	if packErr != nil {
		return fmt.Errorf("pack v3 %s: %w", cfg.name, packErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", outPath, closeErr)
	}

	medW1, err := measureV3MemfdCold(outPath, microarch.AMD64v3, workerCount1)
	if err != nil {
		return fmt.Errorf("decode v3 %s (1w): %w", cfg.name, err)
	}
	medW4, err := measureV3MemfdCold(outPath, microarch.AMD64v3, workerCount4)
	if err != nil {
		return fmt.Errorf("decode v3 %s (4w): %w", cfg.name, err)
	}

	vsNoDict := (float64(stats.TotalArchiveBytes-v2NoDict) / float64(v2NoDict)) * pctScale
	vsDict := (float64(stats.TotalArchiveBytes-v2DictProf) / float64(v2DictProf)) * pctScale
	fmt.Printf(
		"  %-28s total=%9d B payload=%9d B dict=%6d idx=%6d blks=%4d/%4d "+
			"dedup=%9d B ratio=%.2f%% vs_v2_nodict=%+.2f%% vs_v2_dict=%+.2f%% "+
			"pack=%6.1f ms decode1w=%5.2f ms decode4w=%5.2f ms\n",
		cfg.name,
		stats.TotalArchiveBytes,
		stats.CompressedPayload,
		stats.DictBytes,
		stats.IndexBytes,
		stats.UniqueBlockCount,
		stats.LogicalExtentCount,
		stats.DeduplicatedBytes,
		(float64(stats.TotalArchiveBytes)/float64(rawSum))*pctScale,
		vsNoDict,
		vsDict,
		float64(packDur.Microseconds())/usPerMs,
		float64(medW1.Microseconds())/usPerMs,
		float64(medW4.Microseconds())/usPerMs,
	)
	return nil
}

func runCorpusSuite() error {
	if err := os.MkdirAll(corpusBaseDir, dirPerm); err != nil {
		return fmt.Errorf("mkdir %s: %w", corpusBaseDir, err)
	}

	targets := []corpusTarget{
		{name: "demo-stripped", pkg: "./examples/demo", ldflags: "-s -w"},
		{name: "demo-unstripped", pkg: "./examples/demo", ldflags: ""},
		{name: "microfat-stripped", pkg: "./cmd/microfat", ldflags: "-s -w"},
		{name: "microfat-unstripped", pkg: "./cmd/microfat", ldflags: ""},
	}
	v3Configs := []v3BenchConfig{
		{name: "v3-fixed-128k-nobcj-nodict", chunkMode: compress.ChunkFixed, chunkSize: chunk128K, bcjRIP: false, enableDict: false},
		{name: "v3-fixed-128k-bcj-nodict", chunkMode: compress.ChunkFixed, chunkSize: chunk128K, bcjRIP: true, enableDict: false},
		{name: "v3-fixed-128k-bcj-dict", chunkMode: compress.ChunkFixed, chunkSize: chunk128K, bcjRIP: true, enableDict: true},
		{name: "v3-fixed-256k-bcj-dict", chunkMode: compress.ChunkFixed, chunkSize: chunk256K, bcjRIP: true, enableDict: true},
		{name: "v3-fixed-512k-bcj-dict", chunkMode: compress.ChunkFixed, chunkSize: chunk512K, bcjRIP: true, enableDict: true},
		{name: "v3-cdc-64k-bcj-dict", chunkMode: compress.ChunkSectionCDC, chunkSize: chunk64K, bcjRIP: true, enableDict: true},
		{name: "v3-cdc-128k-nobcj-nodict", chunkMode: compress.ChunkSectionCDC, chunkSize: chunk128K, bcjRIP: false, enableDict: false},
		{name: "v3-cdc-128k-bcj-nodict", chunkMode: compress.ChunkSectionCDC, chunkSize: chunk128K, bcjRIP: true, enableDict: false},
		{name: "v3-cdc-128k-bcj-dict", chunkMode: compress.ChunkSectionCDC, chunkSize: chunk128K, bcjRIP: true, enableDict: true},
	}

	levels := []string{microarch.AMD64v1, microarch.AMD64v2, microarch.AMD64v3}
	fmt.Printf("\n=== Part 2: Format v2 vs Format v3 ELF Corpus (Host CPUs=%d) ===\n", runtime.NumCPU())

	for _, target := range targets {
		targetDir := filepath.Join(corpusBaseDir, target.name)
		if err := os.MkdirAll(targetDir, dirPerm); err != nil {
			return fmt.Errorf("mkdir %s: %w", targetDir, err)
		}
		variantPaths := make(map[string]string, len(levels))
		variantBytes := make(map[string][]byte, len(levels))
		var rawSum int64
		for _, lvl := range levels {
			outBin := filepath.Join(targetDir, "amd64_"+lvl)
			b, err := buildCorpusVariant(target.pkg, target.ldflags, lvl, outBin)
			if err != nil {
				return err
			}
			variantPaths[lvl] = outBin
			variantBytes[lvl] = b
			rawSum += int64(len(b))
		}
		fmt.Printf("\n[%s] v1=%d B v2=%d B v3=%d B raw_sum=%d B\n",
			target.name, len(variantBytes[microarch.AMD64v1]), len(variantBytes[microarch.AMD64v2]),
			len(variantBytes[microarch.AMD64v3]), rawSum)

		ctrlStubPath := variantPaths[microarch.AMD64v1]
		ctrlStubSize := int64(len(variantBytes[microarch.AMD64v1]))
		if _, err := benchV2Control(
			"v2-balanced",
			ctrlStubPath, ctrlStubSize, variantPaths,
			filepath.Join(targetDir, "v2_balanced.bin"),
			codec.ProfileBalanced, "", false, rawSum,
		); err != nil {
			return err
		}
		v2NoDict, err := benchV2Control(
			"v2-size-nodict",
			ctrlStubPath, ctrlStubSize, variantPaths,
			filepath.Join(targetDir, "v2_size_nodict.bin"),
			codec.ProfileSize, "zstd:best", false, rawSum,
		)
		if err != nil {
			return err
		}
		v2DictProf, err := benchV2Control(
			"v2-size-dictprof",
			ctrlStubPath, ctrlStubSize, variantPaths,
			filepath.Join(targetDir, "v2_size_dictprof.bin"),
			codec.ProfileSize, "zstd:best", true, rawSum,
		)
		if err != nil {
			return err
		}

		for _, cfg := range v3Configs {
			if err := benchV3Config(
				cfg,
				variantBytes,
				filepath.Join(targetDir, cfg.name+".v3"),
				rawSum,
				v2NoDict,
				v2DictProf,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func runStubE2ESuite() error {
	const e2eDir = ".work/v3lab/e2e"
	codecs := []string{"none", "lz4", "zstd", "zstd-dict"}
	stubs := []struct {
		prefix   string
		stubPath string
	}{
		{prefix: "gof", stubPath: "bin/microfat-stub"},
		{prefix: "gom", stubPath: "bin/microfat-stub-minimal"},
		{prefix: "rs", stubPath: "experiments/v3lab/rust-stub/target/release/microfat-stub-rs"},
	}

	fmt.Println("=== Part 3: End-to-End Stub Compatibility & Cold memfd Latency (15 runs, --help) ===")
	var wantDigest string
	for _, st := range stubs {
		stInfo, err := os.Stat(st.stubPath)
		if err != nil {
			return fmt.Errorf("stat stub %s: %w", st.stubPath, err)
		}
		fmt.Printf("\n[stub=%s bytes=%d]\n", st.prefix, stInfo.Size())
		for _, c := range codecs {
			binPath := filepath.Join(e2eDir, st.prefix+"-"+c)
			binInfo, err := os.Stat(binPath)
			if err != nil {
				return fmt.Errorf("stat fat binary %s: %w", binPath, err)
			}
			durations := make([]time.Duration, stubE2ERuns)
			for run := range stubE2ERuns {
				cmd := exec.Command(binPath, "--help") // #nosec G204 -- controlled e2e artifact path
				cmd.Env = append(os.Environ(), "MICROFAT_EXEC_MODE=memfd")
				t0 := time.Now()
				out, err := cmd.Output()
				elapsed := time.Since(t0)
				if err != nil {
					return fmt.Errorf("exec %s --help: %w", binPath, err)
				}
				sum := sha256.Sum256(out)
				hexSum := hex.EncodeToString(sum[:])
				if wantDigest == "" {
					wantDigest = hexSum
				} else if hexSum != wantDigest {
					return fmt.Errorf("stdout SHA-256 mismatch on %s: got %s, want %s", binPath, hexSum, wantDigest)
				}
				durations[run] = elapsed
			}
			slices.Sort(durations)
			med := durations[len(durations)/2]
			minD := durations[0]
			maxD := durations[len(durations)-1]
			fmt.Printf("  %-12s fat=%9d B stub=%7d B med=%6.2f ms (min=%.2f max=%.2f) sha256=%s\n",
				c,
				binInfo.Size(),
				stInfo.Size(),
				float64(med.Microseconds())/usPerMs,
				float64(minD.Microseconds())/usPerMs,
				float64(maxD.Microseconds())/usPerMs,
				wantDigest[:shortHashPrefixLen],
			)
		}
	}
	return nil
}

func main() {
	stubE2EOnly := flag.Bool("stub-e2e", false, "Run only Part 3 end-to-end stub compatibility & latency benchmarks")
	flag.Parse()

	if *stubE2EOnly {
		if err := runStubE2ESuite(); err != nil {
			fmt.Fprintf(os.Stderr, "v3bench stub-e2e failed: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := runBendParitySuite(); err != nil {
		fmt.Fprintf(os.Stderr, "v3bench parity failed: %v\n", err)
		os.Exit(1)
	}
	if err := runCorpusSuite(); err != nil {
		fmt.Fprintf(os.Stderr, "v3bench corpus failed: %v\n", err)
		os.Exit(1)
	}
}
