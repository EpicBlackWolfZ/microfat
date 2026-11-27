package docgate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDateLine = "- **Date**: 2026-11-27"

func writeFixtureFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

func seedMinimalValidRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, "README.md", strings.Join([]string{
		"# Microfat",
		"",
		"See [ADR index](docs/adr/README.md) and [Guide](#guide).",
		"",
		"## Guide",
		"",
		"## Guide",
		"",
		"Anchor to duplicate [second](#guide-1).",
	}, "\n"))
	writeFixtureFile(t, root, "docs/adr/TEMPLATE.md", "# ADR-NNNN\n")
	writeFixtureFile(t, root, "docs/adr/README.md", strings.Join([]string{
		"# ADRs",
		"",
		"- [ADR-0001](../design/adr-0001-formal-verification-and-v3-lab.md)",
		"- [ADR-0012](ADR-0012-governance.md)",
	}, "\n"))
	writeFixtureFile(t, root, "docs/design/adr-0001-formal-verification-and-v3-lab.md", strings.Join([]string{
		"# ADR-0001",
		"- **Status**: Accepted",
		"- **Date**: 2026-11-23",
		"- **Related Issues**: #378",
		"## 1. Context",
		"## 2. Decision",
		"## 3. Promotion & Rollback Criteria",
	}, "\n"))
	writeFixtureFile(t, root, "docs/adr/ADR-0012-governance.md", strings.Join([]string{
		"# ADR-0012",
		"- **Status**: Accepted",
		testDateLine,
		"- **Deciders**: Maintainer",
		"- **Related Issues**: #524",
		"## Context",
		"## Decision",
		"## Rollback",
	}, "\n"))
	writeFixtureFile(t, root, "docs/specs/TEMPLATE.md", "# SPEC-NNNN\n")
	writeFixtureFile(t, root, "docs/specs/README.md", "# SPECs\n\n- [SPEC-0001](SPEC-0001-format-v2.md)\n")
	writeFixtureFile(t, root, "docs/specs/SPEC-0001-format-v2.md", strings.Join([]string{
		"# SPEC-0001",
		"- **Spec ID**: SPEC-0001",
		"- **Status**: Normative",
		testDateLine,
		"- **Related ADRs**: ADR-0012",
		"- **Verified By**: Tests",
		"## Scope",
		"## Normative Requirements",
		"- `REQ-0001-01`: Trailer MUST be 56 bytes.",
		"- `LAW-0001-01`: Bounds cannot overflow.",
		"## Traceability Matrix",
	}, "\n"))
	writeFixtureFile(t, root, "docs/verification/README.md", strings.Join([]string{
		"# Verification Ledger",
		"",
		"| Law ID | Target | Pin | Task | Gate | Notes |",
		"| :--- | :--- | :--- | :--- | :--- | :--- |",
		"| `LAW-CGROUP-01` | [memory.go](../../internal/cgroup/memory.go) | `GOBRA_LINUX_AMD64` | " +
			"`task verify-cgroup-memory` | `ci.yml:cgroup-memory-proof`, `release.yml:cgroup-memory-proof` | Scope |",
	}, "\n"))
	writeFixtureFile(t, root, "internal/cgroup/memory.go", "package cgroup\n")
	writeFixtureFile(t, root, ".config/tool-versions.env", "# pins\nGOBRA_LINUX_AMD64=v26.09@abc\n")
	writeFixtureFile(t, root, "Taskfile.yml", "version: '3'\ntasks:\n  verify-cgroup-memory:\n    cmds: []\n")
	writeFixtureFile(t, root, ".github/workflows/ci.yml", "jobs:\n  lint:\n    steps:\n      - id: cgroup-memory-proof\n")
	writeFixtureFile(t, root, ".github/workflows/release.yml", "jobs:\n  gate:\n    steps:\n      - id: cgroup-memory-proof\n")
	writeFixtureFile(t, root, "cmd/microfat/main.go",
		"package main\nvar c = &cobra.Command{Use: \"detect\"}\nvar h = &cobra.Command{Use: \"hide\", Hidden: true}\n")
	writeFixtureFile(t, root, "internal/format/format.go", strings.Join([]string{
		"package format",
		"const (",
		"\tMagicString = \"\\x00\\xFA\\x7FMICRO\"",
		"\tTrailerSize = 56",
		"\tIndexMagicV2 = \"\\x00\\xFAM2\"",
		"\tFormatVersion2 = 2",
		"\tMaxIndexSize = 1024 * 1024",
		"\tEnvExecMode = \"MICROFAT_EXEC_MODE\"",
		")",
	}, "\n"))
	writeFixtureFile(t, root, "docs/cli-reference.md",
		"# CLI\n\n### `microfat detect`\n\n### `microfat prewarm <binary>`\n\n`MICROFAT_EXEC_MODE`\n")
	writeFixtureFile(t, root, "docs/architecture.md", strings.Join([]string{
		"# Arch",
		"",
		"56-Byte trailer `\\x00\\xFA\\x7FMICRO` and Format v2 `\\x00\\xFAM2`.",
		"",
		"See [prewarm](cli-reference.md#microfat-prewarm-binary).",
	}, "\n"))
	writeFixtureFile(t, root, "experiments/v3lab/rust-stub/src/format.rs", strings.Join([]string{
		`pub const TRAILER_MAGIC: &[u8; 8] = b"\x00\xFA\x7FMICRO";`,
		`pub const TRAILER_SIZE: usize = 56;`,
		`pub const INDEX_MAGIC_V2: &[u8; 4] = b"\x00\xFAM2";`,
		`pub const FORMAT_VERSION_2: u16 = 2;`,
		`pub const MAX_INDEX_SIZE: u64 = 1024 * 1024;`,
	}, "\n"))
	return root
}

func hasRule(findings []Finding, rule string) bool {
	for _, f := range findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func TestCheckLiveRepositoryPasses(t *testing.T) {
	t.Parallel()
	report, err := Check(filepath.Join("..", ".."))
	require.NoError(t, err)
	assert.True(t, report.OK(), report.Format())
	assert.Positive(t, report.FilesChecked)
	assert.Contains(t, report.Format(), "doc-gate: OK")
}

func TestCheckMinimalSeedRepoAndErrorHandling(t *testing.T) {
	t.Parallel()
	root := seedMinimalValidRepo(t)
	report, err := Check(root)
	require.NoError(t, err)
	assert.True(t, report.OK(), report.Format())

	_, err = Check(filepath.Join(root, "does-not-exist"))
	require.Error(t, err)

	notDir := filepath.Join(root, "README.md")
	_, err = Check(notDir)
	require.Error(t, err)

	_, err = resolveWithinRoot(root, "/abs/path")
	require.Error(t, err)
	_, err = resolveWithinRoot(root, "../outside")
	require.Error(t, err)
	assert.False(t, repoPathExists(root, "../outside"))
}

func TestMarkdownLinksAndAnchors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		mutate   func(t *testing.T, root string)
		wantRule string
	}{
		{
			name: "broken_relative_link",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "README.md", "[missing](docs/missing.md)\n")
			},
			wantRule: "broken-link",
		},
		{
			name: "broken_same_file_anchor",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "README.md", "[bad](#nope)\n")
			},
			wantRule: "broken-anchor",
		},
		{
			name: "broken_cross_file_anchor",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "README.md", "[bad](docs/architecture.md#nope)\n")
			},
			wantRule: "broken-anchor",
		},
		{
			name: "path_escape_rejected",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "README.md", "[escape](../../etc/passwd)\n")
			},
			wantRule: "path-escape",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := seedMinimalValidRepo(t)
			tc.mutate(t, root)
			report, err := Check(root)
			require.NoError(t, err)
			assert.False(t, report.OK())
			assert.True(t, hasRule(report.Findings, tc.wantRule), report.Format())
			assert.Contains(t, report.Format(), "doc-gate: FAILED")
		})
	}
}

func TestMarkdownIgnoresFencesInlineCodeAndExternalLinks(t *testing.T) {
	t.Parallel()
	root := seedMinimalValidRepo(t)
	writeFixtureFile(t, root, "README.md", strings.Join([]string{
		"# Title",
		"",
		"```markdown",
		"[ignored-in-fence](docs/does-not-exist.md)",
		"```",
		"",
		"Inline code `[ignored-inline](docs/does-not-exist.md)` and " +
			"[ext](https://example.com) and [glob](docs/*.md) and [bracket](<docs/adr/README.md>).",
		"",
		"[ref]: https://example.org",
	}, "\n"))

	report, err := Check(root)
	require.NoError(t, err)
	assert.True(t, report.OK(), report.Format())
}

func TestADRAndSPECAndLedgerValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		mutate   func(t *testing.T, root string)
		wantRule string
	}{
		{
			name: "invalid_adr_filename",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/adr/bad-name.md", "# Bad\n")
			},
			wantRule: "adr-filename",
		},
		{
			name: "duplicate_adr_number",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/adr/ADR-0012-dup.md", strings.Join([]string{
					"# ADR-0012",
					"- **Status**: Accepted",
					testDateLine,
					"- **Deciders**: A",
					"- **Related Issues**: #1",
					"## Context",
					"## Decision",
					"## Rollback",
				}, "\n"))
			},
			wantRule: "adr-duplicate-number",
		},
		{
			name: "missing_adr_metadata_and_sections",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/adr/ADR-0012-governance.md", "# ADR-0012\n- **Date**: bad\n## Context\n")
			},
			wantRule: "adr-metadata",
		},
		{
			name: "adr_index_out_of_sync",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/adr/README.md", "# ADRs\n")
			},
			wantRule: "adr-index-sync",
		},
		{
			name: "invalid_spec_filename",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/specs/bad-spec.md", "# Bad\n")
			},
			wantRule: "spec-filename",
		},
		{
			name: "duplicate_spec_number_and_req_id",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/specs/SPEC-0001-dup.md", strings.Join([]string{
					"# SPEC",
					"- **Spec ID**: SPEC-0001",
					"- **Status**: Draft",
					testDateLine,
					"- **Related ADRs**: ADR-0012",
					"- **Verified By**: Tests",
					"## Scope",
					"## Normative Requirements",
					"- `REQ-0001-01`: dup",
					"## Traceability",
				}, "\n"))
			},
			wantRule: "spec-duplicate-id",
		},
		{
			name: "spec_index_out_of_sync",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/specs/README.md", "# SPECs\n")
			},
			wantRule: "spec-index-sync",
		},
		{
			name: "ledger_unknown_pin_task_and_step",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/verification/README.md",
					"# Ledger\n\n| `LAW-BAD-01` | [memory.go](../../internal/cgroup/memory.go) | "+
						"`MISSING_AMD64` | `task missing-task` | `ci.yml:missing-step` | x |\n")
			},
			wantRule: "ledger-unknown-pin",
		},
		{
			name: "ledger_duplicate_law",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/verification/README.md",
					"# Ledger\n\n| `LAW-DUP-01` | a | b | c | d | e |\n| `LAW-DUP-01` | a | b | c | d | e |\n")
			},
			wantRule: "ledger-duplicate-law",
		},
		{
			name: "ledger_empty",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/verification/README.md", "# Ledger\n\nNo entries.\n")
			},
			wantRule: "ledger-empty",
		},
		{
			name: "ledger_missing",
			mutate: func(t *testing.T, root string) {
				require.NoError(t, os.Remove(filepath.Join(root, "docs/verification/README.md")))
			},
			wantRule: "ledger-missing",
		},
		{
			name: "adr_missing_structure",
			mutate: func(t *testing.T, root string) {
				require.NoError(t, os.Remove(filepath.Join(root, "docs/adr/TEMPLATE.md")))
			},
			wantRule: "adr-structure",
		},
		{
			name: "adr_missing_rollback_section",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/adr/ADR-0012-governance.md", strings.Join([]string{
					"# ADR-0012",
					"- **Status**: Accepted",
					testDateLine,
					"- **Deciders**: A",
					"- **Related Issues**: #1",
					"## Context",
					"## Decision",
				}, "\n"))
			},
			wantRule: "adr-section",
		},
		{
			name: "spec_missing_structure",
			mutate: func(t *testing.T, root string) {
				require.NoError(t, os.Remove(filepath.Join(root, "docs/specs/TEMPLATE.md")))
			},
			wantRule: "spec-structure",
		},
		{
			name: "spec_missing_metadata_section_and_reqs",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/specs/SPEC-0001-format-v2.md", "# SPEC\n- **Date**: bad\n## Scope\n")
			},
			wantRule: "spec-requirements",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := seedMinimalValidRepo(t)
			tc.mutate(t, root)
			report, err := Check(root)
			require.NoError(t, err)
			assert.False(t, report.OK())
			assert.True(t, hasRule(report.Findings, tc.wantRule), report.Format())
		})
	}
}

func TestCodeDocDriftChecks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		mutate   func(t *testing.T, root string)
		wantRule string
	}{
		{
			name: "undocumented_cli_command",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "cmd/microfat/newcmd.go", "package main\nvar n = &cobra.Command{Use: \"newcmd\"}\n")
			},
			wantRule: "drift-cli-command",
		},
		{
			name: "undocumented_env_var",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/cli-reference.md", "# CLI\n\n### `microfat detect`\n")
			},
			wantRule: "drift-env-var",
		},
		{
			name: "architecture_trailer_mismatch",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "docs/architecture.md", "# Arch\n\nNo constants here.\n")
			},
			wantRule: "drift-format-doc",
		},
		{
			name: "go_format_parse_failure",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "internal/format/format.go", "package format\n")
			},
			wantRule: "drift-format-parse",
		},
		{
			name: "rust_format_constant_drift",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "experiments/v3lab/rust-stub/src/format.rs",
					"pub const TRAILER_SIZE: usize = 64;\npub const FORMAT_VERSION_2: u16 = 2;\n")
			},
			wantRule: "drift-rust-format",
		},
		{
			name: "rust_max_index_size_drift",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "experiments/v3lab/rust-stub/src/format.rs", strings.Join([]string{
					`pub const TRAILER_MAGIC: &[u8; 8] = b"\x00\xFA\x7FMICRO";`,
					`pub const TRAILER_SIZE: usize = 56;`,
					`pub const INDEX_MAGIC_V2: &[u8; 4] = b"\x00\xFAM2";`,
					`pub const FORMAT_VERSION_2: u16 = 2;`,
					`pub const MAX_INDEX_SIZE: u64 = 512;`,
				}, "\n"))
			},
			wantRule: "drift-rust-format",
		},
		{
			name: "duplicate_env_constant_and_release_gate_ledger",
			mutate: func(t *testing.T, root string) {
				writeFixtureFile(t, root, "internal/format/format.go", strings.Join([]string{
					"package format",
					"const (",
					"\tMagicString = \"\\x00\\xFA\\x7FMICRO\"",
					"\tTrailerSize = 56",
					"\tIndexMagicV2 = \"\\x00\\xFAM2\"",
					"\tFormatVersion2 = 2",
					"\tMaxIndexSize = 1024 * 1024",
					"\tEnvExecMode = \"MICROFAT_EXEC_MODE\"",
					"\tEnvExecDup = \"MICROFAT_EXEC_MODE\"",
					"\tEnvMissing = \"MICROFAT_UNDOCUMENTED\"",
					")",
				}, "\n"))
			},
			wantRule: "drift-env-var",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := seedMinimalValidRepo(t)
			tc.mutate(t, root)
			report, err := Check(root)
			require.NoError(t, err)
			assert.False(t, report.OK())
			assert.True(t, hasRule(report.Findings, tc.wantRule), report.Format())
		})
	}
}

func TestDocGateEdgeCasesAndErrors(t *testing.T) {
	t.Parallel()

	t.Run("lazy_loaded_cross_file_anchor_outside_docs", func(t *testing.T) {
		t.Parallel()
		root := seedMinimalValidRepo(t)
		writeFixtureFile(t, root, "extra/note.md", "# Extra\n\n## Sub Section\n")
		writeFixtureFile(t, root, "README.md", strings.Join([]string{
			"# Main",
			"",
			"[ok](extra/note.md#sub-section) [skip](#) [empty]() [bad](extra/note.md#missing)",
		}, "\n"))
		report, err := Check(root)
		require.NoError(t, err)
		assert.False(t, report.OK())
		assert.True(t, hasRule(report.Findings, "broken-anchor"), report.Format())
	})

	t.Run("unclosed_backtick_and_malformed_metadata_handled", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "hello-world", gfmSlug("`hello world"))
		assert.Equal(t, "", cleanLinkTarget("   "))
		assert.True(t, shouldSkipTarget("#"))
		meta := extractMetadata("- **Valid**: ok\n- **NoColon\n## Stop\n- **After**: no\n")
		assert.Equal(t, "ok", meta["Valid"])
		assert.Empty(t, meta["After"])
	})

	t.Run("file_size_limit_and_missing_required_inputs_return_errors", func(t *testing.T) {
		t.Parallel()
		root := seedMinimalValidRepo(t)
		big := strings.Repeat("a", maxDocFileBytes+1)
		writeFixtureFile(t, root, "docs/huge.md", big)
		_, err := Check(root)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "byte limit")

		for _, drop := range []string{
			"docs/cli-reference.md",
			"internal/format/format.go",
			"docs/architecture.md",
			".config/tool-versions.env",
			"Taskfile.yml",
			".github/workflows/ci.yml",
			".github/workflows/release.yml",
		} {
			r := seedMinimalValidRepo(t)
			require.NoError(t, os.Remove(filepath.Join(r, filepath.FromSlash(drop))))
			_, checkErr := Check(r)
			require.Error(t, checkErr, "expected error when %s is missing", drop)
		}
	})

	t.Run("ledger_missing_release_step", func(t *testing.T) {
		t.Parallel()
		root := seedMinimalValidRepo(t)
		writeFixtureFile(t, root, ".github/workflows/release.yml", "jobs:\n  gate:\n    steps: []\n")
		report, err := Check(root)
		require.NoError(t, err)
		assert.True(t, hasRule(report.Findings, "ledger-unknown-ci-step"), report.Format())
	})
}
