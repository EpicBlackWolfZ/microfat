# Cgroup memory arithmetic laws

[`internal/cgroup/memory.go`](../../internal/cgroup/memory.go) is both the Go build
input and the Gobra input. The `@` comments add proof obligations without adding
runtime code. The proof uses mathematical integers in the specification and
checks signed overflow in the executable operations.

| Law | Checked contract |
| --- | --- |
| **Extraction estimate** | For every four `int64` operands, `ExtractionMemory` succeeds exactly when each operand is nonnegative and their mathematical sum fits in `int64`. Success returns that sum. Failure returns zero and `ErrMemoryBudget`. |
| **Retained storage** | For every two `int64` operands, `RetainedMemoryLimit` returns `limit - retained` if `0 <= retained < limit`, and zero otherwise. Its subtraction cannot overflow. |

The first law assumes `ErrMemoryBudget` is the nonnil, comparable sentinel
initialized by `errors.New`, without external reassignment. The proof trusts
the pinned verifier's `errors` model and the one `math.MaxInt64` declaration in
[`verification/gobra/stubs`](../../verification/gobra/stubs/math/limits.gobra).
It does not check cgroup discovery, how callers calculate the four operands, or
whether the estimate predicts peak memory or prevents OOM. Neither law verifies
the Go compiler, the kernel, or the rest of the package.

## Reproduce

```sh
task verify-cgroup-memory
task verify-cgroup-memory GOBRA_ENGINE=podman
go test -race ./internal/cgroup
```

The Linux amd64 OCI image pin in [`.config/tool-versions.env`](../../.config/tool-versions.env)
binds Gobra v26.09 ([source `d2c5af5`](https://github.com/viperproject/gobra/tree/d2c5af5450540c502855e53f4a5af1ba7630c41f)),
its Silicon backend, Ubuntu OpenJDK 21.0.12.1, and Z3 4.16.0. The same image
runs on a host with amd64 container support or emulation. It runs without
network or a checkout mount; only the two source inputs are streamed in.
No verifier is included in Microfat binaries.

Gobra v26.09's overflow pass aborts on the original inline slice literal in
the range loop. The independently reviewable Go refactor names the four
components as a fixed array. Native boundary tests pass on both forms.
To check that the laws catch regressions, run the same Task in disposable
copies: change the extraction guard from `>` to `>=` in one copy, and return
`limit` instead of `limit - retained` in another. The first must reject the
overly strict bound, the second must fail the retained-storage postcondition,
and the unchanged source must pass.

## Gate and rollback

The existing CI lint job requires the proof for every code-changing PR,
push, and manual run with code checks. Only the existing classification of
documentation-only PRs skips it. Release qualification also runs the same
Task before packaging. Missing tooling, failure, or timeout fails the gate.
Review changes to the contracts, trusted assumptions, or tool pin explicitly.
The proof integration can be reverted separately from the Go refactor, but
doing so requires review and no longer meets the v0.4.1 verification criterion
in [#478](https://github.com/EpicBlackWolfZ/microfat/issues/478).
