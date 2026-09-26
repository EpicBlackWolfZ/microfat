package pack

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestR1_PresentationAndErrorAccounting(t *testing.T) {
	t.Parallel()

	t.Run("exact_presentation_match_across_diverse_encodings", func(t *testing.T) {
		t.Parallel()

		testCases := []struct {
			name   string
			report *ArtifactABIReport
		}{
			{
				name:   "nil_report",
				report: nil,
			},
			{
				name:   "empty_variants",
				report: &ArtifactABIReport{Variants: []*VariantABIReport{}},
			},
			{
				name: "skipped_validation",
				report: &ArtifactABIReport{
					Status:   ComparisonSkipped,
					Variants: []*VariantABIReport{{Level: "v1", Completeness: MetadataSkipped}},
				},
			},
			{
				name: "utf8_and_multibyte_runes",
				report: &ArtifactABIReport{
					Status:     ComparisonConsistent,
					Consistent: true,
					Variants: []*VariantABIReport{
						{
							Level:          "v1",
							Linkage:        LinkageDynamic,
							HasInterpreter: true,
							Interpreter:    "/lib/ld-linux-世界-🚀.so.2",
							Dependencies:   []string{"lib你好.so.1", "lib€uro.so.2"},
							VersionRequirements: []VersionRequirement{
								{Library: "lib你好.so.1", Version: "VER_1.0_世界"},
							},
							Completeness: MetadataComplete,
						},
					},
					DeploymentDisclaimer: "Deployment note with unicode: ★★★",
				},
			},
			{
				name: "c1_control_characters",
				report: &ArtifactABIReport{
					Status:     ComparisonConsistent,
					Consistent: true,
					Variants: []*VariantABIReport{
						{
							Level:          "arm64_v8.0",
							Linkage:        LinkageDynamic,
							HasInterpreter: true,
							Interpreter:    "/lib/ld-\x80\x85\x9f.so",
							Dependencies:   []string{"lib\x88\x90.so"},
							VersionRequirements: []VersionRequirement{
								{Library: "lib\x88\x90.so", Version: "V\x92", Flags: 0x0002},
							},
							Completeness: MetadataComplete,
						},
					},
				},
			},
			{
				name: "split_multibyte_and_invalid_utf8",
				report: &ArtifactABIReport{
					Status:     ComparisonConsistent,
					Consistent: true,
					Variants: []*VariantABIReport{
						{
							Level:          "v2",
							Linkage:        LinkageDynamic,
							HasInterpreter: true,
							Interpreter:    "/lib/ld-\xff\xfe.so",
							Dependencies:   []string{"lib\xe2\x28.so"},
							VersionRequirements: []VersionRequirement{
								{Library: "lib\xe2\x28.so", Version: "V\x80"},
							},
							Completeness: MetadataComplete,
						},
					},
				},
			},
			{
				name: "level_padding_variations",
				report: &ArtifactABIReport{
					Status:     ComparisonConsistent,
					Consistent: true,
					Variants: []*VariantABIReport{
						{Level: "v1", Linkage: LinkageStatic, Completeness: MetadataAbsent},
						{Level: "v3", Linkage: LinkageStatic, Completeness: MetadataAbsent},
						{Level: "arm64_v9.2", Linkage: LinkageStatic, Completeness: MetadataAbsent},
					},
				},
			},
			{
				name: "overridden_notes",
				report: &ArtifactABIReport{
					Status:     ComparisonInconsistent,
					Overridden: true,
					Variants: []*VariantABIReport{
						{Level: "v1", Linkage: LinkageStatic, Completeness: MetadataAbsent},
						{Level: "v2", Linkage: LinkageDynamic, Completeness: MetadataComplete},
					},
				},
			},
			{
				name: "unknown_version_incomplete_note",
				report: &ArtifactABIReport{
					Status: ComparisonUnknown,
					Variants: []*VariantABIReport{
						{Level: "v1", Linkage: LinkageDynamic, Completeness: MetadataPartial},
					},
				},
			},
			{
				name: "unknown_ambiguous_linkage_note",
				report: &ArtifactABIReport{
					Status: ComparisonUnknown,
					Variants: []*VariantABIReport{
						{Level: "v1", Linkage: LinkageAmbiguous, Completeness: MetadataComplete},
					},
				},
			},
			{
				name: "unknown_both_notes",
				report: &ArtifactABIReport{
					Status: ComparisonUnknown,
					Variants: []*VariantABIReport{
						{Level: "v1", Linkage: LinkageAmbiguous, Completeness: MetadataPartial},
					},
				},
			},
			{
				name: "unknown_fallback_note",
				report: &ArtifactABIReport{
					Status: ComparisonUnknown,
					Variants: []*VariantABIReport{
						{Level: "v1", Linkage: LinkageDynamic, Completeness: MetadataComplete},
					},
				},
			},
			{
				name: "truncated_dependencies_and_versions",
				report: &ArtifactABIReport{
					Status:     ComparisonConsistent,
					Consistent: true,
					Variants: []*VariantABIReport{
						{
							Level:          "v1",
							Linkage:        LinkageDynamic,
							HasInterpreter: true,
							Interpreter:    "/lib/ld-linux.so.2",
							Dependencies: []string{
								"d1.so", "d2.so", "d3.so", "d4.so", "d5.so",
								"d6.so", "d7.so", "d8.so", "d9.so", "d10.so",
							},
							VersionRequirements: []VersionRequirement{
								{Library: "d1.so", Version: "V1"},
								{Library: "d2.so", Version: "V2"},
								{Library: "d3.so", Version: "V3"},
								{Library: "d4.so", Version: "V4"},
								{Library: "d5.so", Version: "V5"},
								{Library: "d6.so", Version: "V6"},
								{Library: "d7.so", Version: "V7"},
								{Library: "d8.so", Version: "V8"},
								{Library: "d9.so", Version: "V9"},
								{Library: "d10.so", Version: "V10"},
							},
							Completeness: MetadataComplete,
						},
					},
				},
			},
			{
				name: "string_truncation_over_4096_bytes",
				report: &ArtifactABIReport{
					Status:     ComparisonConsistent,
					Consistent: true,
					Variants: []*VariantABIReport{
						{
							Level:          "v1",
							Linkage:        LinkageDynamic,
							HasInterpreter: true,
							Interpreter:    "/lib/" + strings.Repeat("x", 5000) + ".so",
							Completeness:   MetadataComplete,
						},
					},
				},
			},
		}

		for _, tc := range testCases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				exactBytes, err := MeasureABIReportPresentation(tc.report)
				require.NoError(t, err)

				var buf bytes.Buffer
				err = RenderABIReport(&buf, tc.report)
				require.NoError(t, err)

				assert.Equal(t, exactBytes, uint64(buf.Len()),
					"presentation byte count must match rendered length exactly for test case: %s", tc.name)
			})
		}
	})

	t.Run("negative_control_8_1024_esc_names_exact_boundary", func(t *testing.T) {
		t.Parallel()

		esc1024 := strings.Repeat("\x1b", 1024)
		deps := make([]string, 8)
		for i := range deps {
			deps[i] = esc1024
		}

		rep := &VariantABIReport{
			Level:          "v1",
			Linkage:        LinkageDynamic,
			HasInterpreter: true,
			Interpreter:    testLdLinux,
			Dependencies:   deps,
			Completeness:   MetadataComplete,
		}

		artRep := &ArtifactABIReport{
			Variants:   []*VariantABIReport{rep},
			Status:     ComparisonConsistent,
			Consistent: true,
			DeploymentDisclaimer: "Matching declared ABI requirements does not guarantee deployment host compatibility; " +
				"the target system must provide the required dynamic linker and libraries.",
		}

		exactPresBytes, err := MeasureABIReportPresentation(artRep)
		require.NoError(t, err)
		assert.Greater(t, exactPresBytes, uint64(32768), "8 1024-ESC names must expand to > 32 KiB")

		// Calculate exact retained bytes during validation to find exact total report budget
		raTest := NewReportAccounting(0)
		require.NoError(t, validateReportBudgetWithLimits([]*VariantABIReport{rep}, raTest, defaultABILimits))
		require.NoError(t, raTest.reserve(uint64(len(artRep.DeploymentDisclaimer))))
		retainedBytes := raTest.usedBytes
		exactTotalBytes := retainedBytes + exactPresBytes

		// 1 byte below exact presentation budget must be rejected with ErrABIResourceLimit
		limitsDeny := defaultABILimits
		limitsDeny.MaxArtifactReportBytes = exactTotalBytes - 1
		_, errDeny := CompareVariantABIsWithOptions([]*VariantABIReport{rep}, false, limitsDeny)
		require.Error(t, errDeny)
		assert.True(t, errors.Is(errDeny, ErrABIResourceLimit), "must fail with ErrABIResourceLimit at exactTotalBytes-1")

		// Exact presentation budget must be accepted
		limitsAllow := defaultABILimits
		limitsAllow.MaxArtifactReportBytes = exactTotalBytes
		resAllow, errAllow := CompareVariantABIsWithOptions([]*VariantABIReport{rep}, false, limitsAllow)
		require.NoError(t, errAllow)
		require.NotNil(t, resAllow)

		var buf bytes.Buffer
		err = RenderABIReport(&buf, resAllow)
		require.NoError(t, err)
		assert.Equal(t, exactPresBytes, uint64(buf.Len()))
	})

	t.Run("single_reservation_format_mismatch_error", func(t *testing.T) {
		t.Parallel()

		diffs := []string{
			"differing interpreter configuration: variant v1 has interpreter, variant v2 does not",
			"differing symbol version requirements: variant v1 (1 requirements) vs variant v2 (2 requirements)",
		}

		// Calculate exact needed bytes
		var needed uint64 = uint64(len(ErrABIMismatch.Error()))
		for _, d := range diffs {
			needed += uint64(len(d) + mismatchDiffBulletOverhead)
		}

		// When budget is sufficient:
		ra := NewReportAccounting(needed)
		err := formatMismatchError(diffs, ra)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMismatch))
		assert.Equal(t, needed, ra.usedBytes, "ReportAccounting must be reserved exactly once with exact bytes")
		assert.Contains(t, err.Error(), diffs[0])
		assert.Contains(t, err.Error(), diffs[1])

		// When budget is insufficient:
		raSmall := NewReportAccounting(needed - 1)
		errSmall := formatMismatchError(diffs, raSmall)
		require.Error(t, errSmall)
		assert.True(t, errors.Is(errSmall, ErrABIMismatch), "must still unwrap to ErrABIMismatch")
		assert.Contains(t, errSmall.Error(), "detailed output omitted")
		assert.True(t, errors.Is(errSmall, ErrABIResourceLimit), "must wrap ErrABIResourceLimit")
	})
}

func TestR2_AtomicMultiBudgetScanAccounting(t *testing.T) {
	t.Parallel()

	t.Run("dedicated_scan_exhaustion", func(t *testing.T) {
		t.Parallel()
		strtab := []byte("unterminated_string_longer_than_scan_budget")
		acc := &inputAccounting{
			maxStringScanBytes: 10,
			maxMetadataBytes:   100,
			shared:             NewArtifactMetadataAccounting(100),
		}

		_, err := scanBoundedCString(strtab, 0, acc, "test_field", true)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
		assert.Equal(t, uint64(10), acc.stringScanBytesRead)
		assert.Equal(t, uint64(10), acc.metadataBytesRead)
		assert.Equal(t, uint64(10), acc.shared.usedBytes)
	})

	t.Run("local_metadata_exhaustion_blocks_scan_without_counter_mutation", func(t *testing.T) {
		t.Parallel()
		strtab := []byte("hello\x00")
		acc := &inputAccounting{
			maxStringScanBytes: 100,
			maxMetadataBytes:   50,
			metadataBytesRead:  50, // Already exhausted
			shared:             NewArtifactMetadataAccounting(100),
		}

		_, err := scanBoundedCString(strtab, 0, acc, "test_field", true)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
		assert.Equal(t, uint64(0), acc.stringScanBytesRead, "stringScanBytesRead must remain unchanged on local failure")
		assert.Equal(t, uint64(50), acc.metadataBytesRead, "metadataBytesRead must remain unchanged on local failure")
		assert.Equal(t, uint64(0), acc.shared.usedBytes, "shared.usedBytes must remain unchanged on local failure")
	})

	t.Run("shared_metadata_exhaustion_blocks_scan_without_counter_mutation", func(t *testing.T) {
		t.Parallel()
		strtab := []byte("hello\x00")
		shared := NewArtifactMetadataAccounting(50)
		shared.usedBytes = 50 // Already exhausted
		acc := &inputAccounting{
			maxStringScanBytes: 100,
			maxMetadataBytes:   100,
			metadataBytesRead:  10,
			shared:             shared,
		}

		_, err := scanBoundedCString(strtab, 0, acc, "test_field", true)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
		assert.Equal(t, uint64(0), acc.stringScanBytesRead, "stringScanBytesRead must remain unchanged on shared failure")
		assert.Equal(t, uint64(10), acc.metadataBytesRead, "metadataBytesRead must remain unchanged on shared failure")
		assert.Equal(t, uint64(50), acc.shared.usedBytes, "shared.usedBytes must remain unchanged on shared failure")
	})

	t.Run("failed_shared_reservation_after_local_checks_pass_leaves_all_counters_unchanged", func(t *testing.T) {
		t.Parallel()
		strtab := []byte("hello\x00")
		shared := NewArtifactMetadataAccounting(50)
		shared.usedBytes = 50 // Shared is exhausted, but local has 90 remaining
		acc := &inputAccounting{
			maxStringScanBytes: 100,
			maxMetadataBytes:   100,
			metadataBytesRead:  10,
			shared:             shared,
		}

		_, err := scanBoundedCString(strtab, 0, acc, "test_field", true)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
		assert.Equal(t, uint64(0), acc.stringScanBytesRead, "scan counter unchanged")
		assert.Equal(t, uint64(10), acc.metadataBytesRead, "local counter unchanged")
		assert.Equal(t, uint64(50), acc.shared.usedBytes, "shared counter unchanged")
	})

	t.Run("nil_shared_accounting_works_properly", func(t *testing.T) {
		t.Parallel()
		strtab := []byte("hello\x00")
		acc := &inputAccounting{
			maxStringScanBytes: 100,
			maxMetadataBytes:   100,
			shared:             nil,
		}

		s, err := scanBoundedCString(strtab, 0, acc, "test_field", true)
		require.NoError(t, err)
		assert.Equal(t, "hello", s)
		assert.Equal(t, uint64(6), acc.stringScanBytesRead)
		assert.Equal(t, uint64(6), acc.metadataBytesRead)
	})

	t.Run("refund_consistency_across_all_three_accounts", func(t *testing.T) {
		t.Parallel()
		// Large strtab so chunk size is stringScanChunkSize (64)
		strtab := make([]byte, 128)
		copy(strtab, "abc\x00xyz\x00")

		shared := NewArtifactMetadataAccounting(1000)
		acc1 := &inputAccounting{
			maxStringScanBytes: 1000,
			maxMetadataBytes:   1000,
			shared:             shared,
		}

		// First scan: "abc\x00" (4 bytes). Initial reservation 64, refunded 60.
		s1, err := scanBoundedCString(strtab, 0, acc1, "field1", true)
		require.NoError(t, err)
		assert.Equal(t, "abc", s1)
		assert.Equal(t, uint64(4), acc1.stringScanBytesRead)
		assert.Equal(t, uint64(4), acc1.metadataBytesRead)
		assert.Equal(t, uint64(4), shared.usedBytes)

		// Second scan on same input: "xyz\x00" (4 bytes at offset 4). Initial reservation 64, refunded 60.
		s2, err := scanBoundedCString(strtab, 4, acc1, "field2", true)
		require.NoError(t, err)
		assert.Equal(t, "xyz", s2)
		assert.Equal(t, uint64(8), acc1.stringScanBytesRead)
		assert.Equal(t, uint64(8), acc1.metadataBytesRead)
		assert.Equal(t, uint64(8), shared.usedBytes)

		// Second input using same shared account
		acc2 := &inputAccounting{
			maxStringScanBytes: 1000,
			maxMetadataBytes:   1000,
			shared:             shared,
		}
		s3, err := scanBoundedCString(strtab, 0, acc2, "field3", true)
		require.NoError(t, err)
		assert.Equal(t, "abc", s3)
		assert.Equal(t, uint64(4), acc2.stringScanBytesRead)
		assert.Equal(t, uint64(4), acc2.metadataBytesRead)
		assert.Equal(t, uint64(12), shared.usedBytes, "shared account must accumulate 4 + 4 + 4 = 12 bytes")
	})

	t.Run("retained_string_exhaustion_preserves_scan_accounting", func(t *testing.T) {
		t.Parallel()
		strtab := []byte("hello\x00")
		acc := &inputAccounting{
			maxStringBytes:     2, // Cannot retain 5-byte string
			maxStringScanBytes: 100,
			maxMetadataBytes:   100,
			shared:             NewArtifactMetadataAccounting(100),
		}

		_, err := scanBoundedCString(strtab, 0, acc, "test_field", true)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIResourceLimit))
		assert.Contains(t, err.Error(), "decoded string bytes")

		// Scan work must remain accounted
		assert.Equal(t, uint64(6), acc.stringScanBytesRead)
		assert.Equal(t, uint64(6), acc.metadataBytesRead)
		assert.Equal(t, uint64(6), acc.shared.usedBytes)
		assert.Equal(t, uint64(0), acc.retainedStringBytes, "no bytes copied to retained string buffer")
	})
}

func TestR3_UncertaintySemantics(t *testing.T) {
	t.Parallel()

	t.Run("singleton_ambiguous_with_complete_absent_and_partial_metadata", func(t *testing.T) {
		t.Parallel()

		// Case 1: Ambiguous with complete metadata
		rComplete := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageAmbiguous,
			Completeness: MetadataComplete,
		}
		res1, err1 := CompareVariantABIs([]*VariantABIReport{rComplete}, false)
		require.NoError(t, err1)
		assert.Equal(t, ComparisonUnknown, res1.Status)
		assert.False(t, res1.Consistent)
		require.Len(t, res1.Warnings, 1)
		assert.Contains(t, res1.Warnings[0], "ambiguous linkage")

		// Case 2: Ambiguous with absent metadata
		rAbsent := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageAmbiguous,
			Completeness: MetadataAbsent,
		}
		res2, err2 := CompareVariantABIs([]*VariantABIReport{rAbsent}, false)
		require.NoError(t, err2)
		assert.Equal(t, ComparisonUnknown, res2.Status)
		assert.False(t, res2.Consistent)

		// Case 3: Ambiguous with partial metadata -> both warnings present
		rPartial := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageAmbiguous,
			Completeness: MetadataPartial,
		}
		res3, err3 := CompareVariantABIs([]*VariantABIReport{rPartial}, false)
		require.NoError(t, err3)
		assert.Equal(t, ComparisonUnknown, res3.Status)
		assert.False(t, res3.Consistent)
		assert.Len(t, res3.Warnings, 2, "must contain both incomplete metadata and ambiguous linkage warnings")
	})

	t.Run("two_identical_ambiguous_reports", func(t *testing.T) {
		t.Parallel()

		r1 := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageAmbiguous,
			Dependencies: []string{testLibc},
			Completeness: MetadataComplete,
		}
		r2 := &VariantABIReport{
			Level:        "v2",
			Linkage:      LinkageAmbiguous,
			Dependencies: []string{testLibc},
			Completeness: MetadataComplete,
		}

		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.NoError(t, err)
		assert.Equal(t, ComparisonUnknown, res.Status)
		assert.False(t, res.Consistent)
		assert.Empty(t, res.Differences)
	})

	t.Run("ambiguous_with_differing_dependencies_returns_mismatch", func(t *testing.T) {
		t.Parallel()

		r1 := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageAmbiguous,
			Dependencies: []string{testLibc},
			Completeness: MetadataComplete,
		}
		r2 := &VariantABIReport{
			Level:        "v2",
			Linkage:      LinkageDynamic,
			Dependencies: []string{"libm.so.6"},
			Completeness: MetadataComplete,
		}

		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMismatch), "mismatch must take precedence over ambiguous status")
		assert.Equal(t, ComparisonInconsistent, res.Status)
		assert.False(t, res.Consistent)
		assert.NotEmpty(t, res.Differences)
	})

	t.Run("mismatch_winning_over_uncertain_variant", func(t *testing.T) {
		t.Parallel()

		r1 := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageDynamic,
			Dependencies: []string{testLibc},
			Completeness: MetadataPartial, // Uncertain variant
		}
		r2 := &VariantABIReport{
			Level:        "v2",
			Linkage:      LinkageDynamic,
			Dependencies: []string{"libm.so.6"}, // Differing dependencies
			Completeness: MetadataComplete,
		}

		res, err := CompareVariantABIs([]*VariantABIReport{r1, r2}, false)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrABIMismatch))
		assert.Equal(t, ComparisonInconsistent, res.Status)
		assert.False(t, res.Consistent)
	})

	t.Run("cause_specific_cli_notes_rendered", func(t *testing.T) {
		t.Parallel()

		// Ambiguous only
		repAmbiguous := &ArtifactABIReport{
			Status: ComparisonUnknown,
			Variants: []*VariantABIReport{
				{Level: "v1", Linkage: LinkageAmbiguous, Completeness: MetadataComplete},
			},
		}
		var bufAmbiguous bytes.Buffer
		require.NoError(t, RenderABIReport(&bufAmbiguous, repAmbiguous))
		outAmb := bufAmbiguous.String()
		assert.Contains(t, outAmb, "linkage is ambiguous (payload declares dynamic dependencies but has no PT_INTERP)")
		assert.NotContains(t, outAmb, "symbol version metadata is partial or unsupported")

		// Version incomplete only
		repVer := &ArtifactABIReport{
			Status: ComparisonUnknown,
			Variants: []*VariantABIReport{
				{Level: "v1", Linkage: LinkageDynamic, Completeness: MetadataPartial},
			},
		}
		var bufVer bytes.Buffer
		require.NoError(t, RenderABIReport(&bufVer, repVer))
		outVer := bufVer.String()
		assert.Contains(t, outVer, "symbol version metadata is partial or unsupported")
		assert.NotContains(t, outVer, "linkage is ambiguous")

		// Both ambiguous and version incomplete
		repBoth := &ArtifactABIReport{
			Status: ComparisonUnknown,
			Variants: []*VariantABIReport{
				{Level: "v1", Linkage: LinkageAmbiguous, Completeness: MetadataPartial},
			},
		}
		var bufBoth bytes.Buffer
		require.NoError(t, RenderABIReport(&bufBoth, repBoth))
		outBoth := bufBoth.String()
		assert.Contains(t, outBoth, "linkage is ambiguous (payload declares dynamic dependencies but has no PT_INTERP)")
		assert.Contains(t, outBoth, "symbol version metadata is partial or unsupported")
	})

	t.Run("render and measure edge cases", func(t *testing.T) {
		t.Parallel()

		// Nil and empty reports
		assert.NoError(t, RenderABIReport(nil, nil))
		assert.NoError(t, RenderABIReport(nil, &ArtifactABIReport{}))
		var buf bytes.Buffer
		assert.NoError(t, RenderABIReport(&buf, nil))
		assert.NoError(t, RenderABIReport(&buf, &ArtifactABIReport{}))

		n, err := MeasureABIReportPresentation(nil)
		require.NoError(t, err)
		assert.Equal(t, uint64(0), n)

		n, err = MeasureABIReportPresentation(&ArtifactABIReport{})
		require.NoError(t, err)
		assert.Equal(t, uint64(0), n)

		// Discard writer
		rep := &ArtifactABIReport{
			Status: ComparisonConsistent,
			Variants: []*VariantABIReport{
				{
					Level:          "v1",
					Linkage:        LinkageDynamic,
					HasInterpreter: true,
					Interpreter:    "/lib64/ld-linux-x86-64.so.2",
					Dependencies:   []string{"libc.so.6"},
					VersionRequirements: []VersionRequirement{
						{Library: testLibc, Version: testGlibc225},
					},
				},
			},
		}
		assert.NoError(t, RenderABIReport(nil, rep))

		// Writer errors covering writeLiteral, writeEscaped, writeEscapedToWriter
		for i := 1; i <= 10; i++ {
			fw := &qualificationFailWriter{failOnWrite: i}
			_ = RenderABIReport(fw, rep)
		}
	})

	t.Run("accounting and warning edge branches", func(t *testing.T) {
		t.Parallel()

		// Nil accounting
		var nilAcct *ArtifactMetadataAccounting
		assert.Equal(t, uint64(math.MaxUint64), nilAcct.remaining())
		nilAcct.refund(100)
		assert.NoError(t, nilAcct.check(100))
		nilAcct.commit(100)

		// Zero maxBytes default
		zeroLimit := &ArtifactMetadataAccounting{maxBytes: 0, usedBytes: 10}
		assert.Equal(t, uint64(MaxArtifactMetadataBytes-10), zeroLimit.remaining())

		// Excess refund clamp
		acct := &ArtifactMetadataAccounting{maxBytes: 100, usedBytes: 20}
		acct.refund(50)
		assert.Equal(t, uint64(0), acct.usedBytes)

		// Pre-existing ambiguous linkage warning in single report
		preWarnRep := &VariantABIReport{
			Level:        "v1",
			Linkage:      LinkageAmbiguous,
			Completeness: MetadataComplete,
			Warnings:     []string{"linkage is ambiguous already"},
		}
		ra := NewReportAccounting(defaultABILimits.MaxArtifactReportBytes)
		singleRep, err := handleSingleReport(preWarnRep, ra, &ArtifactABIReport{Variants: []*VariantABIReport{preWarnRep}})
		require.NoError(t, err)
		assert.Equal(t, ComparisonUnknown, singleRep.Status)
		assert.Len(t, singleRep.Warnings, 1)
	})
}

type qualificationFailWriter struct {
	failOnWrite int
	writes      int
}

func (f *qualificationFailWriter) Write(p []byte) (int, error) {
	f.writes++
	if f.failOnWrite == 0 || f.writes >= f.failOnWrite {
		return 0, errors.New("simulated writer error")
	}
	return len(p), nil
}
