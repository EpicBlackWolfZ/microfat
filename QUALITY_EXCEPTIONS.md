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
not Revive's cyclomatic score. Review it independently of configured lint results
and the PR delta view. No provider-side ignores are added.
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

## Reviewed production functions

The following functions appeared in the full baseline scan after the tooling
migrations. The scoped review retains their validation and ownership structure
for the reasons below, even if a future hosted scan reports a complexity warning.
These entries do not suppress any rule. Existing golangci-lint limits continue to
apply. They are separate from the 18 explicit Revive exceptions above.

| Function | Decision and reason |
| --- | --- |
| [Calibrate](benchmarks/ci/calibration.go) | Keep training and holdout cohorts distinct and retain report-only fallback when calibration cannot qualify. |
| [Metric](benchmarks/compare/compare.go) | Keep pairing, provenance, scope compatibility and missing-data outcomes explicit; incomplete evidence stays inconclusive. |
| [traverseCgroupV1CPU](benchmarks/env/cgroup.go) | Fix #249 using same-cgroup quota/period pairs; malformed or missing finite periods fail instead of borrowing an ancestor's period. |
| [traverseCgroupV2CPUMax](benchmarks/env/cgroup.go) | Fix #249 using exact quota/period comparison; retain ancestor traversal and unavailable/error states without rounding CPUs. |
| [detectCPUFreq](benchmarks/env/cpu_linux.go) | Keep sysfs location fallback and individually unavailable frequency observations explicit. |
| [Render](benchmarks/report/report.go) | Keep the small explicit output-format dispatch and each format's rendering rules visible; no new templating dependency. |
| [VerifyFiles](benchmarks/report/report.go) | Keep evidence path, symlink, size and digest checks adjacent to bounded reads; unverified bytes must not be consumed. |
| [buildArtifacts](benchmarks/runner/artifacts.go) | Keep source build, stub selection, PGO, packaging and artifact registration in their resource-owning sequence. |
| [RunExperiment](benchmarks/runner/experiment.go) | Keep configuration, scheduling, process outcomes and evidence publication in one ordered experiment lifecycle. |
| [validateControls](benchmarks/runner/experiment_config.go) | Keep runtime variables, cache mode and CPU-affinity conflicts explicit before building or running a benchmark. |
| [Run](benchmarks/runner/runner.go) | Keep each workload's preparation, execution, cleanup and evidence construction in one visible lifecycle. |
| [runProcessTrial](benchmarks/runner/trial.go) | Keep launch, cancellation, collection, telemetry and failed-trial evidence in one bounded process lifecycle. |
| [ValidateV2](benchmarks/schema/experiment_v2.go) | Keep schema, configuration, provenance and outcome validation ordered before downstream evidence use. |
| [validateProcessTrials](benchmarks/schema/experiment_v2.go) | Keep schedule membership, duplicate trials, timestamps, status and telemetry constraints explicit. |
| [ValidateExperiment](benchmarks/schema/validation.go) | Keep fail-fast schema, identity, timestamp and environment validation in explicit order before scenario traversal. |
| [validateAnalysis](benchmarks/schema/validation.go) | Keep finite-value, sample-count and percentile boundary checks explicit; no generic reflection validator. |
| [validateScenario](benchmarks/schema/validation.go) | Keep observation identity, temporal ordering and metric validation adjacent; invalid observations must not reach analysis. |
| [buildAutoTunedEnviron](cmd/microfat-stub/exec_linux.go) | Keep user environment precedence, dry-run handling and dispatch metadata explicit without another launcher dependency. |
| [executeViaCache](cmd/microfat-stub/exec_linux.go) | Keep directory and executable descriptor ownership, materialization and execution cleanup in the same scope. |
| [newPackCmd](cmd/microfat/main.go) | Reuse existing build options and flag binding; keep manifest and direct-variant validation paths explicit. |
| [newPrewarmCmd](cmd/microfat/main.go) | Share JSON rendering; keep verify-only nonmutation and prewarm materialization paths explicit. |
| [newTrimCmd](cmd/microfat/main.go) | Keep input descriptor and temporary-output ownership, sync, chmod, close and atomic rename in one CLI operation. |
| [runSIMDMathWorkload](examples/demo/main.go) | Preserve deliberate unrolled benchmark work and optimizer behavior; metric-only extraction would change the measured workload. |
| [BuildAndPack](internal/builder/builder.go) | Keep temporary-build ownership and manifest/build/pack order visible so failures clean up only owned artifacts. |
| [assemblePackOptions](internal/builder/builder.go) | Keep manifest versus explicit CLI override precedence, dictionary policy and per-variant settings readable. |
| [MaterializeVariantAtFD](internal/cache/cache_other.go) | Keep temporary-file ownership, bounded write, integrity validation, atomic publication and post-publication checks adjacent on each platform. |
| [MaterializeVariantAtFD](internal/cache/cache_unix.go) | Keep temporary-file ownership, bounded write, integrity validation, atomic publication and post-publication checks adjacent on each platform. |
| [readCgroupV1](internal/cgroup/cgroup.go) | Keep separate memory/CPU controllers and their unavailable or malformed states explicit. |
| [ResolveCompression](internal/codec/codec.go) | Keep profile validation and explicit algorithm/level precedence visible; invalid combinations must still fail. |
| [OpenAndValidateCacheDirFD](internal/format/cache_unix.go) | Keep descriptor validation, ownership and permission repair in one audited sequence; no pathname reopening or new abstraction. |
| [UnmarshalBinaryIndex](internal/format/format.go) | Keep each length and cursor bound check immediately before the corresponding slice; avoid an allocation-heavy generic decoder. |
| [extractX86FeatureList](internal/microarch/microarch.go) | Keep the static CPU feature-to-name mapping explicit; do not introduce reflection or mutable runtime tables. |
| [PrewarmBinary](internal/pack/pack.go) | Keep dictionary bounds and checksum verification before allocation/use, and variant selection before materialization. |
| [PrewarmVariantWithDict](internal/pack/pack.go) | Keep dictionary integrity, cache validation, decompression limits and atomic replacement together with their owned resources. |
| [validateOptions](internal/pack/pack.go) | Keep ISA alias collision, default selection and ELF validation ordering explicit before any output is published. |
| [writeVariantPayload](internal/pack/pack.go) | Share raw/canonical override application while retaining raw-key precedence, codec selection and bounded payload accounting. |
| [ExtractFileFromArchive](internal/releasecheck/archive.go) | Keep path, duplicate-entry, size and complete-archive validation before publishing the extracted destination. |
| [applyTuningPlan](runtimeinit/runtimeinit.go) | Keep the dry-run return before all runtime setters and preserve each user environment override. |

## Hosted scan acceptance

Review both the PR delta and the full analyzed branch at the release candidate.
CodeFactor [does not run duplication checks on pull requests](https://docs.codefactor.io/common-tasks/duplication-issues/),
so the PR's fixed/new counts cannot establish that every baseline finding is gone.
Reconcile all baseline findings and any new findings with actual source locations.
Keep machine inventories and test evidence in `.work/` and reference the final
scan and its commit in the issue or PR review. A retained finding needs a specific
rationale here; a new actionable bug needs a fix or a milestone issue.
