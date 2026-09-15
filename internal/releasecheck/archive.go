package releasecheck

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const (
	maxSingleFileBytes   = 250 * 1024 * 1024 // 250 MB
	maxTotalExtractBytes = 500 * 1024 * 1024 // 500 MB
	dirPerms             = 0o755
	filePerms            = 0o644
	execPerms            = 0o755
	drainBufferSize      = 4096
)

// VariantFacts contains facts extracted from a single variant inside a fat binary.
type VariantFacts struct {
	Level            string
	Offset           int64
	CompressedSize   int64
	UncompressedSize int64
	SHA256           string
	Compression      string
	BuildInfo        *buildinfo.BuildInfo
	ELFHeader        *elf.FileHeader
	Data             []byte
}

// ExecutableFacts contains facts about an extracted executable binary.
type ExecutableFacts struct {
	Name      string
	SHA256    string
	Size      int64
	BuildInfo *buildinfo.BuildInfo
	ELFHeader *elf.FileHeader
}

// ArchiveFacts contains verified facts extracted from a release archive.
type ArchiveFacts struct {
	ArchiveName      string
	ArchiveSHA256    string
	TargetArch       string
	StagingDir       string
	ExtractedDir     string
	Entries          map[string]bool
	Executables      map[string]*ExecutableFacts
	EmbeddedVariants map[string]*VariantFacts
}

// Cleanup safely removes the temporary staging directory and clears paths.
func (af *ArchiveFacts) Cleanup() error {
	if af == nil || af.StagingDir == "" {
		return nil
	}
	err := os.RemoveAll(af.StagingDir)
	af.StagingDir = ""
	af.ExtractedDir = ""
	return err
}

func sanitizeTarEntryName(rawName string) (string, error) {
	if strings.HasPrefix(rawName, "/") || strings.Contains(rawName, "\\") || strings.Contains(rawName, "\x00") {
		return "", fmt.Errorf("illegal relative or absolute path in archive: %s", rawName)
	}

	cleanName := path.Clean(rawName)
	if cleanName == "." {
		return "", nil // harmless root directory entry
	}
	cleanName = strings.TrimPrefix(cleanName, "./")
	if cleanName == "" || cleanName == "." {
		return "", nil // harmless root directory entry
	}

	if cleanName == ".." || strings.HasPrefix(cleanName, "../") {
		return "", fmt.Errorf("illegal relative or absolute path in archive: %s", rawName)
	}
	return cleanName, nil
}

func extractArchiveFileToDisk(tr io.Reader, hdr *tar.Header, destPath string, isRequiredExe bool, total *int64) (*ExecutableFacts, error) {
	if hdr.Size > maxSingleFileBytes {
		return nil, fmt.Errorf("file %s exceeds size limit (%d > %d)", hdr.Name, hdr.Size, maxSingleFileBytes)
	}
	*total += hdr.Size
	if *total > maxTotalExtractBytes {
		return nil, fmt.Errorf("archive exceeds total size limit (%d)", maxTotalExtractBytes)
	}

	if isRequiredExe && (hdr.Mode&0o111 == 0) {
		return nil, fmt.Errorf("required executable %s must have executable permissions, got mode %o", destPath, hdr.Mode)
	}

	perms := os.FileMode(filePerms)
	if hdr.Mode&0o111 != 0 {
		perms = execPerms
	}

	if err := os.MkdirAll(filepath.Dir(destPath), dirPerms); err != nil {
		return nil, fmt.Errorf("creating parent dir for %s: %w", destPath, err)
	}

	// #nosec G304 -- destPath generated inside staging directory
	outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perms)
	if err != nil {
		return nil, fmt.Errorf("creating output file %s: %w", destPath, err)
	}

	fileHasher := sha256.New()
	mw := io.MultiWriter(outFile, fileHasher)
	written, copyErr := io.Copy(mw, io.LimitReader(tr, maxSingleFileBytes))
	closeErr := outFile.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("extracting file %s: %w", destPath, copyErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("closing extracted file %s: %w", destPath, closeErr)
	}
	if written != hdr.Size {
		return nil, fmt.Errorf("size mismatch for %s: expected %d, got %d", destPath, hdr.Size, written)
	}

	if !isRequiredExe {
		return nil, nil
	}

	ef := &ExecutableFacts{
		Name:   filepath.Base(destPath),
		SHA256: hex.EncodeToString(fileHasher.Sum(nil)),
		Size:   written,
	}
	if bi, err := buildinfo.ReadFile(destPath); err == nil {
		ef.BuildInfo = bi
	}
	if efHeader, err := elf.Open(destPath); err == nil {
		ef.ELFHeader = &efHeader.FileHeader
		_ = efHeader.Close()
	}
	return ef, nil
}

// ValidateArchive safely preflights, extracts, and validates an archive according to the contract.
func ValidateArchive(archivePath, expectedArch string, contract *ReleaseContract) (*ArchiveFacts, error) {
	// #nosec G304 -- archivePath provided by caller
	af, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("opening archive: %w", err)
	}
	defer func() { _ = af.Close() }()

	h := sha256.New()
	trArchive := io.TeeReader(af, h)

	gzr, err := gzip.NewReader(trArchive)
	if err != nil {
		return nil, fmt.Errorf("reading gzip header: %w", err)
	}
	defer func() { _ = gzr.Close() }()

	stagingDir, err := os.MkdirTemp("", "microfat-archive-check-*")
	if err != nil {
		return nil, fmt.Errorf("creating staging directory: %w", err)
	}

	var success bool
	defer func() {
		if !success {
			_ = os.RemoveAll(stagingDir)
		}
	}()

	extractedDir := filepath.Join(stagingDir, "archive")
	if err := os.MkdirAll(extractedDir, dirPerms); err != nil {
		return nil, fmt.Errorf("creating archive extraction directory: %w", err)
	}

	facts := &ArchiveFacts{
		ArchiveName:      filepath.Base(archivePath),
		TargetArch:       expectedArch,
		StagingDir:       stagingDir,
		ExtractedDir:     extractedDir,
		Entries:          make(map[string]bool),
		Executables:      make(map[string]*ExecutableFacts),
		EmbeddedVariants: make(map[string]*VariantFacts),
	}

	if err := processArchiveEntries(tar.NewReader(gzr), extractedDir, facts); err != nil {
		return nil, err
	}

	if err := drainArchiveTrailer(trArchive); err != nil {
		return nil, err
	}
	facts.ArchiveSHA256 = hex.EncodeToString(h.Sum(nil))

	for _, req := range contract.RequiredExecutables {
		if _, ok := facts.Executables[req]; !ok {
			return nil, fmt.Errorf("missing required root executable in archive: %s", req)
		}
	}

	microfatPath := filepath.Join(extractedDir, ReleaseProjectName)
	if err := inspectFatBinary(microfatPath, expectedArch, contract, facts); err != nil {
		return nil, fmt.Errorf("validating fat binary in archive: %w", err)
	}

	if err := validateISABuildSettings(expectedArch, contract, facts); err != nil {
		return nil, fmt.Errorf("validating ISA build settings: %w", err)
	}

	success = true
	return facts, nil
}

func processArchiveEntries(tr *tar.Reader, stagingDir string, facts *ArchiveFacts) error {
	var totalExtracted int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar entry: %w", err)
		}

		cleanName, err := sanitizeTarEntryName(hdr.Name)
		if err != nil {
			return err
		}
		if cleanName == "" {
			continue
		}

		if facts.Entries[cleanName] {
			return fmt.Errorf("duplicate entry in archive: %q", cleanName)
		}
		facts.Entries[cleanName] = true

		base := path.Base(cleanName)
		isRequiredExe := base == ReleaseProjectName || base == ReleaseFullStub || base == ReleaseMinStub
		if isRequiredExe && cleanName != base {
			return fmt.Errorf("nested collision with required executable name: %q", cleanName)
		}

		destPath := filepath.Join(stagingDir, filepath.FromSlash(cleanName))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destPath, dirPerms); err != nil {
				return fmt.Errorf("creating directory %s: %w", cleanName, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			ef, err := extractArchiveFileToDisk(tr, hdr, destPath, isRequiredExe, &totalExtracted)
			if err != nil {
				return err
			}
			if isRequiredExe && ef != nil {
				facts.Executables[cleanName] = ef
			}
		default:
			return fmt.Errorf("unsupported entry type %v for %s", hdr.Typeflag, cleanName)
		}
	}
	return nil
}

func drainArchiveTrailer(r io.Reader) error {
	var drainBuf [drainBufferSize]byte
	for {
		_, err := r.Read(drainBuf[:])
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading archive trailer: %w", err)
		}
	}
}

func inspectFatBinary(fatPath, expectedArch string, contract *ReleaseContract, facts *ArchiveFacts) error {
	// #nosec G304 -- fatPath within validated staging directory
	f, err := os.Open(fatPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return err
	}

	idx, err := format.ReadTrailerAndIndex(f, fi.Size())
	if err != nil {
		return fmt.Errorf("reading index and trailer: %w", err)
	}

	if idx.Version != format.FormatVersion2 {
		return fmt.Errorf("expected format v2 index, got version %d", idx.Version)
	}

	if idx.TargetArch != expectedArch {
		return fmt.Errorf("target arch mismatch: expected %s, got %s", expectedArch, idx.TargetArch)
	}

	expectedTiers, ok := contract.ExpectedTiers[expectedArch]
	if !ok {
		return fmt.Errorf("unknown architecture in contract: %s", expectedArch)
	}

	dictBytes, err := readSharedDictBytes(f, idx)
	if err != nil {
		return err
	}

	if err := decompressAndVerifyVariants(f, idx, expectedTiers, dictBytes, facts); err != nil {
		return err
	}

	return nil
}

func readSharedDictBytes(f *os.File, idx *format.Index) ([]byte, error) {
	if idx.DictionarySize <= 0 {
		return nil, nil
	}
	dictBytes := make([]byte, idx.DictionarySize)
	if _, err := f.ReadAt(dictBytes, idx.DictionaryOffset); err != nil {
		return nil, fmt.Errorf("reading shared dictionary: %w", err)
	}
	return dictBytes, nil
}

func decompressAndVerifyVariants(f *os.File, idx *format.Index, expectedTiers []string, dictBytes []byte, facts *ArchiveFacts) error {
	tierMap := make(map[string]bool)
	for _, t := range expectedTiers {
		tierMap[t] = true
	}

	for _, v := range idx.Variants {
		if !tierMap[v.Level] {
			return fmt.Errorf("unexpected variant level %s in %s fat binary", v.Level, idx.TargetArch)
		}

		c, err := codec.Get(v.Compression)
		if err != nil {
			return fmt.Errorf("getting codec %s: %w", v.Compression, err)
		}

		var decompBuf bytes.Buffer
		hasher := sha256.New()
		mw := io.MultiWriter(&decompBuf, hasher)
		secReader := io.NewSectionReader(f, v.Offset, v.CompressedSize)

		if err := codec.DecompressWithOptionalDict(c, mw, secReader, v.UncompressedSize, dictBytes); err != nil {
			return fmt.Errorf("decompressing variant %s: %w", v.Level, err)
		}

		actualHash := hex.EncodeToString(hasher.Sum(nil))
		if actualHash != v.SHA256 {
			return fmt.Errorf("checksum mismatch for variant %s: expected %s, got %s", v.Level, v.SHA256, actualHash)
		}

		payloadBytes := decompBuf.Bytes()
		vf := &VariantFacts{
			Level:            v.Level,
			Offset:           v.Offset,
			CompressedSize:   v.CompressedSize,
			UncompressedSize: v.UncompressedSize,
			SHA256:           actualHash,
			Compression:      v.Compression,
			Data:             payloadBytes,
		}

		if bi, err := buildinfo.Read(bytes.NewReader(payloadBytes)); err == nil {
			vf.BuildInfo = bi
		}
		if elfFile, err := elf.NewFile(bytes.NewReader(payloadBytes)); err == nil {
			vf.ELFHeader = &elfFile.FileHeader
			_ = elfFile.Close()
		}

		facts.EmbeddedVariants[v.Level] = vf
	}

	for _, expectedTier := range expectedTiers {
		if _, ok := facts.EmbeddedVariants[expectedTier]; !ok {
			return fmt.Errorf("missing expected tier %s in %s fat binary", expectedTier, idx.TargetArch)
		}
	}
	return nil
}

// ExtractArchiveSafely preflights and safely extracts an archive to targetDir.
func ExtractArchiveSafely(archivePath, targetDir string) error {
	// #nosec G304 -- archivePath provided by caller
	af, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("opening archive %s: %w", archivePath, err)
	}
	defer func() { _ = af.Close() }()

	gzr, err := gzip.NewReader(af)
	if err != nil {
		return fmt.Errorf("creating gzip reader: %w", err)
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	seenEntries := make(map[string]bool)
	var totalExtracted int64

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar header: %w", err)
		}

		cleanName, err := sanitizeTarEntryName(hdr.Name)
		if err != nil {
			return err
		}
		if cleanName == "" {
			continue
		}

		if seenEntries[cleanName] {
			return fmt.Errorf("duplicate entry in archive: %s", cleanName)
		}
		seenEntries[cleanName] = true

		destPath := filepath.Join(targetDir, filepath.FromSlash(cleanName))
		cleanTargetDir := filepath.Clean(targetDir) + string(filepath.Separator)
		if !strings.HasPrefix(destPath, cleanTargetDir) && destPath != filepath.Clean(targetDir) {
			return fmt.Errorf("path escapes target directory: %s", destPath)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destPath, dirPerms); err != nil {
				return fmt.Errorf("creating directory %s: %w", destPath, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if _, err := extractArchiveFileToDisk(tr, hdr, destPath, false, &totalExtracted); err != nil {
				return err
			}
		default:
			return fmt.Errorf("refusing to extract unsafe archive entry %s of type %v", hdr.Name, hdr.Typeflag)
		}
	}

	return drainArchiveTrailer(af)
}

// ExtractFileFromArchive safely validates the entire archive and extracts targetName at root to destPath.
func ExtractFileFromArchive(archivePath, targetName, destPath string) error {
	// #nosec G304 -- archivePath provided by caller
	af, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = af.Close() }()

	gzr, err := gzip.NewReader(af)
	if err != nil {
		return err
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	seenEntries := make(map[string]bool)
	var matchedBytes []byte
	found := false

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}

		cleanName, err := sanitizeTarEntryName(hdr.Name)
		if err != nil {
			return err
		}
		if cleanName == "" {
			continue
		}

		if seenEntries[cleanName] {
			return fmt.Errorf("duplicate entry in archive: %s", cleanName)
		}
		seenEntries[cleanName] = true

		if cleanName == targetName {
			if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
				return fmt.Errorf("target entry %s is not a regular file", targetName)
			}
			data, err := io.ReadAll(io.LimitReader(tr, maxSingleFileBytes))
			if err != nil {
				return fmt.Errorf("reading target entry %s: %w", targetName, err)
			}
			matchedBytes = data
			found = true
		}
	}

	if !found {
		return fmt.Errorf("target file %q not found in archive %s", targetName, archivePath)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), dirPerms); err != nil {
		return err
	}

	return os.WriteFile(destPath, matchedBytes, execPerms)
}

func getBuildSetting(bi *buildinfo.BuildInfo, key string) (string, bool) {
	if bi == nil {
		return "", false
	}
	for _, s := range bi.Settings {
		if s.Key == key {
			return s.Value, true
		}
	}
	return "", false
}

func validateRootExecutableISA(expectedArch, req string, exe *ExecutableFacts) error {
	if exe == nil {
		return fmt.Errorf("missing executable facts for %s", req)
	}
	expectedMachine := elf.EM_X86_64
	expectedSettingKey := "GOAMD64"
	expectedSettingVal := "v1"
	if expectedArch == ArchARM64 {
		expectedMachine = elf.EM_AARCH64
		expectedSettingKey = "GOARM64"
		expectedSettingVal = "v8.0"
	}

	if exe.ELFHeader == nil || exe.ELFHeader.Machine != expectedMachine {
		return fmt.Errorf("root executable %s is not a valid %s ELF binary", req, expectedArch)
	}
	if exe.BuildInfo == nil {
		return fmt.Errorf("root executable %s is missing Go buildinfo", req)
	}

	val, found := getBuildSetting(exe.BuildInfo, expectedSettingKey)
	if found && val != expectedSettingVal {
		return fmt.Errorf("root executable %s %s setting mismatch: expected %s, got %s", req, expectedSettingKey, expectedSettingVal, val)
	}
	return nil
}

func validateEmbeddedVariantISA(expectedArch, tier string, vf *VariantFacts) error {
	if vf == nil {
		return fmt.Errorf("missing variant facts for tier %s", tier)
	}
	expectedMachine := elf.EM_X86_64
	settingKey := "GOAMD64"
	expectedVal := tier
	baseline := "v1"
	if expectedArch == ArchARM64 {
		expectedMachine = elf.EM_AARCH64
		settingKey = "GOARM64"
		baseline = "v8.0"
	}

	if vf.ELFHeader == nil || vf.ELFHeader.Machine != expectedMachine {
		return fmt.Errorf("embedded variant %s is not a valid %s ELF binary", tier, expectedArch)
	}
	if vf.BuildInfo == nil {
		return fmt.Errorf("embedded variant %s is missing Go buildinfo", tier)
	}

	val, found := getBuildSetting(vf.BuildInfo, settingKey)
	if !found {
		if tier != baseline {
			return fmt.Errorf("embedded variant %s missing required %s setting (expected %s)", tier, settingKey, expectedVal)
		}
	} else if val != expectedVal {
		return fmt.Errorf("embedded variant %s %s mismatch: expected %s, got %s", tier, settingKey, expectedVal, val)
	}
	return nil
}

func validateISABuildSettings(expectedArch string, contract *ReleaseContract, facts *ArchiveFacts) error {
	for _, req := range contract.RequiredExecutables {
		exe := facts.Executables[req]
		if err := validateRootExecutableISA(expectedArch, req, exe); err != nil {
			return err
		}
	}

	for tier, vf := range facts.EmbeddedVariants {
		if err := validateEmbeddedVariantISA(expectedArch, tier, vf); err != nil {
			return err
		}
	}
	return nil
}

