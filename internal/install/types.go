// Package install manages verified release generations and installer-owned entrypoints.
// Callers authenticate release artifacts before applying a generation. Local metadata
// records that decision; it is not itself a publisher authentication mechanism.
package install

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
)

const (
	SchemaVersion            = 1
	OwnerKind                = "microfat-installer"
	ownerFile                = "owner.json"
	generationFile           = "generation.json"
	currentLink              = "current"
	generationDir            = "generations"
	lockFile                 = ".lock"
	metadataLimit            = 32 * 1024
	MaxFileBytes       int64 = 250 * 1024 * 1024
	directoryMode            = 0o755
	privateMode              = 0o700
	metadataMode             = 0o600
	publicMetadataMode       = 0o644
)

var (
	ErrOwnership   = errors.New("installation ownership validation failed")
	ErrChanged     = errors.New("installation changed; inspect it again before retrying")
	ErrConflict    = errors.New("destination contains an unmanaged or modified entrypoint")
	ErrCorrupt     = errors.New("installed generation is corrupt; explicit repair is required")
	ErrUnsupported = errors.New("installer requires Linux")
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	idPattern      = regexp.MustCompile(`^[0-9a-f]{32}$`)
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

// Products returns the complete installed executable set.
func Products() []string { return []string{"microfat", "microfat-stub", "microfat-stub-minimal"} }

type Paths struct{ Bin, Store string }

func (p Paths) Validate() error {
	for _, name := range []string{p.Bin, p.Store} {
		if !filepath.IsAbs(name) || filepath.Clean(name) != name || name == string(filepath.Separator) {
			return fmt.Errorf("installation roots must be clean absolute non-root paths: %q", name)
		}
	}
	if within(p.Bin, p.Store) || within(p.Store, p.Bin) {
		return errors.New("installation store and bin directories must not overlap")
	}
	return nil
}

func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "." || (rel != ".." && !filepath.IsAbs(rel) && !startsParent(rel)))
}

func startsParent(path string) bool {
	return len(path) > 3 && path[:3] == ".."+string(filepath.Separator)
}

type Owner struct {
	Schema int    `json:"schema"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	UID    int    `json:"uid"`
	Bin    string `json:"bin"`
	Store  string `json:"store"`
}

type File struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Generation struct {
	Schema        int             `json:"schema"`
	ID            string          `json:"id"`
	Version       string          `json:"version"`
	Arch          string          `json:"arch"`
	ArchiveSHA256 string          `json:"archive_sha256"`
	Files         map[string]File `json:"files"`
}

func (g Generation) Validate() error {
	if g.Schema != SchemaVersion || !idPattern.MatchString(g.ID) || !versionPattern.MatchString(g.Version) ||
		(g.Arch != "amd64" && g.Arch != "arm64") || !digestPattern.MatchString(g.ArchiveSHA256) {
		return errors.New("invalid generation identity or schema")
	}
	if len(g.Files) != len(Products()) {
		return errors.New("generation must contain exactly the three products")
	}
	for _, name := range Products() {
		file := g.Files[name]
		if file.Size <= 0 || file.Size > MaxFileBytes || !digestPattern.MatchString(file.SHA256) {
			return fmt.Errorf("invalid generation file metadata: %s", name)
		}
	}
	return nil
}

// Snapshot binds an operation to the installation observed before acquisition.
// Its private fields prevent callers from manufacturing an ownership decision.
type Snapshot struct {
	paths         Paths
	owner         *Owner
	binInfo       os.FileInfo
	storeInfo     os.FileInfo
	currentTarget string
	current       *Generation
	currentError  error
	updateOnly    bool
	updateFiles   map[string]os.FileInfo
}

func (s Snapshot) Owner() *Owner {
	if s.owner == nil {
		return nil
	}
	copyOwner := *s.owner
	return &copyOwner
}

// Current reports the selected generation metadata observed with this snapshot.
// Apply revalidates the selection under the lock before making changes.
func (s Snapshot) Current() (*Generation, error) {
	if s.current == nil {
		return nil, s.currentError
	}
	generation := *s.current
	generation.Files = maps.Clone(generation.Files)
	return &generation, s.currentError
}

type Result struct {
	Generation Generation
	Activated  bool
	Reused     bool
	Removed    []string
	Preserved  []string
}

// ApplyOptions controls explicit repair of a corrupt owned generation.
type ApplyOptions struct{ Repair bool }
