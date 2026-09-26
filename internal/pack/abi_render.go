package pack

import (
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"
)

const (
	minLevelColumnWidth = 6
	maxDisplayDeps      = 8
	maxDisplayVers      = 8
)

// reportEmitter performs synchronized sizing and streaming output for ArtifactABIReport presentations.
type reportEmitter struct {
	w       io.Writer
	written uint64
	err     error
}

func (e *reportEmitter) writeLiteral(s string) {
	if e.err != nil {
		return
	}
	n := uint64(len(s))
	if e.written > math.MaxUint64-n {
		e.err = fmt.Errorf("%w: report presentation size overflow", ErrABIResourceLimit)
		return
	}
	e.written += n
	if e.w != nil {
		if _, err := io.WriteString(e.w, s); err != nil {
			e.err = err
		}
	}
}

func (e *reportEmitter) writeEscaped(s string) {
	if e.err != nil {
		return
	}
	escLen, _ := EscapedMetadataLen(s)
	if escLen < 0 {
		return
	}
	n := uint64(escLen)
	if e.written > math.MaxUint64-n {
		e.err = fmt.Errorf("%w: report presentation size overflow", ErrABIResourceLimit)
		return
	}
	e.written += n
	if e.w != nil {
		if err := writeEscapedToWriter(e.w, s); err != nil {
			e.err = err
		}
	}
}

func writeEscapedToWriter(w io.Writer, s string) error {
	const maxDisplayLen = 4096
	truncated := false
	if len(s) > maxDisplayLen {
		s = s[:maxDisplayLen]
		truncated = true
	}
	var tmp [4]byte
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			esc := fmt.Sprintf("\\x%02x", s[i])
			if _, err := io.WriteString(w, esc); err != nil {
				return err
			}
			i++
			continue
		}
		if r < 32 || r == 127 || (r >= 0x80 && r <= 0x9f) {
			esc := fmt.Sprintf("\\x%02x", r)
			if _, err := io.WriteString(w, esc); err != nil {
				return err
			}
		} else {
			n := utf8.EncodeRune(tmp[:], r)
			if _, err := w.Write(tmp[:n]); err != nil {
				return err
			}
		}
		i += size
	}
	if truncated {
		if _, err := io.WriteString(w, "...[truncated]"); err != nil {
			return err
		}
	}
	return nil
}

func emitVariantDependencies(e *reportEmitter, deps []string) {
	e.writeLiteral(" | deps: ")
	if len(deps) == 0 {
		e.writeLiteral("none")
		return
	}
	n := min(len(deps), maxDisplayDeps)
	for i := 0; i < n; i++ {
		if i > 0 {
			e.writeLiteral(", ")
		}
		e.writeEscaped(deps[i])
	}
	if len(deps) > maxDisplayDeps {
		e.writeLiteral(fmt.Sprintf(" ... (+%d more)", len(deps)-maxDisplayDeps))
	}
}

func emitVariantVersions(e *reportEmitter, vers []VersionRequirement) {
	e.writeLiteral(" | versions: ")
	if len(vers) == 0 {
		e.writeLiteral("none")
		return
	}
	n := min(len(vers), maxDisplayVers)
	for i := 0; i < n; i++ {
		if i > 0 {
			e.writeLiteral(", ")
		}
		vr := vers[i]
		e.writeEscaped(vr.Library)
		e.writeLiteral(" (")
		e.writeEscaped(vr.Version)
		if vr.Flags != 0 {
			e.writeLiteral(fmt.Sprintf(" [flags=0x%04x]", vr.Flags))
		}
		e.writeLiteral(")")
	}
	if len(vers) > maxDisplayVers {
		e.writeLiteral(fmt.Sprintf(" ... (+%d more)", len(vers)-maxDisplayVers))
	}
}

func emitVariantLine(e *reportEmitter, v *VariantABIReport) {
	if v == nil {
		return
	}
	e.writeLiteral("  • ")

	// Level left-padded to at least minLevelColumnWidth columns
	escLevelLen, _ := EscapedMetadataLen(v.Level)
	e.writeEscaped(v.Level)
	if escLevelLen < minLevelColumnWidth {
		e.writeLiteral(strings.Repeat(" ", minLevelColumnWidth-escLevelLen))
	}

	e.writeLiteral(" [")
	e.writeLiteral(string(v.Linkage))
	e.writeLiteral("] -> interpreter: ")

	if v.HasInterpreter {
		e.writeEscaped(v.Interpreter)
	} else {
		e.writeLiteral("none")
	}

	emitVariantDependencies(e, v.Dependencies)
	emitVariantVersions(e, v.VersionRequirements)

	e.writeLiteral(" (")
	e.writeLiteral(string(v.Completeness))
	e.writeLiteral(")\n")
}

func emitReportNotes(e *reportEmitter, report *ArtifactABIReport) {
	if report.Overridden {
		e.writeLiteral("  [!] Note: declared ABI differences were explicitly allowed via --allow-mixed-abi\n")
	}

	if report.Status == ComparisonUnknown {
		hasVersionIncomplete := false
		hasAmbiguousLinkage := false
		for _, v := range report.Variants {
			if v == nil {
				continue
			}
			if v.Completeness == MetadataPartial || v.Completeness == MetadataUnsupported {
				hasVersionIncomplete = true
			}
			if v.Linkage == LinkageAmbiguous {
				hasAmbiguousLinkage = true
			}
		}
		if hasVersionIncomplete {
			e.writeLiteral("  [!] Note: symbol version metadata is partial or unsupported; complete compatibility could not be verified\n")
		}
		if hasAmbiguousLinkage {
			e.writeLiteral("  [!] Note: linkage is ambiguous (payload declares dynamic dependencies but has no PT_INTERP); " +
				"complete compatibility could not be verified\n")
		}
		if !hasVersionIncomplete && !hasAmbiguousLinkage {
			e.writeLiteral("  [!] Note: symbol version metadata is partial or unsupported; complete compatibility could not be verified\n")
		}
	}

	if len(report.DeploymentDisclaimer) > 0 {
		e.writeLiteral("  [i] ")
		e.writeLiteral(report.DeploymentDisclaimer)
		e.writeLiteral("\n")
	}
}

func emitReport(e *reportEmitter, report *ArtifactABIReport) {
	if report == nil || len(report.Variants) == 0 {
		return
	}
	if report.Status == ComparisonSkipped {
		e.writeLiteral("\nDeclared ABI Requirements: skipped (--skip-elf-validation)\n")
		return
	}

	e.writeLiteral("\nDeclared ABI Requirements:\n")
	for _, v := range report.Variants {
		emitVariantLine(e, v)
	}
	emitReportNotes(e, report)
}

// MeasureABIReportPresentation computes the exact presentation byte count of report.
func MeasureABIReportPresentation(report *ArtifactABIReport) (uint64, error) {
	e := &reportEmitter{}
	emitReport(e, report)
	if e.err != nil {
		return 0, e.err
	}
	return e.written, nil
}

// RenderABIReport renders the formatted report to w using the shared canonical format.
func RenderABIReport(w io.Writer, report *ArtifactABIReport) error {
	if report == nil || len(report.Variants) == 0 {
		return nil
	}
	if w == nil {
		w = io.Discard
	}
	e := &reportEmitter{w: w}
	emitReport(e, report)
	return e.err
}
