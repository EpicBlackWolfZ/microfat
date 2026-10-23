package main

import (
	"github.com/EpicBlackWolfZ/microfat/internal/format"
)

const nativeStudyModes = 4

// Revalidate recorded correctness evidence independently of the campaign exit
// code. A truncated post-timing native phase must never become a useful result.
func completeStudyNative(o observation, m corpusManifest) bool {
	if len(o.Launches) != nativeStudyModes {
		return false
	}
	reference := o.Launches[0]
	if reference.RequestedMode != nativeLaunchMode || reference.ExecMode != nativeLaunchMode ||
		!recordedNativeReadinessValid(o.Spec.Family, reference.Readiness) ||
		validateNativeFixtureOutput(o.Spec.Family, reference.Stdout) != nil {
		return false
	}
	var source *corpusVariant
	for i := range m.Variants {
		v := &m.Variants[i]
		if v.Family == o.Spec.Family && v.Level == reference.ExpectedVariant {
			source = v
			break
		}
	}
	if source == nil {
		return false
	}
	seen := map[string]bool{}
	for _, launch := range o.Launches {
		if seen[launch.RequestedMode] || !recordedNativeReadinessValid(o.Spec.Family, launch.Readiness) ||
			launch.Readiness.DatabaseDir != reference.Readiness.DatabaseDir ||
			!nativeRecordIdentity(launch, reference, *source, o.Request.Output, corpusArchitecture(m)) {
			return false
		}
		seen[launch.RequestedMode] = true
		if launch.RequestedMode == nativeLaunchMode {
			continue
		}
		if launch.RequestedMode != format.ExecModeMemfd && launch.RequestedMode != format.ExecModeCache &&
			launch.RequestedMode != format.ExecModeAuto {
			return false
		}
		parsed := launch
		if validateNativeDispatch(&parsed, reference.Stdout) != nil || parsed.ExecMode != launch.ExecMode ||
			launch.Prewarmed != (launch.RequestedMode == format.ExecModeAuto) {
			return false
		}
	}
	return seen[nativeLaunchMode] && seen[format.ExecModeMemfd] && seen[format.ExecModeCache] && seen[format.ExecModeAuto]
}

func nativeRecordIdentity(launch, reference launchObservation, source corpusVariant, artifact, arch string) bool {
	return launch.MeasurementKind == nativeLaunchKind && launch.Error == "" && launch.OutputMatched &&
		launch.ExpectedArch == arch && launch.ExpectedVariant == source.Level && launch.SelectedVariant == source.Level &&
		launch.ExpectedSHA256 == source.SHA256 && launch.SelectedSHA256 == source.SHA256 &&
		launch.ExpectedSizeBytes == source.Bytes && launch.SelectedSizeBytes == source.Bytes &&
		launch.NativePayloadPath == source.Path && launch.ArtifactPath == artifact && launch.Stdout == reference.Stdout
}
