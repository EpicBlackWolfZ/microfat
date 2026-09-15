package releasecheck

// Export internal functions for whitebox testing in releasecheck_test.
var (
	ValidateRootExecutableISA  = validateRootExecutableISA
	ValidateEmbeddedVariantISA = validateEmbeddedVariantISA
	GetBuildSetting            = getBuildSetting
)
