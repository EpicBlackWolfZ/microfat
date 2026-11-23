//! Cgroup limit math and container resource parsers verified against `internal/cgroup`
//! and `formal/lean/Microfat/CgroupMath.lean`.

/// Minimum memory headroom reserved for non-heap allocations (64 MiB).
pub const DEFAULT_MIN_HEADROOM_BYTES: i64 = 64 * 1024 * 1024;

/// Cgroup v1 sentinel threshold (>= 1 PiB treated as unlimited).
pub const UNLIMITED_CGROUP_V1_THRESHOLD: i64 = 1024 * 1024 * 1024 * 1024 * 1024;

/// Default memory ratio in basis points (90% = 9000 / 10000).
pub const DEFAULT_MEMORY_RATIO_BPS: u64 = 9000;

/// Basis-point denominator (100% = 10000).
pub const RATIO_BPS_DENOMINATOR: u64 = 10_000;

/// Overflow-safe extraction memory budget estimate matching `internal/cgroup/memory.go`.
/// Succeeds iff all four operands are non-negative and their exact sum fits in `i64`.
pub fn extraction_memory(
    payload: i64,
    dictionary: i64,
    decoder: i64,
    reserve: i64,
) -> Option<i64> {
    if payload < 0 || dictionary < 0 || decoder < 0 || reserve < 0 {
        return None;
    }
    let s1 = payload.checked_add(dictionary)?;
    let s2 = s1.checked_add(decoder)?;
    s2.checked_add(reserve)
}

/// Deducts kernel-backed executable storage once from a raw ceiling without underflow.
/// Matches `internal/cgroup.RetainedMemoryLimit`.
pub const fn retained_memory_limit(limit: i64, retained: i64) -> i64 {
    if retained < 0 || retained >= limit {
        0
    } else {
        limit - retained
    }
}

/// Selects the tighter of `memory.max` and `memory.high`, or whichever is finite.
pub const fn effective_memory_limit(max_opt: Option<i64>, high_opt: Option<i64>) -> Option<i64> {
    match (max_opt, high_opt) {
        (Some(m), Some(h)) => Some(if h < m { h } else { m }),
        (Some(m), None) => Some(m),
        (None, Some(h)) => Some(h),
        (None, None) => None,
    }
}

/// Computes `GOMEMLIMIT` using the capped-headroom policy:
/// `min(floor(limit * ratio_bps / 10000), limit - min(min_headroom, limit - limit / 2))`.
/// Uses pure `u64` arithmetic (`limit < 2^50` and `ratio_bps <= 10000` guarantees zero overflow).
pub fn compute_gomemlimit(limit: i64, ratio_bps: u64, min_headroom: i64) -> Option<i64> {
    if limit <= 0 || limit >= UNLIMITED_CGROUP_V1_THRESHOLD {
        return None;
    }
    let r_bps = if ratio_bps == 0 || ratio_bps > RATIO_BPS_DENOMINATOR {
        DEFAULT_MEMORY_RATIO_BPS
    } else {
        ratio_bps
    };
    let headroom = if min_headroom <= 0 {
        DEFAULT_MIN_HEADROOM_BYTES
    } else {
        min_headroom
    };

    let ratio_budget = (((limit as u64) * r_bps) / RATIO_BPS_DENOMINATOR) as i64;
    let half = limit / 2;
    let limit_minus_half = limit - half;
    let reserved = if headroom < limit_minus_half {
        headroom
    } else {
        limit_minus_half
    };
    let headroom_budget = limit - reserved;

    let budget = if ratio_budget < headroom_budget {
        ratio_budget
    } else {
        headroom_budget
    };
    if budget <= 0 {
        None
    } else {
        Some(budget)
    }
}

/// Computes `GOMAXPROCS` as floor(`quota_us / period_us`), clamped to `>= 1`.
pub fn compute_gomaxprocs(quota_us: i64, period_us: i64) -> Option<u32> {
    if quota_us <= 0 || period_us <= 0 {
        return None;
    }
    let raw = quota_us / period_us;
    Some(raw.clamp(1, u32::MAX as i64) as u32)
}

const fn trim_ascii_ws(mut s: &[u8]) -> &[u8] {
    while let [first, rest @ ..] = s {
        if matches!(*first, b' ' | b'\t' | b'\r' | b'\n') {
            s = rest;
        } else {
            break;
        }
    }
    while let [rest @ .., last] = s {
        if matches!(*last, b' ' | b'\t' | b'\r' | b'\n') {
            s = rest;
        } else {
            break;
        }
    }
    s
}

/// Parses an unsigned decimal ASCII integer cleanly without panics.
pub fn parse_u64_ascii(raw: &[u8]) -> Option<u64> {
    let trimmed = trim_ascii_ws(raw);
    if trimmed.is_empty() {
        return None;
    }
    let mut acc = 0u64;
    for &b in trimmed {
        if !b.is_ascii_digit() {
            return None;
        }
        acc = acc.checked_mul(10)?.checked_add((b - b'0') as u64)?;
    }
    Some(acc)
}

fn u64_to_i64(v: u64) -> Option<i64> {
    if v > i64::MAX as u64 {
        None
    } else {
        Some(v as i64)
    }
}

/// Parses cgroup v2 `memory.max` / `memory.high` contents (`"max"` -> `Some(None)`).
pub fn parse_cgroup_v2_memory_bytes(raw: &[u8]) -> Option<Option<i64>> {
    let trimmed = trim_ascii_ws(raw);
    if trimmed.is_empty() {
        return None;
    }
    if trimmed == b"max" {
        return Some(None);
    }
    let val = u64_to_i64(parse_u64_ascii(trimmed)?)?;
    Some(Some(val))
}

/// Parses cgroup v2 `cpu.max` (`"<quota> <period>"` or `"max <period>"`).
pub fn parse_cgroup_v2_cpu_max(raw: &[u8]) -> Option<Option<(i64, i64)>> {
    let trimmed = trim_ascii_ws(raw);
    if trimmed.is_empty() {
        return None;
    }
    let mut split_at = None;
    for (i, &b) in trimmed.iter().enumerate() {
        if matches!(b, b' ' | b'\t') {
            split_at = Some(i);
            break;
        }
    }
    let sep = split_at?;
    let quota_tok = &trimmed[..sep];
    let period_tok = trim_ascii_ws(&trimmed[sep + 1..]);
    if period_tok.is_empty() {
        return None;
    }
    for &b in period_tok {
        if matches!(b, b' ' | b'\t') {
            return None;
        }
    }
    let period = u64_to_i64(parse_u64_ascii(period_tok)?)?;
    if period <= 0 {
        return None;
    }
    if quota_tok == b"max" {
        return Some(None);
    }
    let quota = u64_to_i64(parse_u64_ascii(quota_tok)?)?;
    if quota <= 0 {
        return None;
    }
    Some(Some((quota, period)))
}

/// Parses cgroup v1 `memory.limit_in_bytes` (`"-1"` or `>= 1 PiB` -> `Some(None)`).
pub fn parse_cgroup_v1_memory_bytes(raw: &[u8]) -> Option<Option<i64>> {
    let trimmed = trim_ascii_ws(raw);
    if trimmed.is_empty() {
        return None;
    }
    if trimmed == b"-1" {
        return Some(None);
    }
    let val = u64_to_i64(parse_u64_ascii(trimmed)?)?;
    if val <= 0 {
        return None;
    }
    if val >= UNLIMITED_CGROUP_V1_THRESHOLD {
        return Some(None);
    }
    Some(Some(val))
}

/// Parses cgroup v1 `cpu.cfs_quota_us` and `cpu.cfs_period_us`.
pub fn parse_cgroup_v1_cpu_quota_and_period(
    quota_raw: &[u8],
    period_raw: &[u8],
) -> Option<Option<(i64, i64)>> {
    let period = u64_to_i64(parse_u64_ascii(period_raw)?)?;
    if period <= 0 {
        return None;
    }
    let q_trim = trim_ascii_ws(quota_raw);
    if q_trim == b"-1" {
        return Some(None);
    }
    let quota = u64_to_i64(parse_u64_ascii(q_trim)?)?;
    if quota <= 0 {
        return None;
    }
    Some(Some((quota, period)))
}

/// Parses `MICROFAT_MEM_RATIO` decimal string in `(0.0, 1.0]` into basis points (`1..=10000`).
pub fn parse_ratio_bps(raw: &[u8]) -> Option<u64> {
    let s = trim_ascii_ws(raw);
    if s.is_empty() {
        return None;
    }
    let mut int_part = 0u64;
    let mut frac_bps = 0u64;
    let mut frac_scale = 1000u64;
    let mut seen_dot = false;
    let mut seen_digit = false;

    for &b in s {
        if b == b'.' {
            if seen_dot {
                return None;
            }
            seen_dot = true;
            continue;
        }
        if !b.is_ascii_digit() {
            return None;
        }
        seen_digit = true;
        let d = (b - b'0') as u64;
        if !seen_dot {
            int_part = int_part.checked_mul(10)?.checked_add(d)?;
            if int_part > 1 {
                return None;
            }
        } else if frac_scale > 0 {
            frac_bps += d * frac_scale;
            frac_scale /= 10;
        }
    }

    if !seen_digit {
        return None;
    }
    let total = int_part.checked_mul(RATIO_BPS_DENOMINATOR)?.checked_add(frac_bps)?;
    if total == 0 || total > RATIO_BPS_DENOMINATOR {
        return None;
    }
    Some(total)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn extraction_memory_checks_bounds_and_overflow() {
        assert_eq!(extraction_memory(10, 20, 30, 40), Some(100));
        assert_eq!(extraction_memory(-1, 0, 0, 0), None);
        assert_eq!(extraction_memory(i64::MAX, 1, 0, 0), None);
        assert_eq!(extraction_memory(i64::MAX, 0, 0, 0), Some(i64::MAX));
    }

    #[test]
    fn retained_and_effective_limits_match_go() {
        assert_eq!(retained_memory_limit(1000, 300), 700);
        assert_eq!(retained_memory_limit(1000, 1000), 0);
        assert_eq!(retained_memory_limit(1000, -1), 0);
        assert_eq!(effective_memory_limit(Some(500), Some(300)), Some(300));
        assert_eq!(effective_memory_limit(Some(500), None), Some(500));
        assert_eq!(effective_memory_limit(None, Some(300)), Some(300));
        assert_eq!(effective_memory_limit(None, None), None);
    }

    #[test]
    fn gomemlimit_respects_capped_headroom_and_ratios() {
        // 512 MiB: 90% = 460.8 MiB vs 512 - 64 = 448 MiB -> min = 448 MiB.
        let m512 = 512 * 1024 * 1024;
        assert_eq!(
            compute_gomemlimit(m512, DEFAULT_MEMORY_RATIO_BPS, DEFAULT_MIN_HEADROOM_BYTES),
            Some(m512 - DEFAULT_MIN_HEADROOM_BYTES)
        );
        // 64 MiB small container: reserve capped at ceil(limit/2) = 32 MiB -> budget = 32 MiB.
        let m64 = 64 * 1024 * 1024;
        assert_eq!(
            compute_gomemlimit(m64, DEFAULT_MEMORY_RATIO_BPS, DEFAULT_MIN_HEADROOM_BYTES),
            Some(m64 / 2)
        );
        // 2 GiB: 90% = 1.8 GiB < 2 GiB - 64 MiB -> ratio wins.
        let g2 = 2i64 * 1024 * 1024 * 1024;
        assert_eq!(
            compute_gomemlimit(g2, DEFAULT_MEMORY_RATIO_BPS, DEFAULT_MIN_HEADROOM_BYTES),
            Some((g2 * 9) / 10)
        );
        assert_eq!(
            compute_gomemlimit(
                UNLIMITED_CGROUP_V1_THRESHOLD,
                DEFAULT_MEMORY_RATIO_BPS,
                DEFAULT_MIN_HEADROOM_BYTES
            ),
            None
        );
    }

    #[test]
    fn gomemlimit_is_monotone_across_container_sizes() {
        let sizes_mib = [
            16i64, 32, 64, 96, 128, 192, 256, 384, 512, 768, 1024, 2048, 4096, 8192, 65536,
        ];
        let ratios = [5000u64, 8000, 9000, 10_000];
        let headrooms = [16 * 1024 * 1024, DEFAULT_MIN_HEADROOM_BYTES, 128 * 1024 * 1024];
        for &r in &ratios {
            for &h in &headrooms {
                let mut prev = 0i64;
                for &mib in &sizes_mib {
                    let cur = compute_gomemlimit(mib * 1024 * 1024, r, h).unwrap();
                    assert!(cur >= prev, "non-monotone at {mib} MiB ratio={r} headroom={h}");
                    prev = cur;
                }
            }
        }
    }

    #[test]
    fn parsers_cover_v1_v2_and_ratios() {
        assert_eq!(parse_cgroup_v2_memory_bytes(b"max\n"), Some(None));
        assert_eq!(parse_cgroup_v2_memory_bytes(b"268435456\n"), Some(Some(268_435_456)));
        assert_eq!(parse_cgroup_v2_memory_bytes(b"0"), Some(Some(0)));
        assert_eq!(parse_cgroup_v2_memory_bytes(b"bad"), None);

        assert_eq!(parse_cgroup_v2_cpu_max(b"max 100000\n"), Some(None));
        assert_eq!(parse_cgroup_v2_cpu_max(b"250000 100000\n"), Some(Some((250_000, 100_000))));
        assert_eq!(parse_cgroup_v2_cpu_max(b"0 100000"), None);
        assert_eq!(compute_gomaxprocs(250_000, 100_000), Some(2));
        assert_eq!(compute_gomaxprocs(50_000, 100_000), Some(1));

        assert_eq!(parse_cgroup_v1_memory_bytes(b"-1\n"), Some(None));
        assert_eq!(parse_cgroup_v1_memory_bytes(b"536870912\n"), Some(Some(536_870_912)));
        assert_eq!(parse_cgroup_v1_memory_bytes(b"0\n"), None);
        assert_eq!(parse_cgroup_v1_cpu_quota_and_period(b"-1", b"100000"), Some(None));
        assert_eq!(parse_cgroup_v1_cpu_quota_and_period(b"350000", b"100000"), Some(Some((350_000, 100_000))));

        assert_eq!(parse_ratio_bps(b"0.9"), Some(9000));
        assert_eq!(parse_ratio_bps(b"0.85"), Some(8500));
        assert_eq!(parse_ratio_bps(b"1.0"), Some(10_000));
        assert_eq!(parse_ratio_bps(b".5"), Some(5000));
        assert_eq!(parse_ratio_bps(b"0"), None);
        assert_eq!(parse_ratio_bps(b"1.01"), None);
    }
}

#[cfg(kani)]
mod kani_proofs {
    use super::*;

    #[kani::proof]
    fn verify_extraction_memory_matches_math_spec() {
        let p: i64 = kani::any();
        let d: i64 = kani::any();
        let dec: i64 = kani::any();
        let res: i64 = kani::any();

        let got = extraction_memory(p, d, dec, res);
        let exact = (p as i128) + (d as i128) + (dec as i128) + (res as i128);
        let fits = p >= 0 && d >= 0 && dec >= 0 && res >= 0 && exact <= (i64::MAX as i128);
        assert_eq!(got.is_some(), fits);
        if let Some(v) = got {
            assert_eq!(v as i128, exact);
        }
    }

    #[kani::proof]
    fn verify_retained_memory_limit_exact() {
        let limit: i64 = kani::any();
        let retained: i64 = kani::any();

        let rem = retained_memory_limit(limit, retained);
        if retained >= 0 && retained < limit {
            assert_eq!(rem as i128, (limit as i128) - (retained as i128));
            assert!(rem > 0 && rem <= limit);
        } else {
            assert_eq!(rem, 0);
        }
    }

    #[kani::proof]
    fn verify_gomemlimit_no_overflow_and_bounds() {
        let limit: i64 = kani::any();
        let min_headroom: i64 = kani::any();
        let idx: usize = kani::any();
        kani::assume(idx < 6);
        let ratio_bps = [0u64, 5000, 8000, 9000, 10_000, 10_001][idx];
        let eff_headroom = if min_headroom <= 0 {
            DEFAULT_MIN_HEADROOM_BYTES
        } else {
            min_headroom
        };

        let got = compute_gomemlimit(limit, ratio_bps, min_headroom);
        if limit <= 0 || limit >= UNLIMITED_CGROUP_V1_THRESHOLD {
            assert_eq!(got, None);
        }
        if let Some(b) = got {
            assert!(b > 0 && b <= limit);
            assert!(limit - b >= eff_headroom.min(limit - limit / 2));
        }
    }

    #[kani::proof]
    fn verify_gomaxprocs_safe_and_positive() {
        let q_any: i64 = kani::any();
        let p_any: i64 = kani::any();
        if q_any <= 0 || p_any <= 0 {
            assert_eq!(compute_gomaxprocs(q_any, p_any), None);
        }

        // Standard Linux CFS period (100ms) across arbitrary positive quotas up to 4e9 us.
        let quota_u32: u32 = kani::any();
        kani::assume(quota_u32 >= 1);
        let quota = quota_u32 as i64;
        let period = 100_000i64;
        let cpus = compute_gomaxprocs(quota, period).unwrap();
        assert!(cpus >= 1);
        if quota < period {
            assert_eq!(cpus, 1);
        } else {
            assert!((cpus as i128) * (period as i128) <= quota as i128);
            assert!(((cpus as i128) + 1) * (period as i128) > quota as i128);
        }
    }
}
