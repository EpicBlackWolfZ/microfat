package microarch_test

import (
	"testing"

	"github.com/EpicBlackWolfZ/microfat/internal/microarch"
)

func FuzzVariantLevelParsing(f *testing.F) {
	f.Add("v1")
	f.Add("v2")
	f.Add("v3")
	f.Add("v4")
	f.Add("v8.0")
	f.Add("v8.2")
	f.Add("v9.0")
	f.Add("v9.2")
	f.Add("amd64_v3")
	f.Add("arm64-v8.2")
	f.Add("x86_64_v2")
	f.Add("darwin_arm64_v8.0")
	f.Add("INVALID")
	f.Add("")

	f.Fuzz(func(t *testing.T, level string) {
		norm := microarch.Normalize(level)
		if norm == "" {
			t.Fatalf("Normalize(%q) produced empty string", level)
		}
		_ = microarch.Rank(microarch.ArchAMD64, norm)
		_ = microarch.Rank(microarch.ArchARM64, norm)
		_ = microarch.IsSupported(level)
		_ = microarch.Compare(microarch.ArchAMD64, level, "v1")
		_ = microarch.Compare(microarch.ArchARM64, level, "v8.0")
	})
}
