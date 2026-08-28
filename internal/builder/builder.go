package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
	"github.com/EpicBlackWolfZ/microfat/internal/pack"
)

// Common constants for builder orchestration.
const (
	defaultGoBinary = "go"
	tempDirPattern  = ".microfat-pgo-*"
	pgoOffFlag      = "-pgo=off"
	defaultPGOFile  = "default.pgo"
	defaultFileMode = 0o755
)

// BuildOptions contains configuration parameters for building and packaging PGO variants.
type BuildOptions struct {
	ManifestPath      string
	OutputPath        string
	StubPath          string
	Concurrency       int
	KeepIntermediates bool
	GoBinary          string
	SkipELFValidation bool
	Profile           string
	Compression       string
	CompressionLevel  string
	EnableDict        bool
	DictSize          int
	FormatVersion     int
	Stdout            io.Writer
	Stderr            io.Writer
}

// BuildResult contains metadata about the compiled and packaged fat binary.
type BuildResult struct {
	Index             *format.Index
	OutputPath        string
	IntermediatesDir  string
	CompiledVariants  map[string]string // level -> temporary binary path
	VariantPGOApplied map[string]string // level -> pgo flag applied
}

// BuildAndPack orchestrates concurrent Go compilation of all manifest variants with Profile-Guided
// Optimization flags, then packages the resulting ELF binaries into a self-dispatching fat executable.
func BuildAndPack(ctx context.Context, m *Manifest, opts BuildOptions) (*BuildResult, error) {
	if m == nil {
		return nil, ErrInvalidManifest
	}

	finalOutput := opts.OutputPath
	if finalOutput == "" {
		finalOutput = m.Output
	}
	if finalOutput == "" {
		return nil, errors.New("output destination path must be specified via manifest 'output' or CLI '--output'")
	}
	finalOutput = filepath.Clean(finalOutput)
	if !filepath.IsAbs(finalOutput) && m.Dir != "" {
		finalOutput = filepath.Join(m.Dir, finalOutput)
	}

	stubPath, err := ResolveStubPath(opts.StubPath, m.Stub, m.Dir)
	if err != nil {
		return nil, err
	}

	goBinary := opts.GoBinary
	if goBinary == "" {
		goBinary = os.Getenv("GO")
	}
	if goBinary == "" {
		goBinary = defaultGoBinary
	}

	outDir := filepath.Dir(finalOutput)
	absOutDir, err := filepath.Abs(outDir)
	if err != nil {
		return nil, fmt.Errorf("resolving absolute output directory %s: %w", outDir, err)
	}
	if err := os.MkdirAll(absOutDir, defaultFileMode); err != nil {
		return nil, fmt.Errorf("creating output directory %s: %w", absOutDir, err)
	}

	tmpDir, err := os.MkdirTemp(absOutDir, tempDirPattern)
	if err != nil {
		return nil, fmt.Errorf("creating temporary build directory: %w", err)
	}
	absTmpDir, err := filepath.Abs(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("resolving absolute temp directory %s: %w", tmpDir, err)
	}
	tmpDir = absTmpDir

	if !opts.KeepIntermediates {
		defer func() {
			_ = os.RemoveAll(tmpDir)
		}()
	}

	appName := m.AppName
	if appName == "" {
		appName = filepath.Base(finalOutput)
	}

	pgoMap, err := resolveAllVariantPGOs(m)
	if err != nil {
		return nil, err
	}

	compiledMap, err := compileVariantsConcurrently(ctx, m, pgoMap, tmpDir, goBinary, opts)
	if err != nil {
		return nil, err
	}

	packOpts := assemblePackOptions(m, stubPath, finalOutput, appName, compiledMap, opts)

	idx, err := pack.Pack(packOpts)
	if err != nil {
		return nil, fmt.Errorf("packaging fat binary: %w", err)
	}

	return &BuildResult{
		Index:             idx,
		OutputPath:        finalOutput,
		IntermediatesDir:  tmpDir,
		CompiledVariants:  compiledMap,
		VariantPGOApplied: pgoMap,
	}, nil
}

func resolveAllVariantPGOs(m *Manifest) (map[string]string, error) {
	pkgDir := m.Package
	if !filepath.IsAbs(pkgDir) && m.Dir != "" {
		pkgDir = filepath.Join(m.Dir, pkgDir)
	}

	pgoMap := make(map[string]string, len(m.Variants))
	for _, v := range m.Variants {
		pgoFlag, err := resolveVariantPGO(v, m.DefaultPGO, m.Dir, pkgDir)
		if err != nil {
			return nil, err
		}
		pgoMap[v.Level] = pgoFlag
	}
	return pgoMap, nil
}

func resolveVariantPGO(v VariantConfig, defaultPGO, manifestDir, pkgDir string) (string, error) {
	pgoVal := strings.TrimSpace(v.PGO)
	if strings.EqualFold(pgoVal, "off") {
		return pgoOffFlag, nil
	}

	if pgoVal != "" {
		resolved := pgoVal
		if !filepath.IsAbs(resolved) && manifestDir != "" {
			resolved = filepath.Join(manifestDir, resolved)
		}
		if _, err := os.Stat(resolved); err != nil {
			return "", fmt.Errorf("%w for variant %s: %s", ErrProfileNotFound, v.Level, resolved)
		}
		return "-pgo=" + resolved, nil
	}

	// Fallback to top-level default_pgo
	defVal := strings.TrimSpace(defaultPGO)
	if strings.EqualFold(defVal, "off") {
		return pgoOffFlag, nil
	}

	if defVal != "" {
		resolved := defVal
		if !filepath.IsAbs(resolved) && manifestDir != "" {
			resolved = filepath.Join(manifestDir, resolved)
		}
		if _, err := os.Stat(resolved); err != nil {
			return "", fmt.Errorf("%w (default_pgo): %s", ErrProfileNotFound, resolved)
		}
		return "-pgo=" + resolved, nil
	}

	// Fallback to default.pgo in package directory
	pkgDefault := filepath.Join(pkgDir, defaultPGOFile)
	if stat, err := os.Stat(pkgDefault); err == nil && !stat.IsDir() {
		return "-pgo=" + pkgDefault, nil
	}

	return pgoOffFlag, nil
}

func compileVariantsConcurrently(
	ctx context.Context,
	m *Manifest,
	pgoMap map[string]string,
	tmpDir string,
	goBinary string,
	opts BuildOptions,
) (map[string]string, error) {
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = runtime.NumCPU()
	}
	if concurrency > len(m.Variants) {
		concurrency = len(m.Variants)
	}
	if concurrency < 1 {
		concurrency = 1
	}

	type compileTask struct {
		variant VariantConfig
	}

	tasks := make(chan compileTask, len(m.Variants))
	for _, v := range m.Variants {
		tasks <- compileTask{variant: v}
	}
	close(tasks)

	var (
		mu          sync.Mutex
		firstErr    error
		compiledMap = make(map[string]string, len(m.Variants))
		wg          sync.WaitGroup
	)

	ctxCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range tasks {
				select {
				case <-ctxCancel.Done():
					return
				default:
				}

				v := task.variant
				outBinary := filepath.Join(tmpDir, fmt.Sprintf("bin_%s_%s", m.TargetArch, v.Level))
				pgoFlag := pgoMap[v.Level]

				cmd := buildGoCommand(ctxCancel, goBinary, m, v, pgoFlag, outBinary)
				out, err := cmd.CombinedOutput()
				if err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("compiling variant %s: %w\nGo compiler output:\n%s", v.Level, err, strings.TrimSpace(string(out)))
						cancel()
					}
					mu.Unlock()
					return
				}

				mu.Lock()
				compiledMap[v.Level] = outBinary
				if opts.Stdout != nil {
					_, _ = fmt.Fprintf(opts.Stdout, "  ✔ Compiled %-6s (pgo: %s)\n", v.Level, pgoFlag)
				}
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	return compiledMap, nil
}

func buildGoCommand(
	ctx context.Context,
	goBinary string,
	m *Manifest,
	v VariantConfig,
	pgoFlag string,
	outBinary string,
) *exec.Cmd {
	pkgTarget := m.Package
	workDir := m.Dir

	resolvedPkg := m.Package
	if !filepath.IsAbs(resolvedPkg) && m.Dir != "" {
		resolvedPkg = filepath.Join(m.Dir, resolvedPkg)
	}

	if stat, err := os.Stat(resolvedPkg); err == nil && stat.IsDir() {
		workDir = resolvedPkg
		pkgTarget = "."
	}

	args := []string{"build", "-o", outBinary}

	if pgoFlag != "" {
		args = append(args, pgoFlag)
	}

	if len(m.Tags) > 0 {
		args = append(args, "-tags="+strings.Join(m.Tags, ","))
	}

	args = append(args, m.BuildFlags...)
	args = append(args, v.Flags...)
	args = append(args, pkgTarget)

	// #nosec G204,G702 -- goBinary is user configured or PATH discovered
	cmd := exec.CommandContext(ctx, goBinary, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}

	env := os.Environ()
	env = append(env, "GOOS="+m.TargetOS, "GOARCH="+m.TargetArch)

	switch m.TargetArch {
	case microarch.ArchAMD64:
		env = append(env, "GOAMD64="+v.Level)
	case microarch.ArchARM64:
		env = append(env, "GOARM64="+v.Level)
	}

	for k, val := range m.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, val))
	}
	for k, val := range v.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, val))
	}

	cmd.Env = env
	return cmd
}

// ResolveStubPath resolves the path to the microfat launcher stub binary using precedence:
// 1. Explicit CLI flag `--stub`
// 2. Manifest field `stub:`
// 3. Executable-adjacent directory (`microfat-stub`)
// 4. `bin/microfat-stub` or `../bin/microfat-stub`
// 5. System `$PATH` lookup
func ResolveStubPath(cliStub, manifestStub, manifestDir string) (string, error) {
	if cliStub != "" {
		clean := filepath.Clean(cliStub)
		if stat, err := os.Stat(clean); err == nil && !stat.IsDir() {
			return clean, nil
		}
		return "", fmt.Errorf("%w: %s (specified via --stub)", ErrStubNotFound, cliStub)
	}

	if manifestStub != "" {
		resolved := manifestStub
		if !filepath.IsAbs(resolved) && manifestDir != "" {
			resolved = filepath.Join(manifestDir, resolved)
		}
		clean := filepath.Clean(resolved)
		if stat, err := os.Stat(clean); err == nil && !stat.IsDir() {
			return clean, nil
		}
		return "", fmt.Errorf("%w: %s (specified in manifest)", ErrStubNotFound, manifestStub)
	}

	// Search adjacent to current executable
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidate := filepath.Join(exeDir, "microfat-stub")
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate, nil
		}
	}

	// Search relative working directories
	candidates := []string{
		filepath.Join("bin", "microfat-stub"),
		filepath.Join("..", "bin", "microfat-stub"),
	}
	for _, c := range candidates {
		if stat, err := os.Stat(c); err == nil && !stat.IsDir() {
			return filepath.Clean(c), nil
		}
	}

	// Search in PATH
	if p, err := exec.LookPath("microfat-stub"); err == nil {
		return p, nil
	}

	return "", fmt.Errorf("%w: provide --stub flag, 'stub:' manifest entry, or compile stub into bin/microfat-stub", ErrStubNotFound)
}

func assemblePackOptions(
	m *Manifest,
	stubPath, finalOutput, appName string,
	compiledMap map[string]string,
	opts BuildOptions,
) pack.Options {
	packOpts := pack.DefaultOptions()
	packOpts.StubPath = stubPath
	packOpts.OutputPath = finalOutput
	packOpts.AppName = appName
	if m.TargetOS != "" {
		packOpts.TargetOS = m.TargetOS
	}
	if m.TargetArch != "" {
		packOpts.TargetArch = m.TargetArch
	}
	packOpts.Variants = compiledMap
	packOpts.SkipELFValidation = opts.SkipELFValidation
	if opts.FormatVersion != 0 {
		packOpts.FormatVersion = opts.FormatVersion
	}

	if m.Compression != nil {
		packOpts.Profile = m.Compression.Profile
		packOpts.Compression = m.Compression.Algorithm
		packOpts.CompressionLevel = m.Compression.Level
		packOpts.EnableDict = m.Compression.EnableDict
		packOpts.DictSize = m.Compression.DictSize
	}

	if opts.Profile != "" {
		packOpts.Profile = opts.Profile
	}
	if opts.Compression != "" {
		packOpts.Compression = opts.Compression
	}
	if opts.CompressionLevel != "" {
		packOpts.CompressionLevel = opts.CompressionLevel
	}
	if opts.EnableDict {
		packOpts.EnableDict = true
	}
	if opts.DictSize > 0 {
		packOpts.DictSize = opts.DictSize
	}

	if len(m.Variants) > 0 {
		varCompMap := make(map[string]pack.VariantCompressionOptions, len(m.Variants))
		for _, v := range m.Variants {
			if v.Compression != nil {
				varCompMap[v.Level] = pack.VariantCompressionOptions{
					Profile:     v.Compression.Profile,
					Compression: v.Compression.Algorithm,
					Level:       v.Compression.Level,
				}
			}
		}
		if len(varCompMap) > 0 {
			packOpts.VariantCompression = varCompMap
		}
	}

	return packOpts
}
