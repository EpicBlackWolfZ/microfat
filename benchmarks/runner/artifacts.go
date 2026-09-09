package runner

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/EpicBlackWolfZ/microfat/benchmarks/process"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/report"
	"github.com/EpicBlackWolfZ/microfat/benchmarks/schema"
	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

const (
	nativeMode    = "native"
	buildTimeout  = 5 * time.Minute
	targetPackage = "./benchmarks/workloads/server/cmd/bench-server"
	levelSymbol   = "github.com/EpicBlackWolfZ/microfat/benchmarks/workloads/server.Level"
)

type builtArtifacts struct {
	Artifacts      []schema.Artifact
	Configurations []schema.Configuration
	SourceSHA      string
	Dirty          bool
}

func buildArtifacts(ctx context.Context, cfg ExperimentConfig, opts RunOptions, root string) (*builtArtifacts, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("fat executable experiments require Linux")
	}
	ctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	version, _, err := process.Run(ctx, process.Spec{Path: opts.Go, Args: []string{"version"}, Dir: opts.Repository})
	if err != nil || !strings.Contains(string(version), "go1.27.1 ") {
		return nil, errors.New("benchmark builds require Go 1.27.1")
	}
	sha, _, err := process.Run(ctx, process.Spec{Path: "git", Args: []string{"rev-parse", "HEAD"}, Dir: opts.Repository})
	if err != nil {
		return nil, err
	}
	dirty, _, err := process.Run(ctx, process.Spec{Path: "git", Args: []string{"status", "--porcelain"}, Dir: opts.Repository})
	if err != nil {
		return nil, err
	}
	built := &builtArtifacts{SourceSHA: strings.TrimSpace(string(sha)), Dirty: len(dirty) > 0}
	generic := "v1"
	if runtime.GOARCH == "arm64" {
		generic = "v8.0"
	}
	host := microarch.Detect()
	specialized := cfg.SpecializedLevel
	if specialized == "" {
		specialized = host.Level
	}
	if _, err := microarch.SelectVariantForHost(runtime.GOARCH, host, []string{specialized}, microarch.Policy{}); err != nil {
		return nil, fmt.Errorf("specialized level incompatible with host: %w", err)
	}
	paths := make(map[string]string)
	for _, level := range []string{generic, specialized} {
		if paths[level] != "" {
			continue
		}
		file := filepath.Join(root, "native-"+level)
		if err := compile(ctx, opts, file, targetPackage, level, ""); err != nil {
			return nil, err
		}
		paths[level] = file
		artifact, err := identify(file, "native-"+level, built.SourceSHA)
		if err != nil {
			return nil, err
		}
		built.Artifacts = append(built.Artifacts, artifact)
	}
	stub := filepath.Join(root, "stub")
	tags := ""
	if cfg.Profile == minimalProfile {
		tags = minimalProfile
	}
	if err := compile(ctx, opts, stub, "./cmd/microfat-stub", generic, tags); err != nil {
		return nil, err
	}
	stubArtifact, err := identify(stub, "stub", built.SourceSHA)
	if err != nil {
		return nil, err
	}
	built.Artifacts = append(built.Artifacts, stubArtifact)
	fat := filepath.Join(root, "fat-server")
	packerArtifact, err := buildPacker(ctx, opts, root, generic, built.SourceSHA)
	if err != nil {
		return nil, err
	}

	built.Artifacts = append(built.Artifacts, packerArtifact)
	if err := packagePayloads(ctx, cfg, packerArtifact.Path, stub, fat, paths); err != nil {
		return nil, err
	}

	artifact, err := identify(fat, "fat", built.SourceSHA)
	if err != nil {
		return nil, err
	}
	artifact.Settings["format"], artifact.Settings["codec"], artifact.Settings["profile"] = strconv.Itoa(cfg.Format), cfg.Codec, cfg.Profile
	built.Artifacts = append(built.Artifacts, artifact)
	matched := map[string]string{"MICROFAT_AUTOTUNE": "0", "GOMAXPROCS": strconv.Itoa(cfg.GOMAXPROCS),
		"GOMEMLIMIT": cfg.GOMEMLIMIT, "GOGC": cfg.GOGC}
	built.Configurations = []schema.Configuration{
		{ID: "native-generic", ArtifactID: "native-" + generic, Level: generic, Mode: nativeMode, Tuning: tuningMatched, Environment: matched},
		{ID: "native-specialized", ArtifactID: "native-" + specialized, Level: specialized, Mode: nativeMode,
			Tuning: tuningMatched, Environment: matched},
		{ID: "fat-memfd", ArtifactID: "fat", Level: specialized, Mode: "memfd", Cache: "absent", Tuning: tuningMatched, Environment: matched},
	}
	for _, state := range cfg.CacheStates {
		built.Configurations = append(built.Configurations, schema.Configuration{ID: "fat-cache-" + state,
			ArtifactID: "fat", Level: specialized, Mode: "cache", Cache: state, Tuning: tuningMatched, Environment: matched})
	}
	if cfg.Tuning == "on-off" {
		built.Configurations = nil
		for _, toggle := range []string{"off", "on"} {
			value := "0"
			if toggle == "on" {
				value = "1"
			}
			built.Configurations = append(built.Configurations, schema.Configuration{ID: "fat-memfd-tuning-" + toggle,
				ArtifactID: "fat", Level: specialized, Mode: "memfd", Cache: "absent", Tuning: toggle,
				Environment: map[string]string{"MICROFAT_AUTOTUNE": value}})
		}
	}
	if opts.BaseRepository != "" {
		if err := addRevisionArtifacts(ctx, cfg, opts, root, generic, paths, built); err != nil {
			return nil, err
		}
	}
	return built, nil
}

func addRevisionArtifacts(ctx context.Context, cfg ExperimentConfig, opts RunOptions, root, generic string,
	paths map[string]string, built *builtArtifacts) error {
	baseOpts := opts
	baseOpts.Repository = opts.BaseRepository
	sha, _, err := process.Run(ctx, process.Spec{Path: "git", Args: []string{"rev-parse", "HEAD"}, Dir: baseOpts.Repository})
	if err != nil {
		return err
	}
	baseSHA := strings.TrimSpace(string(sha))
	stub, packer, fat := filepath.Join(root, "base-stub"), filepath.Join(root, "base-packer"), filepath.Join(root, "base-fat")
	tags := ""
	if cfg.Profile == minimalProfile {
		tags = minimalProfile
	}
	if err := compile(ctx, baseOpts, stub, "./cmd/microfat-stub", generic, tags); err != nil {
		return err
	}
	if err := compile(ctx, baseOpts, packer, "./cmd/microfat", generic, ""); err != nil {
		return err
	}
	if err := packagePayloads(ctx, cfg, packer, stub, fat, paths); err != nil {
		return err
	}

	for id, path := range map[string]string{"base-stub": stub, "base-packer": packer, "base-fat": fat} {
		artifact, err := identify(path, id, baseSHA)
		if err != nil {
			return err
		}
		built.Dirty = built.Dirty || artifact.Settings["vcs.modified"] == "true"
		built.Artifacts = append(built.Artifacts, artifact)
	}
	original := slices.Clone(built.Configurations)
	built.Configurations = nil
	for _, cfg := range original {
		base, head := cfg, cfg
		base.ID, head.ID = "base-"+cfg.ID, "head-"+cfg.ID
		if cfg.ArtifactID == "fat" {
			base.ArtifactID = "base-fat"
		}
		built.Configurations = append(built.Configurations, base, head)
	}
	return nil
}

func compile(ctx context.Context, opts RunOptions, output, pkg, level, tags string) error {
	args := []string{"build", "-trimpath", "-buildvcs=true", "-ldflags=-s -w -X " + levelSymbol + "=" + level, "-o", output}
	if tags != "" {
		args = append(args, "-tags="+tags)
	}
	args = append(args, pkg)
	env := cleanEnvironment()
	env = append(env, "GOOS=linux", "GOARCH="+runtime.GOARCH, "GOTOOLCHAIN=local")
	if runtime.GOARCH == "arm64" {
		env = append(env, "GOARM64="+level)
	} else {
		env = append(env, "GOAMD64="+level)
	}
	_, stderr, err := process.Run(ctx, process.Spec{Path: opts.Go, Args: args, Dir: opts.Repository, Env: env})
	if err != nil {
		return fmt.Errorf("building %s: %w: %s", pkg, err, stderr)
	}
	return nil
}

func identify(path, id, source string) (schema.Artifact, error) {
	data, err := report.ReadBounded(path)
	if err != nil {
		return schema.Artifact{}, err
	}
	artifact := schema.Artifact{ID: id, Path: path, SHA256: schema.Digest(data), Bytes: int64(len(data)),
		SourceSHA: source, Settings: make(map[string]string)}
	if info, err := buildinfo.ReadFile(path); err == nil {
		artifact.GoVersion = info.GoVersion
		artifact.ModuleDigest = schema.Digest([]byte(info.String()))
		for _, setting := range info.Settings {
			artifact.Settings[setting.Key] = setting.Value
		}
	}
	return artifact, nil
}

func cleanEnvironment() []string {
	var result []string
	for _, name := range []string{"PATH", "HOME", "TMPDIR", "GOCACHE", "GOPATH", "GOPROXY", "GOSUMDB"} {
		if value, ok := os.LookupEnv(name); ok {
			result = append(result, name+"="+value)
		}
	}
	return result
}

func packagePayloads(ctx context.Context, cfg ExperimentConfig, packer, stub, fat string, paths map[string]string) error {
	args := []string{"pack", "--stub", stub, "--output", fat, "--format-version", strconv.Itoa(cfg.Format),
		"--compression", cfg.Codec, "--arch", runtime.GOARCH}
	if cfg.Dictionary {
		args = append(args, "--dict")
	}
	levels := make([]string, 0, len(paths))
	for level := range paths {
		levels = append(levels, level)
	}
	slices.Sort(levels)
	for _, level := range levels {
		args = append(args, "--variant", level+"="+paths[level])
	}
	_, stderr, err := process.Run(ctx, process.Spec{Path: packer, Args: args, Env: cleanEnvironment()})
	if err != nil {
		return fmt.Errorf("packaging failed: %w: %s", err, stderr)
	}
	return nil
}

func buildPacker(ctx context.Context, opts RunOptions, root, generic, source string) (schema.Artifact, error) {
	path := filepath.Join(root, "head-packer")
	if err := compile(ctx, opts, path, "./cmd/microfat", generic, ""); err != nil {
		return schema.Artifact{}, err
	}
	return identify(path, "head-packer", source)
}
