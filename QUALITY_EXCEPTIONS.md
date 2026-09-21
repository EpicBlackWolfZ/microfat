# Reviewed complexity exceptions

This is the source-level exception policy for the v0.2.5 quality review. It is
not a list of hidden findings or a grade target. Re-review an entry whenever its
function gains a new responsibility, changes an invariant, or changes resource
ownership. A new finding requires its own review; a similar filename or score
does not inherit an exception.

`revive.toml` uses CodeFactor's supported root configuration and the same numeric
cyclomatic ceiling (25) as `.golangci.yml`. Revive also counts default switch
arms, so its values are not identical to gocyclo. All previously applicable
CodeFactor Revive checks remain enabled; the provider's old `warning-comment`
entry is not a rule in its documented Revive 1.16.0. Reasons and rule names are
mandatory for disable directives. There are no file or test-tree exclusions.

CodeFactor's separate **Complex Method** metric is a readability review signal,
not Revive's cyclomatic score. This TOML does not suppress that metric. Accepted
findings below remain visible in CodeFactor; no provider-side ignores are needed.
The review preserves ordered validation, explicit ownership, independent test
oracles and test coverage. It does not remove checks to reduce a score.

Supported configuration and metric definitions:
[CodeFactor analysis tools](https://docs.codefactor.io/bootcamp/analysis-tools/),
[CodeFactor glossary](https://docs.codefactor.io/bootcamp/glossary/),
[Revive directives](https://github.com/mgechev/revive/tree/v1.16.0#comment-directives).

## Narrow Revive exceptions

Each directive disables only `cyclomatic` on the following function declaration.
It does not disable other checks, neighboring functions, or a whole file. Test
matrices retain their named subtests and failure semantics; fixture mutation and
cleanup remain explicit. Static ISA mappings avoid reflection and mutable lookup
state on the dispatch path.

| Function | Reason |
| --- | --- |
| [TestCanonicalSerializationAndEvidence](benchmarks/schema/schema_test.go) | Keep canonical bytes, digest, tampering and round-trip evidence checks in one fixture lifecycle. |
| [TestEndToEndFatBinaryWorkflow](cmd/microfat/integration_test.go) | Keep build, pack, trim, prewarm and optimize steps tied to the same executable and environment. |
| [TestMaterializeVariantAtFD](internal/cache/cache_test.go) | Keep write, checksum, rename and post-rename fault injection beside temporary-file cleanup assertions. |
| [TestOpenAndValidateFD_LifecycleAndPurge](internal/cache/cache_test.go) | Keep descriptor lifetime, unlink replacement and purge decisions visible in the same fixture matrix. |
| [TestVerifyVariant](internal/cache/cache_test.go) | Keep the missing, truncated, corrupted and valid cache status matrix explicit. |
| [TestResolveCacheDir_SecurityValidation](internal/format/format_test.go) | Keep explicit ownership, symlink and permission attack fixtures together with their restoration hooks. |
| [TestARM64Fixtures_PrerequisiteDegradation](internal/microarch/arm64_fixtures_test.go) | Keep explicit feature mutations independent of production feature lookup. |
| [Rank](internal/microarch/microarch.go) | Keep the static architecture rank mapping explicit; Revive counts default arms unlike gocyclo. |
| [hasARM64NamedFeature](internal/microarch/microarch.go) | Keep the static feature mapping explicit without reflection or runtime dispatch tables. |
| [TestMicroarch_AMD64MonotonicityInvariants](internal/microarch/monotonicity_test.go) | Keep the explicit independent ISA prerequisite oracle; do not reuse the selector being tested. |
| [TestAutoDictionaryTrainingDiagnostics](internal/pack/pack_test.go) | Keep explicit versus automatic dictionary failure policy and its warning expectations together. |
| [TestPrewarmVariantWithDict_IntegrityAndAtomicReplacement](internal/pack/pack_test.go) | Keep cache recovery, atomic replacement and concurrent-writer outcomes explicit. |
| [TestVerifyCacheVariantAndBinary](internal/pack/pack_test.go) | Keep successive missing, mixed, valid and corrupted cache states visible in one lifecycle. |
| [TestCacheSecurityAndFilesystemInvariants](tests/e2e/cache_security_test.go) | Keep filesystem attack scenarios and concurrent cache recovery assertions explicit. |
| [TestCorruptionAndSecurityBoundary](tests/e2e/corruption_test.go) | Keep independent malformed-byte fixtures and no-payload-execution assertions explicit. |
| [TestGoldenVariantSelection_Deterministic](tests/e2e/golden_variant_test.go) | Keep architecture-specific compatibility and forbidden-execution expectations independent of the selector. |
| [TestLifecycleReleaseSmoke](tests/e2e/lifecycle_test.go) | Keep the ordered release lifecycle and launcher meta-command outcomes visible together. |
| [TestProcessFidelityAndExecutionInvariants](tests/e2e/process_fidelity_test.go) | Keep argv, environment, stdin, exit status and executable-identity scenarios explicit. |

## CodeFactor review

The final candidate's retained Complex Method findings and specific rationales
are recorded here after the hosted scan. Resolved duplication and maintainability
findings do not need permanent exceptions.
