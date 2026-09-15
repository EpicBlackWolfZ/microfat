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
	dirPerms        = 0o755
	filePerms       = 0o644
	execPerms       = 0o755
	keyValueParts   = 2
	minArchiveParts = 2

	formatSPDXJSON      = "spdx-json"
	formatCycloneDXJSON = "cyclonedx-json"
	hashAlgSHA256       = "SHA-256"
	spdxNoAssertion     = "NOASSERTION"
	cdxTypeApplication  = "application"
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

func isOmittedBinaryPackage(name string) bool {
	clean := strings.TrimPrefix(name, "archive/")
	clean = strings.TrimPrefix(clean, "derived/variants/")
	clean = strings.TrimPrefix(clean, "embedded_variants/")
	return strings.HasPrefix(name, "/") || clean == "microfat" || clean == "microfat-stub" ||
		clean == "microfat-stub-minimal" || strings.HasPrefix(clean, "microfat-variant-") ||
		name == "archive" || name == "derived" || name == "embedded_variants"
}

func buildSPDXPackages(
	docPackages []releasecheck.SPDXPackage,
	facts *releasecheck.ArchiveFacts,
	inv *releasecheck.ArchiveInventory,
	version string,
) ([]releasecheck.SPDXPackage, map[string]releasecheck.SPDXPackage) {
	var cleanPackages []releasecheck.SPDXPackage
	existingPkgByName := make(map[string]releasecheck.SPDXPackage)
	for _, p := range docPackages {
		if isOmittedBinaryPackage(p.Name) {
			continue
		}
		cleanPackages = append(cleanPackages, p)
		existingPkgByName[p.Name] = p
	}

	var mainModule string
	for _, b := range inv.Binaries {
		if b.MainModule != "" {
			mainModule = b.MainModule
			break
		}
	}
	if mainModule != "" {
		if _, exists := existingPkgByName[mainModule]; !exists {
			pkg := releasecheck.SPDXPackage{
				SPDXID:           fmt.Sprintf("SPDXRef-Package-%s", sanitizeSPDXID(mainModule)),
				Name:             mainModule,
				VersionInfo:      version,
				DownloadLocation: spdxNoAssertion,
				FilesAnalyzed:    false,
			}
			cleanPackages = append(cleanPackages, pkg)
			existingPkgByName[mainModule] = pkg
		}
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
				DownloadLocation: spdxNoAssertion,
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
		DownloadLocation: spdxNoAssertion,
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
			DownloadLocation: spdxNoAssertion,
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
			DownloadLocation: spdxNoAssertion,
			FilesAnalyzed:    false,
			Checksums: []releasecheck.SPDXChecksum{
				{Algorithm: "SHA256", ChecksumValue: vf.SHA256},
			},
			Comment: fmt.Sprintf("variant_tier=%s;arch=%s", tier, facts.TargetArch),
		}
		cleanPackages = append(cleanPackages, vPkg)
	}

	return cleanPackages, existingPkgByName
}

func buildSPDXRelationships(
	docID, archivePkgID string,
	facts *releasecheck.ArchiveFacts,
	inv *releasecheck.ArchiveInventory,
	existingPkgByName map[string]releasecheck.SPDXPackage,
) []releasecheck.SPDXRelationship {
	var relationships []releasecheck.SPDXRelationship
	relationships = append(relationships, releasecheck.SPDXRelationship{
		SPDXElementID:      docID,
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
	tiers := make([]string, 0, len(facts.EmbeddedVariants))
	for tier := range facts.EmbeddedVariants {
		tiers = append(tiers, tier)
	}
	sort.Strings(tiers)
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
	return relationships
}

func attributeSPDX(
	rawJSON []byte,
	facts *releasecheck.ArchiveFacts,
	inv *releasecheck.ArchiveInventory,
	version string,
) ([]byte, error) {
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &rawMap); err != nil {
		return nil, fmt.Errorf("parsing syft SPDX JSON: %w", err)
	}

	var creationInfo releasecheck.SPDXCreationInfo
	rawCI, ok := rawMap["creationInfo"]
	if !ok {
		return nil, fmt.Errorf("SPDX raw JSON missing required 'creationInfo'")
	}
	if err := json.Unmarshal(rawCI, &creationInfo); err != nil {
		return nil, fmt.Errorf("parsing creationInfo: %w", err)
	}
	if creationInfo.Created == "" {
		return nil, fmt.Errorf("SPDX raw JSON missing required 'creationInfo.created'")
	}
	if len(creationInfo.Creators) == 0 {
		return nil, fmt.Errorf("SPDX raw JSON missing required 'creationInfo.creators'")
	}

	var originalPackages []releasecheck.SPDXPackage
	if rawPkgs, ok := rawMap["packages"]; ok {
		if err := json.Unmarshal(rawPkgs, &originalPackages); err != nil {
			return nil, fmt.Errorf("unmarshalling packages: %w", err)
		}
	}

	docID := "SPDXRef-DOCUMENT"
	archivePkgID := "SPDXRef-Archive"
	cleanPackages, existingPkgByName := buildSPDXPackages(originalPackages, facts, inv, version)
	relationships := buildSPDXRelationships(docID, archivePkgID, facts, inv, existingPkgByName)

	spdxVer, _ := json.Marshal("SPDX-2.3")
	rawMap["spdxVersion"] = spdxVer
	dataLic, _ := json.Marshal("CC0-1.0")
	rawMap["dataLicense"] = dataLic
	spdxID, _ := json.Marshal(docID)
	rawMap["SPDXID"] = spdxID
	docName, _ := json.Marshal(facts.ArchiveName)
	rawMap["name"] = docName
	docNS, _ := json.Marshal(fmt.Sprintf("https://github.com/EpicBlackWolfZ/microfat/releases/tag/v%s/%s",
		version, facts.ArchiveName))
	rawMap["documentNamespace"] = docNS
	pkgsBytes, _ := json.Marshal(cleanPackages)
	rawMap["packages"] = pkgsBytes
	relsBytes, _ := json.Marshal(relationships)
	rawMap["relationships"] = relsBytes

	return json.MarshalIndent(rawMap, "", "  ")
}

func isOmittedBinaryComponent(name string) bool {
	clean := strings.TrimPrefix(name, "archive/")
	clean = strings.TrimPrefix(clean, "derived/variants/")
	clean = strings.TrimPrefix(clean, "embedded_variants/")
	return strings.HasPrefix(name, "/") || clean == "microfat" || clean == "microfat-stub" ||
		clean == "microfat-stub-minimal" || strings.HasPrefix(clean, "microfat-variant-") ||
		name == "archive" || name == "derived" || name == "embedded_variants"
}

func buildCDXComponents(
	docComponents []releasecheck.CDXComponent,
	facts *releasecheck.ArchiveFacts,
	inv *releasecheck.ArchiveInventory,
	version string,
) ([]releasecheck.CDXComponent, []releasecheck.CDXComponent, map[string]releasecheck.CDXComponent) {
	var cleanComponents []releasecheck.CDXComponent
	existingCompByName := make(map[string]releasecheck.CDXComponent)
	for _, c := range docComponents {
		if isOmittedBinaryComponent(c.Name) {
			continue
		}
		cleanComponents = append(cleanComponents, c)
		existingCompByName[c.Name] = c
	}

	var mainModule string
	for _, b := range inv.Binaries {
		if b.MainModule != "" {
			mainModule = b.MainModule
			break
		}
	}
	if mainModule != "" {
		if _, exists := existingCompByName[mainModule]; !exists {
			comp := releasecheck.CDXComponent{
				BOMRef:  fmt.Sprintf("pkg:%s@%s", mainModule, version),
				Type:    cdxTypeApplication,
				Name:    mainModule,
				Version: version,
			}
			cleanComponents = append(cleanComponents, comp)
			existingCompByName[mainModule] = comp
		}
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
	for _, tier := range tiers {
		vf := facts.EmbeddedVariants[tier]
		vRef := fmt.Sprintf("variant-%s", sanitizeSPDXID(tier))
		vComp := releasecheck.CDXComponent{
			BOMRef:  vRef,
			Type:    cdxTypeApplication,
			Name:    "microfat-variant-" + tier,
			Version: version,
			Hashes: []releasecheck.CDXHash{
				{Alg: hashAlgSHA256, Content: vf.SHA256},
			},
			Properties: []releasecheck.CDXProperty{
				{Name: "microfat:variant_tier", Value: tier},
				{Name: "microfat:target_arch", Value: facts.TargetArch},
			},
		}
		variantComponents = append(variantComponents, vComp)
		existingCompByName[vComp.Name] = vComp
	}

	microfatComp := releasecheck.CDXComponent{
		BOMRef:  "bin-microfat",
		Type:    cdxTypeApplication,
		Name:    releasecheck.ReleaseProjectName,
		Version: version,
		Hashes: []releasecheck.CDXHash{
			{Alg: hashAlgSHA256, Content: facts.Executables[releasecheck.ReleaseProjectName].SHA256},
		},
		Components: variantComponents,
	}

	stubComp := releasecheck.CDXComponent{
		BOMRef:  "bin-stub",
		Type:    cdxTypeApplication,
		Name:    releasecheck.ReleaseFullStub,
		Version: version,
		Hashes: []releasecheck.CDXHash{
			{Alg: hashAlgSHA256, Content: facts.Executables[releasecheck.ReleaseFullStub].SHA256},
		},
		Properties: []releasecheck.CDXProperty{
			{Name: "microfat:stub_profile", Value: "full"},
			{Name: "microfat:target_arch", Value: facts.TargetArch},
		},
	}

	minStubComp := releasecheck.CDXComponent{
		BOMRef:  "bin-min-stub",
		Type:    cdxTypeApplication,
		Name:    releasecheck.ReleaseMinStub,
		Version: version,
		Hashes: []releasecheck.CDXHash{
			{Alg: hashAlgSHA256, Content: facts.Executables[releasecheck.ReleaseMinStub].SHA256},
		},
		Properties: []releasecheck.CDXProperty{
			{Name: "microfat:stub_profile", Value: "minimal"},
			{Name: "microfat:target_arch", Value: facts.TargetArch},
		},
	}

	rootExecComponents := []releasecheck.CDXComponent{microfatComp, stubComp, minStubComp}
	existingCompByName[microfatComp.Name] = microfatComp
	existingCompByName[stubComp.Name] = stubComp
	existingCompByName[minStubComp.Name] = minStubComp

	return cleanComponents, rootExecComponents, existingCompByName
}

func buildCDXDependencies(
	inv *releasecheck.ArchiveInventory,
	existingCompByName map[string]releasecheck.CDXComponent,
) []releasecheck.CDXDependency {
	var dependencies []releasecheck.CDXDependency

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
	return dependencies
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

	cleanComponents, rootExecComponents, existingCompByName := buildCDXComponents(doc.Components, facts, inv, version)

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
		Components: rootExecComponents,
	}

	doc.Components = cleanComponents
	doc.Dependencies = buildCDXDependencies(inv, existingCompByName)

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

func resolveArchiveVersion(archivePath string) string {
	version, err := releasecheck.DeriveVersion(filepath.Dir(archivePath), "")
	if err == nil {
		return version
	}
	parts := strings.Split(filepath.Base(archivePath), "_")
	if len(parts) >= minArchiveParts && parts[1] != "" {
		return strings.TrimPrefix(parts[1], "v")
	}
	return "0.0.0-dev"
}

func stageExtractedVariants(facts *releasecheck.ArchiveFacts) error {
	stagingVariantsDir := filepath.Join(facts.StagingDir, "derived", "variants")
	// #nosec G703 -- stagingVariantsDir within validated temporary staging dir
	if err := os.MkdirAll(stagingVariantsDir, dirPerms); err != nil {
		return fmt.Errorf("creating variants staging dir: %w", err)
	}
	for tier, vf := range facts.EmbeddedVariants {
		vPath := filepath.Join(stagingVariantsDir, "microfat-variant-"+tier)
		// #nosec G703 -- vPath within validated temporary staging dir
		if err := os.WriteFile(vPath, vf.Data, execPerms); err != nil {
			return fmt.Errorf("staging variant %s: %w", tier, err)
		}
	}
	return nil
}

func validateAttributedSBOM(
	attributed []byte,
	formatName string,
	facts *releasecheck.ArchiveFacts,
	inv *releasecheck.ArchiveInventory,
) error {
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
	return nil
}

// Generate processes an archive tarball, extracts embedded variants, invokes syft, and writes an attributed SBOM.
func Generate(archivePath, outputPath, formatName string) error {
	targetArch := releasecheck.ArchAMD64
	if strings.Contains(archivePath, "arm64") {
		targetArch = releasecheck.ArchARM64
	}

	version := resolveArchiveVersion(archivePath)
	contract, err := releasecheck.NewReleaseContract(version)
	if err != nil {
		return fmt.Errorf("creating release contract: %w", err)
	}

	facts, err := releasecheck.ValidateArchive(archivePath, targetArch, contract)
	if err != nil {
		return fmt.Errorf("validating archive %s: %w", archivePath, err)
	}
	defer func() {
		_ = facts.Cleanup()
	}()

	if err := stageExtractedVariants(facts); err != nil {
		return err
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

	if err := validateAttributedSBOM(attributed, formatName, facts, inv); err != nil {
		return err
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
