## Summary

<!-- Provide a clear, brief summary of what this pull request changes and why. -->

## Changes

<!-- List individual changes or bullet points. -->
- 

## Governance & Traceability

- [ ] Updated or added an ADR in [`docs/adr/`](../docs/adr/README.md) (if architectural decision or policy changed)
- [ ] Updated or added a normative SPEC in [`docs/specs/`](../docs/specs/README.md) (`REQ-*` / `LAW-*` IDs) and [`docs/verification/README.md`](../docs/verification/README.md) (if requirements or formal invariants changed)
- [ ] Captured red-to-green TDD evidence for behavioral changes

## Verification & Testing

<!-- Detail the automated or manual tests performed. -->
- [ ] `NO_COLOR=1 task lint-docs` executed cleanly (Markdown links, ADRs, SPECs, verification ledger, code-to-doc drift)
- [ ] `task all` executed cleanly (tidy, lint, vuln, test, coverage >= 95%, build)
- [ ] Added unit tests covering new code paths
- [ ] Race detection passed (`go test -race ./...`)

## Checklist

- [ ] My PR title adheres to [Conventional Commits](https://www.conventionalcommits.org/) (e.g., `feat:`, `fix:`, `docs:`, `chore:`)
- [ ] All commits are clean and follow repository conventions
- [ ] Documentation updated (if applicable)
