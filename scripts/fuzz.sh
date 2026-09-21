#!/usr/bin/env bash
# Preserve the full bounded fuzz target set in a sequential, fail-fast invocation.
set -euo pipefail
IFS=$'\n\t'
GO="${GO:-go}"
FUZZTIME="${FUZZTIME:-5s}"
while IFS=' ' read -r package target; do
    "${GO}" test "-fuzz=^${target}$" "-fuzztime=${FUZZTIME}" "./internal/${package}"
done <<'TARGETS'
format FuzzUnmarshalBinaryIndex
format FuzzUnmarshalJSONIndex
format FuzzLegacyJSONSyntax
format FuzzReadTrailerAndIndex
codec FuzzDecompressZstd
codec FuzzDecompressLZ4
cgroup FuzzCalculateGOMEMLIMIT
cgroup FuzzCalculateGOMAXPROCS
cgroup FuzzParseGCProfile
cgroup FuzzResolveTuningPlan
microarch FuzzVariantLevelParsing
pack FuzzValidateELFBinary
pack FuzzVerifyBinary
TARGETS
