# Frozen held-out confirmation

The completed demo/microfat pilot is retained as `../evidence/pilot-report.json.gz`.
Its uncompressed SHA-256 is
`4f8f5b8fd86343abf3436581c1e756d799f1171a4c7a90231e9c9ad9397a5c9b`.
It ran 144/144 episodes in 9.28 seconds, with no failures or timeouts. All 240
never-prewarm launches used memfd; all 240 prewarmed launches used cache. Every
launch matched the frozen payload identity and native stdout.

Before any held-out timing, retain the pilot's **20 ms foreground episode SLO**,
10 percentage point practical risk margin, N=100 per arm/scenario, six-comparison
family and alpha=.05. The held-out applications are actionlint and Fortio,
stripped v1/v2/v3 payloads in zstd/format2 bundles with the current full stub;
selected v3 alone is prewarmed. Each command has a 2-second timeout. The campaign
has an 18-minute cap, keeping pilot plus confirmation timing below 20 minutes.
Each scenario has exactly 100 independent-unit *attempts* per arm. The unit is a
complete episode, including its dependent concurrent launches.

Primary inference remains descriptive: **IID=false** on this shared desktop.
The pilot already shows why both outcomes matter: prewarm improved microfat's
foreground SLO misses while increasing deployment-to-first-ready and one-off CPU
cost. Do not suppress a lifecycle penalty because foreground latency improves.

Prespecified secondary sensitivity: after the fixed confirmation completes,
`--mode iid-sensitivity --request report.json` computes the unchanged #377 exact
binomial/Bonferroni bounds using the **same** SLO, N, margin and family, under a
hypothetical IID/stationarity assumption. The output is explicitly CONDITIONAL
IID SENSITIVITY ONLY, retains the original IID=false and observed report digest,
and cannot establish those assumptions or replace the primary descriptive result.
No alternative SLO, subgroup, stopping rule or additional candidate will be tried.

The one-off schedule has one launch at0; recurring-2x3 has three launches at0 and
three at100 ms; disappearing-1x3 has three at0 with no later demand. Logical disk
retention horizons are 60 seconds, 1 second and 60 seconds respectively. They are
modeled charges, not waits or measured page residency. Auto dispatch has fresh
private cache per episode. Both arms emit identical dispatch logging, whose cost
is inside the timer. Full artifact SHA verification reads the bundles before the
campaign; preparation also reads inputs. Kernel page caches are consequently
likely warm and are not reset. This is not physical-storage-cold startup evidence.

No prewarm/foreground overlap claim is part of the primary protocol. If a later
separate descriptive cohort is worthwhile it must not alter these six comparisons.
