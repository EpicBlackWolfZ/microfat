// Package update coordinates explicit release checks and owned-generation updates.
package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/EpicBlackWolfZ/microfat/internal/builder"
	"github.com/EpicBlackWolfZ/microfat/internal/install"
	"github.com/EpicBlackWolfZ/microfat/internal/installrelease"
)

const (
	metadataOnly     = "metadata_only"
	notPerformed     = "not_performed"
	artifactVerified = "artifact_verified"
	homebrew         = "homebrew"
	unmanaged        = "unmanaged"
)

type Options struct {
	Check, AllowDowngrade, System          bool
	Version, Staging, Cosign, CosignSHA256 string
}

func (o Options) Validate() error {
	if o.Version != "" {
		if _, err := installrelease.ParseVersion(o.Version); err != nil {
			return err
		}
	}
	if o.AllowDowngrade && o.Version == "" {
		return errors.New("--allow-downgrade requires --version")
	}
	if (o.Cosign == "") != (o.CosignSHA256 == "") {
		return errors.New("--cosign and --cosign-sha256 must be supplied together")
	}
	if o.Check && (o.AllowDowngrade || o.System || o.Staging != "" || o.Cosign != "" || o.CosignSHA256 != "") {
		return errors.New("--check cannot use mutation-only flags")
	}
	return nil
}

type Result struct {
	SchemaVersion   int     `json:"schema_version"`
	Status          string  `json:"status"`
	RunningVersion  *string `json:"running_version"`
	CurrentVersion  *string `json:"current_version"`
	TargetVersion   *string `json:"target_version"`
	UpdateAvailable bool    `json:"update_available"`
	Management      *string `json:"management"`
	CanSelfUpdate   bool    `json:"can_self_update"`
	Verification    string  `json:"verification"`
	Activated       bool    `json:"activated"`
	Error           string  `json:"error,omitempty"`
	Guidance        string  `json:"guidance,omitempty"`
}

type releaseClient interface {
	Published(context.Context, string, string) (string, error)
	PrepareVerifier(context.Context, string, string, installrelease.Cosign) (installrelease.Cosign, error)
	Acquire(context.Context, string, string, string, installrelease.Verifier) (install.Generation, string, error)
}

type Service struct {
	version, goos, arch string
	uid                 int
	executable          func() (string, error)
	client              releaseClient
	apply               func(context.Context, install.Snapshot, install.Generation, string, install.ApplyOptions) (install.Result, error)
}

func NewService(version string) *Service {
	return &Service{version: version, goos: runtime.GOOS, arch: runtime.GOARCH, uid: os.Geteuid(),
		executable: builder.ResolveInstallationExecutable, client: installrelease.NewClient(), apply: install.Apply}
}

func (s *Service) Run(ctx context.Context, opts Options) (Result, error) {
	result := Result{SchemaVersion: 1, Status: "error", Verification: notPerformed}
	err := s.run(ctx, opts, &result)
	if err != nil {
		result.Status, result.Error = "error", err.Error()
	}
	return result, err
}

func (s *Service) run(ctx context.Context, opts Options, result *Result) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.goos != "linux" || (s.arch != "amd64" && s.arch != "arm64") {
		return errors.New("update supports Linux amd64 and arm64")
	}
	running, err := installrelease.ParseVersion(s.version)
	if err != nil {
		return fmt.Errorf("cannot determine a supported stable running version; use the verified installer: %w", err)
	}
	result.RunningVersion, result.CurrentVersion = &running, &running
	physical, err := s.executable()
	if err != nil {
		return err
	}
	snapshot, err := s.inspect(physical, opts, result)
	if err != nil {
		return err
	}
	target, err := s.client.Published(ctx, opts.Version, s.arch)
	if err != nil {
		return err
	}
	result.TargetVersion, result.Verification = &target, metadataOnly
	comparison := installrelease.CompareVersions(target, *result.CurrentVersion)
	result.UpdateAvailable = comparison > 0
	result.Status = selectionStatus(comparison, opts.Version != "")
	if opts.Check {
		return nil
	}
	if comparison == 0 || (comparison < 0 && opts.Version == "") {
		return nil
	}
	if comparison < 0 && !opts.AllowDowngrade {
		return errors.New("refusing downgrade; select --version and --allow-downgrade explicitly")
	}
	return s.activate(ctx, opts, snapshot, target, physical, result)
}

func selectionStatus(comparison int, explicit bool) string {
	if comparison > 0 {
		return "update_available"
	}
	if comparison == 0 {
		return "current"
	}
	if explicit {
		return "selected_older"
	}
	return "installed_newer"
}

func (s *Service) inspect(physical string, opts Options, result *Result) (install.Snapshot, error) {
	var snapshot install.Snapshot
	managed, err := install.ValidateDiscovery(physical, physical)
	if err != nil {
		result.Guidance = "Inspect the managed installation and use the verified installer for explicit repair."
		return snapshot, err
	}
	if !managed {
		management, err := externalManagement(physical)
		if err != nil {
			return snapshot, err
		}
		result.Management = &management
		result.Guidance = externalGuidance(management)
		if !opts.Check {
			return snapshot, errors.New(result.Guidance)
		}
		return snapshot, nil
	}
	management := install.OwnerKind
	result.Management = &management
	observed, err := install.ReadInstallation(physical)
	if err != nil {
		return snapshot, err
	}
	if observed.Running.Version != *result.RunningVersion || observed.Running.Arch != s.arch || observed.Current.Arch != s.arch {
		return snapshot, errors.New("running build identity disagrees with installation metadata")
	}
	result.CurrentVersion = &observed.Current.Version
	sameGeneration := observed.Running.ID == observed.Current.ID
	result.CanSelfUpdate = sameGeneration && observed.Owner.UID == s.uid
	if !sameGeneration {
		result.Guidance = "Restart through the public microfat entrypoint; a different generation is active."
	}
	if observed.Owner.UID != s.uid {
		result.Guidance = "Run the update as the installation owner; system installations require root and --system."
	}
	if opts.Check {
		return snapshot, nil
	}
	if opts.System && s.uid != 0 {
		return snapshot, errors.New("--system requires root")
	}
	if s.uid == 0 && !opts.System {
		return snapshot, errors.New("root updates require explicit --system; no sudo is invoked")
	}
	if !result.CanSelfUpdate {
		return snapshot, fmt.Errorf("%w: %s", install.ErrChanged, result.Guidance)
	}
	snapshot, err = install.InspectUpdate(physical)
	if err != nil {
		return snapshot, err
	}
	current, err := snapshot.Current()
	if err != nil {
		return snapshot, err
	}
	if current == nil || current.ID != observed.Current.ID || snapshot.Owner() == nil || *snapshot.Owner() != observed.Owner {
		return snapshot, install.ErrChanged
	}
	return snapshot, nil
}

func (s *Service) activate(
	ctx context.Context, opts Options, snapshot install.Snapshot, target, physical string, result *Result,
) (err error) {
	staging, err := installrelease.Staging(opts.Staging)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()
	verifier, err := s.client.PrepareVerifier(ctx, s.arch, staging, installrelease.Cosign{Path: opts.Cosign, SHA256: opts.CosignSHA256})
	if err != nil {
		return err
	}
	generation, source, err := s.client.Acquire(ctx, target, s.arch, staging, verifier)
	if err != nil {
		return err
	}
	result.Verification = artifactVerified
	currentExecutable, err := s.executable()
	if err != nil || currentExecutable != physical {
		return errors.Join(install.ErrChanged, err)
	}
	applied, err := s.apply(ctx, snapshot, generation, source, install.ApplyOptions{})
	result.Activated = applied.Activated
	if applied.Activated {
		result.CurrentVersion = &applied.Generation.Version
		result.UpdateAvailable = false
		result.CanSelfUpdate = false
	}
	if err != nil {
		if applied.Activated {
			return fmt.Errorf("v%s is active, but update completion failed: %w", applied.Generation.Version, err)
		}
		return err
	}
	result.Status, result.UpdateAvailable = "updated", false
	if installrelease.CompareVersions(target, *result.RunningVersion) < 0 {
		result.Status = "downgraded"
	}
	result.CurrentVersion = &applied.Generation.Version
	result.Guidance = "The selected release is active. Start subsequent commands through the public entrypoint."
	return nil
}
