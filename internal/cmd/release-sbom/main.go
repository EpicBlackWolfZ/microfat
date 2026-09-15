// Package main provides the payload-aware release SBOM generation tool for microfat archives.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EpicBlackWolfZ/microfat/internal/codec"
	"github.com/EpicBlackWolfZ/microfat/internal/format"
	"github.com/EpicBlackWolfZ/microfat/internal/releasecheck"
)

const (
	dirPerms      = 0o755
	filePerms     = 0o644
	execPerms     = 0o755
	keyValueParts = 2

	formatSPDXJSON      = "spdx-json"
	formatCycloneDXJSON = "cyclonedx-json"
	hashAlgSHA256       = "SHA-256"
)

func parseArgs(args []string) (archivePath, outputPath, formatName string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--output" || arg == "-output":
			if i+1 < len(args) {
				outputPath = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--output="):
			outputPath = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-output="):
			outputPath = strings.TrimPrefix(arg, "-output=")
		case arg == "--format" || arg == "-format":
			if i+1 < len(args) {
				formatName = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--format="):
			formatName = strings.TrimPrefix(arg, "--format=")
		case strings.HasPrefix(arg, "-format="):
			formatName = strings.TrimPrefix(arg, "-format=")
		case arg == "--archive" || arg == "-archive":
			if i+1 < len(args) {
				archivePath = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--archive="):
			archivePath = strings.TrimPrefix(arg, "--archive=")
		case strings.HasPrefix(arg, "-archive="):
			archivePath = strings.TrimPrefix(arg, "-archive=")
		case !strings.HasPrefix(arg, "-") && archivePath == "":
			archivePath = arg
		}
	}

	if archivePath == "" {
		return "", "", "", errors.New("missing archive path (provide positional argument or --archive)")
	}

	if strings.Contains(outputPath, "=") {
		parts := strings.SplitN(outputPath, "=", keyValueParts)
		formatName = parts[0]
		outputPath = parts[1]
	}

	if outputPath == "" {
		return "", "", "", errors.New("missing output path (provide --output)")
	}

	if formatName == "" {
		switch {
		case strings.HasSuffix(outputPath, ".spdx.json"):
			formatName = formatSPDXJSON
		case strings.HasSuffix(outputPath, ".cyclonedx.json"):
			formatName = formatCycloneDXJSON
		default:
			return "", "", "", errors.New("unable to determine format: specify --format or use format=path in --output")
		}
	}

	return archivePath, outputPath, formatName, nil
}

func extractArchiveSafely(archivePath, targetDir string) error {
	return releasecheck.ExtractArchiveSafely(archivePath, targetDir)
}

func readSharedDictionary(f *os.File, idx *format.Index) ([]byte, error) {
	if idx.DictionarySize <= 0 {
		return nil, nil
	}
	if idx.DictionarySize > format.MaxDictionarySize || idx.DictionaryOffset < 0 {
		return nil, fmt.Errorf("%w: dictionary size %d or offset %d out of bounds",
			format.ErrInvalidDictionary, idx.DictionarySize, idx.DictionaryOffset)
	}
	if idx.DictionarySHA256 == "" || !format.ValidateChecksum(idx.DictionarySHA256) {
		return nil, fmt.Errorf("%w: dictionary missing or invalid sha256 checksum", format.ErrInvalidChecksum)
	}
	dictBytes := make([]byte, idx.DictionarySize)
	if _, err := f.ReadAt(dictBytes, idx.DictionaryOffset); err != nil {
		return nil, fmt.Errorf("reading shared dictionary: %w", err)
	}
	dictHash := sha256.Sum256(dictBytes)
	if hex.EncodeToString(dictHash[:]) != idx.DictionarySHA256 {
		return nil, fmt.Errorf("%w: dictionary hash mismatch", format.ErrInvalidChecksum)
	}
	return dictBytes, nil
}

func extractSingleVariant(f *os.File, v format.VariantEntry, dictBytes []byte, stagingDir string) error {
	if v.SHA256 == "" || !format.ValidateChecksum(v.SHA256) {
		return fmt.Errorf("%w: invalid checksum for variant %s", format.ErrInvalidChecksum, v.Level)
	}
	if v.UncompressedSize <= 0 || v.UncompressedSize > format.MaxPayloadSize {
		return fmt.Errorf("%w: invalid uncompressed size %d for variant %s",
			format.ErrPayloadTooLarge, v.UncompressedSize, v.Level)
	}
	if v.Offset < 0 || v.CompressedSize <= 0 {
		return fmt.Errorf("invalid offset %d or compressed size %d for variant %s",
			v.Offset, v.CompressedSize, v.Level)
	}

	c, err := codec.Get(v.Compression)
	if err != nil {
		return fmt.Errorf("getting codec %s for variant %s: %w", v.Compression, v.Level, err)
	}

	variantPath := filepath.Join(stagingDir, fmt.Sprintf("microfat-variant-%s", v.Level))
	// #nosec G304 -- variantPath within temporary staging dir
	outFile, err := os.OpenFile(variantPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, execPerms)
	if err != nil {
		return fmt.Errorf("creating file for variant %s: %w", v.Level, err)
	}

	hasher := sha256.New()
	mw := io.MultiWriter(outFile, hasher)
	secReader := io.NewSectionReader(f, v.Offset, v.CompressedSize)

	if err := codec.DecompressWithOptionalDict(c, mw, secReader, v.UncompressedSize, dictBytes); err != nil {
		_ = outFile.Close()
		return fmt.Errorf("decompressing variant %s: %w", v.Level, err)
	}

	if err := outFile.Close(); err != nil {
		return fmt.Errorf("closing extracted variant %s: %w", v.Level, err)
	}

	actualHash := hex.EncodeToString(hasher.Sum(nil))
	if actualHash != v.SHA256 {
		return fmt.Errorf("checksum mismatch for variant %s: expected %s, got %s", v.Level, v.SHA256, actualHash)
	}
	return nil
}

func extractVariantsFromFatBinary(fatBinaryPath, stagingDir string) error {
	// #nosec G304 -- fatBinaryPath within temporary staging dir
	f, err := os.Open(fatBinaryPath)
	if err != nil {
		return fmt.Errorf("opening fat binary %s: %w", fatBinaryPath, err)
	}
	defer func() { _ = f.Close() }()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat fat binary: %w", err)
	}

	idx, err := format.ReadTrailerAndIndex(f, fi.Size())
	if err != nil {
		return fmt.Errorf("reading fat trailer and index: %w", err)
	}

	if len(idx.Variants) == 0 {
		return fmt.Errorf("fat binary index in %s contains no variants", fatBinaryPath)
	}

	dictBytes, err := readSharedDictionary(f, idx)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(stagingDir, dirPerms); err != nil {
		return fmt.Errorf("creating staging dir %s: %w", stagingDir, err)
	}

	for _, v := range idx.Variants {
		if err := extractSingleVariant(f, v, dictBytes, stagingDir); err != nil {
			return err
		}
	}

	return nil
}

func runSyft(scanDir, formatName string) ([]byte, error) {
	var syftFormat string
	switch formatName {
	case "spdx", formatSPDXJSON:
		syftFormat = formatSPDXJSON
	case "cyclonedx", formatCycloneDXJSON:
		syftFormat = formatCycloneDXJSON
	default:
		return nil, fmt.Errorf("unsupported SBOM format %q (expected spdx-json or cyclonedx-json)", formatName)
	}

	syftPath, err := exec.LookPath("syft")
	if err != nil {
		return nil, fmt.Errorf("syft executable not found in PATH: %w", err)
	}

	// #nosec G204 -- syftPath resolved via LookPath, formatName validated against allowlist
	cmd := exec.Command(syftPath, scanDir, "-o", syftFormat)
	cmd.Env = os.Environ()

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("syft failed with exit code %d: %s", exitErr.ExitCode(), string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("running syft: %w", err)
	}
	return out, nil
}

func sanitizeSPDXID(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('-')
		}
	}
	return sb.String()
}

func attributeSPDX(
	rawJSON []byte,
	facts *releasecheck.ArchiveFacts,
	inv *releasecheck.ArchiveInventory,
	version string,
) ([]byte, error) {
	var doc releasecheck.SPDXDocument
	if err := json.Unmarshal(rawJSON, &doc); err != nil {
		return nil, fmt.Errorf("parsing syft SPDX JSON: %w", err)
	}

	doc.SPDXVersion = "SPDX-2.3"
	doc.DataLicense = "CC0-1.0"
	doc.SPDXID = "SPDXRef-DOCUMENT"
	doc.Name = facts.ArchiveName
	doc.DocumentNamespace = fmt.Sprintf("https://github.com/EpicBlackWolfZ/microfat/releases/tag/v%s/%s",
		version, facts.ArchiveName)

	var cleanPackages []releasecheck.SPDXPackage
	existingPkgByName := make(map[string]releasecheck.SPDXPackage)
	for _, p := range doc.Packages {
		if strings.HasPrefix(p.Name, "/") || strings.Contains(p.Name, "microfat") || p.Name == "embedded_variants" {
			continue
		}
		cleanPackages = append(cleanPackages, p)
		existingPkgByName[p.Name] = p
	}

	for modPath, dep := range inv.AllDependencies {
		targetName := modPath
		targetVer := dep.Version
		if dep.ReplacePath != "" {
			targetName = dep.ReplacePath
			targetVer = dep.ReplaceVer
		}
		if _, exists := existingPkgByName[targetName]; !exists {
			pkg := releasecheck.SPDXPackage{
				SPDXID:           fmt.Sprintf("SPDXRef-Package-%s", sanitizeSPDXID(targetName)),
				Name:             targetName,
				VersionInfo:      targetVer,
				DownloadLocation: "NOASSERTION",
				FilesAnalyzed:    false,
			}
			cleanPackages = append(cleanPackages, pkg)
			existingPkgByName[targetName] = pkg
		}
	}

	archivePkgID := "SPDXRef-Archive"
	archivePkg := releasecheck.SPDXPackage{
		SPDXID:           archivePkgID,
		Name:             facts.ArchiveName,
		VersionInfo:      version,
		DownloadLocation: "NOASSERTION",
		FilesAnalyzed:    false,
		Checksums: []releasecheck.SPDXChecksum{
			{Algorithm: "SHA256", ChecksumValue: facts.ArchiveSHA256},
		},
		Supplier: "Organization: EpicBlackWolfZ",
		Comment:  fmt.Sprintf("release_archive;arch=%s", facts.TargetArch),
	}
	cleanPackages = append([]releasecheck.SPDXPackage{archivePkg}, cleanPackages...)

	for exeName, exe := range facts.Executables {
		exePkg := releasecheck.SPDXPackage{
			SPDXID:           fmt.Sprintf("SPDXRef-Binary-%s", exeName),
			Name:             exeName,
			VersionInfo:      version,
			DownloadLocation: "NOASSERTION",
			FilesAnalyzed:    false,
			Checksums: []releasecheck.SPDXChecksum{
				{Algorithm: "SHA256", ChecksumValue: exe.SHA256},
			},
		}
		cleanPackages = append(cleanPackages, exePkg)
	}

	tiers := make([]string, 0, len(facts.EmbeddedVariants))
	for tier := range facts.EmbeddedVariants {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)
	for _, tier := range tiers {
		vf := facts.EmbeddedVariants[tier]
		vPkg := releasecheck.SPDXPackage{
			SPDXID:           fmt.Sprintf("SPDXRef-Variant-%s", sanitizeSPDXID(tier)),
			Name:             "microfat-variant-" + tier,
			VersionInfo:      version,
			DownloadLocation: "NOASSERTION",
			FilesAnalyzed:    false,
			Checksums: []releasecheck.SPDXChecksum{
				{Algorithm: "SHA256", ChecksumValue: vf.SHA256},
			},
			Comment: fmt.Sprintf("variant_tier=%s;arch=%s", tier, facts.TargetArch),
		}
		cleanPackages = append(cleanPackages, vPkg)
	}
	doc.Packages = cleanPackages

	var relationships []releasecheck.SPDXRelationship
	relationships = append(relationships, releasecheck.SPDXRelationship{
		SPDXElementID:      doc.SPDXID,
		RelatedSPDXElement: archivePkgID,
		RelationshipType:   "DESCRIBES",
	})
	for exeName := range facts.Executables {
		relationships = append(relationships, releasecheck.SPDXRelationship{
			SPDXElementID:      archivePkgID,
			RelatedSPDXElement: fmt.Sprintf("SPDXRef-Binary-%s", exeName),
			RelationshipType:   "CONTAINS",
		})
	}
	for _, tier := range tiers {
		relationships = append(relationships, releasecheck.SPDXRelationship{
			SPDXElementID:      fmt.Sprintf("SPDXRef-Binary-%s", releasecheck.ReleaseProjectName),
			RelatedSPDXElement: fmt.Sprintf("SPDXRef-Variant-%s", sanitizeSPDXID(tier)),
			RelationshipType:   "CONTAINS",
		})
	}

	for binID, binInv := range inv.Binaries {
		var sourceID string
		if strings.HasPrefix(binID, "variant:") {
			tier := strings.TrimPrefix(binID, "variant:")
			sourceID = fmt.Sprintf("SPDXRef-Variant-%s", sanitizeSPDXID(tier))
		} else {
			sourceID = fmt.Sprintf("SPDXRef-Binary-%s", binID)
		}
		for modPath, dep := range binInv.Dependencies {
			targetName := modPath
			if dep.ReplacePath != "" {
				targetName = dep.ReplacePath
			}
			modPkg, ok := existingPkgByName[targetName]
			if !ok {
				continue
			}
			relationships = append(relationships, releasecheck.SPDXRelationship{
				SPDXElementID:      sourceID,
				RelatedSPDXElement: modPkg.SPDXID,
				RelationshipType:   "DEPENDS_ON",
			})
		}
	}
	doc.Relationships = relationships

	return json.MarshalIndent(doc, "", "  ")
}

func attributeCycloneDX(
	rawJSON []byte,
	facts *releasecheck.ArchiveFacts,
	inv *releasecheck.ArchiveInventory,
	version string,
) ([]byte, error) {
	var doc releasecheck.CDXDocument
	if err := json.Unmarshal(rawJSON, &doc); err != nil {
		return nil, fmt.Errorf("parsing syft CycloneDX JSON: %w", err)
	}

	doc.BOMFormat = "CycloneDX"
	doc.SpecVersion = "1.5"

	archiveRef := "archive-root"
	doc.Metadata.Component = &releasecheck.CDXComponent{
		BOMRef:  archiveRef,
		Type:    "file",
		Name:    facts.ArchiveName,
		Version: version,
		Hashes: []releasecheck.CDXHash{
			{Alg: hashAlgSHA256, Content: facts.ArchiveSHA256},
		},
		Properties: []releasecheck.CDXProperty{
			{Name: "microfat:target_arch", Value: facts.TargetArch},
		},
	}

	var cleanComponents []releasecheck.CDXComponent
	existingCompByName := make(map[string]releasecheck.CDXComponent)
	for _, c := range doc.Components {
		if strings.HasPrefix(c.Name, "/") || strings.Contains(c.Name, "microfat") || c.Name == "embedded_variants" {
			continue
		}
		cleanComponents = append(cleanComponents, c)
		existingCompByName[c.Name] = c
	}

	for modPath, dep := range inv.AllDependencies {
		targetName := modPath
		targetVer := dep.Version
		if dep.ReplacePath != "" {
			targetName = dep.ReplacePath
			targetVer = dep.ReplaceVer
		}
		if _, exists := existingCompByName[targetName]; !exists {
			comp := releasecheck.CDXComponent{
				BOMRef:  fmt.Sprintf("pkg:%s@%s", targetName, targetVer),
				Type:    "library",
				Name:    targetName,
				Version: targetVer,
			}
			cleanComponents = append(cleanComponents, comp)
			existingCompByName[targetName] = comp
		}
	}

	tiers := make([]string, 0, len(facts.EmbeddedVariants))
	for tier := range facts.EmbeddedVariants {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)

	var variantComponents []releasecheck.CDXComponent
	var variantRefs []string
	for _, tier := range tiers {
		vf := facts.EmbeddedVariants[tier]
		vRef := fmt.Sprintf("variant-%s", sanitizeSPDXID(tier))
		variantRefs = append(variantRefs, vRef)
		variantComponents = append(variantComponents, releasecheck.CDXComponent{
			BOMRef:  vRef,
			Type:    "application",
			Name:    "microfat-variant-" + tier,
			Version: version,
			Hashes: []releasecheck.CDXHash{
				{Alg: "SHA-256", Content: vf.SHA256},
			},
			Properties: []releasecheck.CDXProperty{
				{Name: "microfat:variant_tier", Value: tier},
				{Name: "microfat:target_arch", Value: facts.TargetArch},
			},
		})
	}

	microfatComp := releasecheck.CDXComponent{
		BOMRef:  "bin-microfat",
		Type:    "application",
		Name:    releasecheck.ReleaseProjectName,
		Version: version,
		Hashes: []releasecheck.CDXHash{
			{Alg: "SHA-256", Content: facts.Executables[releasecheck.ReleaseProjectName].SHA256},
		},
		Components: variantComponents,
	}
	cleanComponents = append(cleanComponents, microfatComp)

	stubComp := releasecheck.CDXComponent{
		BOMRef:  "bin-stub",
		Type:    "application",
		Name:    releasecheck.ReleaseFullStub,
		Version: version,
		Hashes: []releasecheck.CDXHash{
			{Alg: "SHA-256", Content: facts.Executables[releasecheck.ReleaseFullStub].SHA256},
		},
		Properties: []releasecheck.CDXProperty{
			{Name: "microfat:stub_profile", Value: "full"},
			{Name: "microfat:target_arch", Value: facts.TargetArch},
		},
	}
	cleanComponents = append(cleanComponents, stubComp)

	minStubComp := releasecheck.CDXComponent{
		BOMRef:  "bin-min-stub",
		Type:    "application",
		Name:    releasecheck.ReleaseMinStub,
		Version: version,
		Hashes: []releasecheck.CDXHash{
			{Alg: "SHA-256", Content: facts.Executables[releasecheck.ReleaseMinStub].SHA256},
		},
		Properties: []releasecheck.CDXProperty{
			{Name: "microfat:stub_profile", Value: "minimal"},
			{Name: "microfat:target_arch", Value: facts.TargetArch},
		},
	}
	cleanComponents = append(cleanComponents, minStubComp)
	doc.Components = cleanComponents

	var dependencies []releasecheck.CDXDependency
	dependencies = append(dependencies, releasecheck.CDXDependency{
		Ref:       archiveRef,
		DependsOn: []string{"bin-microfat", "bin-stub", "bin-min-stub"},
	})
	dependencies = append(dependencies, releasecheck.CDXDependency{
		Ref:       "bin-microfat",
		DependsOn: variantRefs,
	})

	for binID, binInv := range inv.Binaries {
		var sourceRef string
		switch {
		case strings.HasPrefix(binID, "variant:"):
			tier := strings.TrimPrefix(binID, "variant:")
			sourceRef = fmt.Sprintf("variant-%s", sanitizeSPDXID(tier))
		case binID == releasecheck.ReleaseProjectName:
			sourceRef = "bin-microfat"
		case binID == releasecheck.ReleaseFullStub:
			sourceRef = "bin-stub"
		case binID == releasecheck.ReleaseMinStub:
			sourceRef = "bin-min-stub"
		}

		var depRefs []string
		for modPath, dep := range binInv.Dependencies {
			targetName := modPath
			if dep.ReplacePath != "" {
				targetName = dep.ReplacePath
			}
			if c, ok := existingCompByName[targetName]; ok {
				depRefs = append(depRefs, c.BOMRef)
			}
		}
		if sourceRef != "" && len(depRefs) > 0 {
			dependencies = append(dependencies, releasecheck.CDXDependency{
				Ref:       sourceRef,
				DependsOn: depRefs,
			})
		}
	}
	doc.Dependencies = dependencies

	return json.MarshalIndent(doc, "", "  ")
}

func attributeSBOM(
	rawJSON []byte,
	formatName string,
	facts *releasecheck.ArchiveFacts,
	inv *releasecheck.ArchiveInventory,
	version string,
) ([]byte, error) {
	switch formatName {
	case "spdx", "spdx-json":
		return attributeSPDX(rawJSON, facts, inv, version)
	case "cyclonedx", "cyclonedx-json":
		return attributeCycloneDX(rawJSON, facts, inv, version)
	default:
		return nil, fmt.Errorf("unsupported format %s", formatName)
	}
}

func writeAtomic(targetPath string, data []byte) error {
	dir := filepath.Dir(targetPath)
	// #nosec G703 -- directory created from output path
	if err := os.MkdirAll(dir, dirPerms); err != nil {
		return fmt.Errorf("creating directory for %s: %w", targetPath, err)
	}

	tmpFile, err := os.CreateTemp(dir, ".sbom-tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		// #nosec G703 -- cleaning up temporary file
		_ = os.Remove(tmpName)
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing to temp file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("syncing temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	// #nosec G703 -- setting permissions on newly created temporary file
	if err := os.Chmod(tmpName, filePerms); err != nil {
		return fmt.Errorf("chmodding temp file: %w", err)
	}

	// #nosec G703 -- renaming temporary file to target path
	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tmpName, targetPath, err)
	}

	return nil
}

// Generate processes an archive tarball, extracts embedded variants, invokes syft, and writes an attributed SBOM.
func Generate(archivePath, outputPath, formatName string) error {
	targetArch := releasecheck.ArchAMD64
	if strings.Contains(archivePath, "arm64") {
		targetArch = releasecheck.ArchARM64
	}

	version, err := releasecheck.DeriveVersion(filepath.Dir(archivePath), "")
	if err != nil {
		parts := strings.Split(filepath.Base(archivePath), "_")
		if len(parts) >= 2 && parts[1] != "" {
			version = strings.TrimPrefix(parts[1], "v")
		} else {
			version = "0.0.0-dev"
		}
	}

	contract, err := releasecheck.NewReleaseContract(version)
	if err != nil {
		return fmt.Errorf("creating release contract: %w", err)
	}

	facts, err := releasecheck.ValidateArchive(archivePath, targetArch, contract)
	if err != nil {
		return fmt.Errorf("validating archive %s: %w", archivePath, err)
	}
	defer func() {
		// #nosec G703 -- cleaning up temporary staging directory
		_ = os.RemoveAll(facts.StagingDir)
	}()

	stagingVariantsDir := filepath.Join(facts.StagingDir, "embedded_variants")
	if err := os.MkdirAll(stagingVariantsDir, dirPerms); err != nil {
		return fmt.Errorf("creating variants staging dir: %w", err)
	}
	for tier, vf := range facts.EmbeddedVariants {
		vPath := filepath.Join(stagingVariantsDir, "microfat-variant-"+tier)
		if err := os.WriteFile(vPath, vf.Data, execPerms); err != nil {
			return fmt.Errorf("staging variant %s: %w", tier, err)
		}
	}

	inv, err := releasecheck.ExtractArchiveInventory(facts)
	if err != nil {
		return fmt.Errorf("extracting inventory from archive: %w", err)
	}

	rawSBOM, err := runSyft(facts.StagingDir, formatName)
	if err != nil {
		return fmt.Errorf("generating SBOM with syft: %w", err)
	}

	attributed, err := attributeSBOM(rawSBOM, formatName, facts, inv, contract.Version)
	if err != nil {
		return fmt.Errorf("attributing SBOM: %w", err)
	}

	switch formatName {
	case "spdx", "spdx-json":
		if err := releasecheck.ValidateSPDXBytes(attributed, facts, inv); err != nil {
			return fmt.Errorf("semantic validation of generated SPDX failed: %w", err)
		}
	case "cyclonedx", "cyclonedx-json":
		if err := releasecheck.ValidateCycloneDXBytes(attributed, facts, inv); err != nil {
			return fmt.Errorf("semantic validation of generated CycloneDX failed: %w", err)
		}
	default:
		return fmt.Errorf("unsupported format %s", formatName)
	}

	if err := writeAtomic(outputPath, attributed); err != nil {
		return fmt.Errorf("writing SBOM output to %s: %w", outputPath, err)
	}

	return nil
}

func runMain(args []string) int {
	archivePath, outputPath, formatName, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if err := Generate(archivePath, outputPath, formatName); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

var exitFunc = os.Exit

func main() {
	exitFunc(runMain(os.Args[1:]))
}
