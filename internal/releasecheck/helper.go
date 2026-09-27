package releasecheck

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/inputfile"
)

type ArtifactKind string

const ProductArchive ArtifactKind = "product-archive"
const NativeHelper ArtifactKind = "native-installer"

type ArtifactIdentity struct {
	ArchiveIdentity
	Kind ArtifactKind
}

// ParseReleaseArtifactName distinguishes the raw native helper from a fat
// product archive. It does not admit arbitrary extra release payloads.
func ParseReleaseArtifactName(name string) (ArtifactIdentity, error) {
	base := filepath.Base(name)
	if !strings.HasPrefix(base, ReleaseInstaller+"_") {
		identity, err := ParseReleaseArchiveName(name)
		return ArtifactIdentity{ArchiveIdentity: identity, Kind: ProductArchive}, err
	}
	for _, arch := range []string{ArchAMD64, ArchARM64} {
		prefix, suffix := ReleaseInstaller+"_", "_linux_"+arch
		if !strings.HasSuffix(base, suffix) {
			continue
		}
		version := strings.TrimSuffix(strings.TrimPrefix(base, prefix), suffix)
		contract, err := NewReleaseContract(version)
		if err == nil && base == contract.ExpectedHelpers[arch] {
			return ArtifactIdentity{ArchiveIdentity: ArchiveIdentity{Version: version, Arch: arch}, Kind: NativeHelper}, nil
		}
	}
	return ArtifactIdentity{}, fmt.Errorf("invalid installer helper artifact name %q", base)
}

// ValidateArtifact dispatches only after checking the explicit versioned kind.
func ValidateArtifact(filename string) (*ArchiveFacts, error) {
	identity, err := ParseReleaseArtifactName(filename)
	if err != nil {
		return nil, err
	}
	if identity.Kind == NativeHelper {
		return validateHelper(filename, identity)
	}
	contract, err := NewReleaseContract(identity.Version)
	if err != nil {
		return nil, err
	}
	return ValidateArchive(filename, identity.Arch, contract)
}

func validateHelper(filename string, identity ArtifactIdentity) (*ArchiveFacts, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSingleFileBytes {
		return nil, fmt.Errorf("invalid native installer file type or size")
	}
	file, err := inputfile.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxSingleFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Size() || len(data) > maxSingleFileBytes {
		return nil, fmt.Errorf("native installer changed size while reading")
	}
	return helperFacts(data, filepath.Base(filename), identity.Arch)
}

func helperFacts(data []byte, name, arch string) (*ArchiveFacts, error) {
	reader := bytes.NewReader(data)
	if format.IsFatBinary(reader, int64(len(data))) {
		return nil, fmt.Errorf("installer helper must be a native ELF")
	}
	bi, err := buildinfo.Read(reader)
	if err != nil {
		return nil, err
	}
	header, err := elf.NewFile(reader)
	if err != nil {
		return nil, err
	}
	defer func() { _ = header.Close() }()
	if header.Class != elf.ELFCLASS64 || header.Data != elf.ELFDATA2LSB || (header.Type != elf.ET_EXEC && header.Type != elf.ET_DYN) {
		return nil, fmt.Errorf("invalid native installer ELF header")
	}
	for _, program := range header.Progs {
		if program.Type == elf.PT_INTERP {
			return nil, fmt.Errorf("installer helper must not require an ELF interpreter")
		}
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	exe := &ExecutableFacts{Name: ReleaseInstaller, SHA256: digest, Size: int64(len(data)), BuildInfo: bi, ELFHeader: &header.FileHeader}
	if err := validateRootExecutableISA(arch, ReleaseInstaller, exe); err != nil {
		return nil, err
	}
	if bi.Path != "github.com/EpicBlackWolfZ/microfat/cmd/microfat-install" {
		return nil, fmt.Errorf("wrong native installer main package")
	}
	settings := map[string]string{"GOOS": "linux", "GOARCH": arch, "CGO_ENABLED": "0"}
	if arch == ArchAMD64 {
		settings["GOAMD64"] = "v1"
	} else {
		settings["GOARM64"] = "v8.0"
	}
	for key, expected := range settings {
		if actual, found := getBuildSetting(bi, key); !found || actual != expected {
			return nil, fmt.Errorf("installer helper %s must be %s", key, expected)
		}
	}
	return &ArchiveFacts{Kind: NativeHelper, ArchiveName: name, ArchiveSHA256: digest, TargetArch: arch,
		Executables: map[string]*ExecutableFacts{ReleaseInstaller: exe}, EmbeddedVariants: map[string]*VariantFacts{}}, nil
}
