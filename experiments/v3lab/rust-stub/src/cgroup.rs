//! Cgroup limit math and container resource parsers verified against `internal/cgroup`
//! and `formal/lean/Microfat/CgroupMath.lean`.

#[cfg(verus_keep_ghost)]
use vstd::prelude::*;

/// Minimum memory headroom reserved for non-heap allocations (64 MiB).
pub const DEFAULT_MIN_HEADROOM_BYTES: i64 = 64 * 1024 * 1024;

/// Maximum sliding window size allowed for the Zstd decoder (128 MiB).
pub const DEFAULT_ZSTD_MAX_WINDOW: i64 = 128 * 1024 * 1024;

/// Fixed Zstd decoder tables/buffers overhead (4 MiB).
pub const DEFAULT_ZSTD_OVERHEAD_BYTES: i64 = 4 * 1024 * 1024;

/// Upper bound on total Zstd decoder memory budget (512 MiB).
pub const DEFAULT_ZSTD_MAX_DECODER_MEMORY: i64 = 512 * 1024 * 1024;

/// Default container memory ratio for `memory_constrained` GC profile (80%).
pub const MEMORY_CONSTRAINED_RATIO_BPS: u64 = 8000;

/// Cgroup v1 sentinel threshold (>= 1 PiB treated as unlimited).
pub const UNLIMITED_CGROUP_V1_THRESHOLD: i64 = 1024 * 1024 * 1024 * 1024 * 1024;

/// Default memory ratio in basis points (90% = 9000 / 10000).
pub const DEFAULT_MEMORY_RATIO_BPS: u64 = 9000;

/// Basis-point denominator (100% = 10000).
pub const RATIO_BPS_DENOMINATOR: u64 = 10_000;

/// Overflow-safe extraction memory budget estimate matching `internal/cgroup/memory.go`.
/// Succeeds iff all four operands are non-negative and their exact sum fits in `i64`.
#[cfg_attr(verus_keep_ghost, verus_spec(
    result =>
        ensures
            match result {
                Some(total) =>
                    0 <= payload && 0 <= dictionary && 0 <= decoder && 0 <= reserve
                    && (payload as int) + (dictionary as int) + (decoder as int) + (reserve as int) <= i64::MAX as int
                    && total as int == (payload as int) + (dictionary as int) + (decoder as int) + (reserve as int),
                None =>
                    !(0 <= payload && 0 <= dictionary && 0 <= decoder && 0 <= reserve
                      && (payload as int) + (dictionary as int) + (decoder as int) + (reserve as int) <= i64::MAX as int),
            }
))]
pub fn extraction_memory(payload: i64, dictionary: i64, decoder: i64, reserve: i64) -> Option<i64> {
    if payload < 0 || dictionary < 0 || decoder < 0 || reserve < 0 {
        return None;
    }
    if dictionary > i64::MAX - payload {
        return None;
    }
    let s1 = payload + dictionary;
    if decoder > i64::MAX - s1 {
        return None;
    }
    let s2 = s1 + decoder;
    if reserve > i64::MAX - s2 {
        return None;
    }
    Some(s2 + reserve)
}

/// Overflow-safe Zstd decoder memory allowance matching `internal/cgroup.ZstdDecoderMemory`
/// and `formal/lean/Microfat/CgroupMath.lean` (`zstdDecoderMemory`).
/// Computes `2*window + overhead` when `0 < window <= max_window`, `overhead >= 0`,
/// `max_decoder > 0`, and `2*window + overhead <= max_decoder`.
#[cfg_attr(verus_keep_ghost, verus_spec(
    result =>
        ensures
            match result {
                Some(total) =>
                    0 < window && window <= max_window && 0 <= overhead && 0 < max_decoder
                    && 2 * (window as int) + (overhead as int) <= i64::MAX as int
                    && 2 * (window as int) + (overhead as int) <= max_decoder as int
                    && total as int == 2 * (window as int) + (overhead as int),
                None =>
                    !(0 < window && window <= max_window && 0 <= overhead && 0 < max_decoder
                      && 2 * (window as int) + (overhead as int) <= i64::MAX as int
                      && 2 * (window as int) + (overhead as int) <= max_decoder as int),
            }
))]
pub fn zstd_decoder_memory(
    window: i64,
    max_window: i64,
    overhead: i64,
    max_decoder: i64,
) -> Option<i64> {
    if window <= 0 || max_window <= 0 || overhead < 0 || max_decoder <= 0 || window > max_window {
        return None;
    }
    if window > max_decoder - window {
        return None;
    }
    let doubled = window + window;
    if overhead > max_decoder - doubled {
        return None;
    }
    Some(doubled + overhead)
}

/// Deducts kernel-backed executable storage once from a raw ceiling without underflow.
/// Matches `internal/cgroup.RetainedMemoryLimit`.
#[cfg_attr(verus_keep_ghost, verus_spec(
    result =>
        ensures
            0 <= retained && retained < limit ==> result as int == (limit as int) - (retained as int),
            retained < 0 || retained >= limit ==> result == 0,
))]
pub const fn retained_memory_limit(limit: i64, retained: i64) -> i64 {
    if retained < 0 || retained >= limit {
        0
    } else {
        limit - retained
    }
}

/// Selects the tighter of `memory.max` and `memory.high`, or whichever is finite.
#[cfg_attr(verus_keep_ghost, verus_spec(
    result =>
        ensures
            match result {
                Some(eff) =>
                    (match max_opt { Some(m) => eff <= m, None => true })
                    && (match high_opt { Some(h) => eff <= h, None => true })
                    && ((match max_opt { Some(m) => eff == m, None => false })
                        || (match high_opt { Some(h) => eff == h, None => false })),
                None => max_opt.is_none() && high_opt.is_none(),
            }
))]
pub const fn effective_memory_limit(max_opt: Option<i64>, high_opt: Option<i64>) -> Option<i64> {
    match (max_opt, high_opt) {
        (Some(m), Some(h)) => Some(if h < m { h } else { m }),
        (Some(m), None) => Some(m),
        (None, Some(h)) => Some(h),
        (None, None) => None,
    }
}

#[cfg(not(verus_keep_ghost))]
mod parsers_and_policy {
    use super::*;

    #[derive(Copy, Clone, Debug, Eq, PartialEq)]
    pub enum CpuPolicy {
        Static,
        Native,
    }

    pub fn resolve_cpu_policy(raw: Option<&[u8]>) -> CpuPolicy {
        match raw.map(trim_ascii_ws) {
            Some(v) if v.eq_ignore_ascii_case(b"native") => CpuPolicy::Native,
            _ => CpuPolicy::Static,
        }
    }

    #[derive(Copy, Clone, Debug, Eq, PartialEq)]
    pub enum GcProfile {
        Default,
        LatencyCritical,
        MemoryConstrained,
        BatchEtl,
        Adaptive,
    }

    impl GcProfile {
        pub const fn as_str(self) -> &'static str {
            match self {
                Self::Default => "default",
                Self::LatencyCritical => "latency_critical",
                Self::MemoryConstrained => "memory_constrained",
                Self::BatchEtl => "batch_etl",
                Self::Adaptive => "adaptive",
            }
        }
    }

    fn eq_norm_gc_token(s: &[u8], target: &[u8]) -> bool {
        if s.len() != target.len() {
            return false;
        }
        let mut i = 0;
        while i < s.len() {
            let a = if s[i] == b'-' {
                b'_'
            } else {
                s[i].to_ascii_lowercase()
            };
            if a != target[i] {
                return false;
            }
            i += 1;
        }
        true
    }

    pub fn parse_gc_profile(raw: Option<&[u8]>) -> GcProfile {
        let Some(s) = raw.map(trim_ascii_ws) else {
            return GcProfile::Default;
        };
        if s.is_empty() || eq_norm_gc_token(s, b"default") {
            GcProfile::Default
        } else if eq_norm_gc_token(s, b"latency_critical") || eq_norm_gc_token(s, b"latency") {
            GcProfile::LatencyCritical
        } else if eq_norm_gc_token(s, b"memory_constrained") || eq_norm_gc_token(s, b"memory") {
            GcProfile::MemoryConstrained
        } else if eq_norm_gc_token(s, b"batch_etl")
            || eq_norm_gc_token(s, b"batch")
            || eq_norm_gc_token(s, b"etl")
        {
            GcProfile::BatchEtl
        } else if eq_norm_gc_token(s, b"adaptive") || eq_norm_gc_token(s, b"dynamic") {
            GcProfile::Adaptive
        } else {
            GcProfile::Default
        }
    }

    pub const fn profile_default_ratio_bps(profile: GcProfile) -> u64 {
        match profile {
            GcProfile::MemoryConstrained => MEMORY_CONSTRAINED_RATIO_BPS,
            _ => DEFAULT_MEMORY_RATIO_BPS,
        }
    }

    #[derive(Copy, Clone, Debug, Eq, PartialEq)]
    pub enum GogcOutcome {
        Value(i64),
        Off,
        SkippedBatchNoLimit,
        Unset,
    }

    pub fn parse_byte_size_i64(raw: &[u8]) -> Option<i64> {
        let s = trim_ascii_ws(raw);
        if s.is_empty() {
            return None;
        }
        let mut i = 0usize;
        let mut digits = 0usize;
        let mut whole: u64 = 0;
        let mut seen_dot = false;
        while i < s.len() {
            let b = s[i];
            if b.is_ascii_digit() {
                digits += 1;
                if !seen_dot {
                    whole = whole.checked_mul(10)?.checked_add((b - b'0') as u64)?;
                }
                i += 1;
            } else if b == b'.' && !seen_dot {
                seen_dot = true;
                i += 1;
            } else {
                break;
            }
        }
        if digits == 0 {
            return None;
        }
        let unit = trim_ascii_ws(&s[i..]);
        let mult: u64 = if unit.is_empty()
            || unit.eq_ignore_ascii_case(b"b")
            || unit.eq_ignore_ascii_case(b"byte")
            || unit.eq_ignore_ascii_case(b"bytes")
        {
            1
        } else if unit.eq_ignore_ascii_case(b"k")
            || unit.eq_ignore_ascii_case(b"kb")
            || unit.eq_ignore_ascii_case(b"kib")
        {
            1024
        } else if unit.eq_ignore_ascii_case(b"m")
            || unit.eq_ignore_ascii_case(b"mb")
            || unit.eq_ignore_ascii_case(b"mib")
        {
            1024 * 1024
        } else if unit.eq_ignore_ascii_case(b"g")
            || unit.eq_ignore_ascii_case(b"gb")
            || unit.eq_ignore_ascii_case(b"gib")
        {
            1024 * 1024 * 1024
        } else if unit.eq_ignore_ascii_case(b"t")
            || unit.eq_ignore_ascii_case(b"tb")
            || unit.eq_ignore_ascii_case(b"tib")
        {
            1024 * 1024 * 1024 * 1024
        } else {
            return None;
        };
        let bytes = whole.checked_mul(mult)?;
        u64_to_i64(bytes)
    }

    pub fn parse_runtime_memory_limit(raw: &[u8]) -> Option<i64> {
        let s = trim_ascii_ws(raw);
        if s.is_empty() {
            return None;
        }
        let (digits, mult): (&[u8], u64) = if let Some(prefix) = s.strip_suffix(b"TiB") {
            (prefix, 1024 * 1024 * 1024 * 1024)
        } else if let Some(prefix) = s.strip_suffix(b"GiB") {
            (prefix, 1024 * 1024 * 1024)
        } else if let Some(prefix) = s.strip_suffix(b"MiB") {
            (prefix, 1024 * 1024)
        } else if let Some(prefix) = s.strip_suffix(b"KiB") {
            (prefix, 1024)
        } else if let Some(prefix) = s.strip_suffix(b"B") {
            if s.ends_with(b"iB") {
                return None;
            }
            (prefix, 1)
        } else {
            (s, 1)
        };
        if digits.is_empty() || digits.iter().any(|b| !b.is_ascii_digit()) {
            return None;
        }
        let val = parse_u64_ascii(digits)?;
        let prod = val.checked_mul(mult)?;
        if prod == 0 || prod >= i64::MAX as u64 {
            return None;
        }
        Some(prod as i64)
    }

    pub fn resolve_profile_gogc(
        profile: GcProfile,
        computed_gomemlimit: Option<i64>,
        effective_mem_limit_for_batch: Option<i64>,
        live_heap_bytes: Option<i64>,
    ) -> GogcOutcome {
        match profile {
            GcProfile::LatencyCritical => GogcOutcome::Value(75),
            GcProfile::MemoryConstrained => GogcOutcome::Value(40),
            GcProfile::BatchEtl => match effective_mem_limit_for_batch {
                Some(lim) if lim > 0 && lim < i64::MAX => GogcOutcome::Off,
                _ => GogcOutcome::SkippedBatchNoLimit,
            },
            GcProfile::Adaptive => match (computed_gomemlimit, live_heap_bytes) {
                (Some(avail), Some(live)) if avail > 0 && live > 0 => {
                    let gogc = (avail - live).saturating_mul(100) / live;
                    GogcOutcome::Value(gogc.clamp(10, 100))
                }
                _ => GogcOutcome::Unset,
            },
            GcProfile::Default => GogcOutcome::Unset,
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
        let total = int_part
            .checked_mul(RATIO_BPS_DENOMINATOR)?
            .checked_add(frac_bps)?;
        if total == 0 || total > RATIO_BPS_DENOMINATOR {
            return None;
        }
        Some(total)
    }
} // mod parsers_and_policy

#[cfg(not(verus_keep_ghost))]
pub use self::parsers_and_policy::*;

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
        let headrooms = [
            16 * 1024 * 1024,
            DEFAULT_MIN_HEADROOM_BYTES,
            128 * 1024 * 1024,
        ];
        for &r in &ratios {
            for &h in &headrooms {
                let mut prev = 0i64;
                for &mib in &sizes_mib {
                    let cur = compute_gomemlimit(mib * 1024 * 1024, r, h).unwrap();
                    assert!(
                        cur >= prev,
                        "non-monotone at {mib} MiB ratio={r} headroom={h}"
                    );
                    prev = cur;
                }
            }
        }
    }

    #[test]
    fn parsers_cover_v1_v2_and_ratios() {
        assert_eq!(parse_cgroup_v2_memory_bytes(b"max\n"), Some(None));
        assert_eq!(
            parse_cgroup_v2_memory_bytes(b"268435456\n"),
            Some(Some(268_435_456))
        );
        assert_eq!(parse_cgroup_v2_memory_bytes(b"0"), Some(Some(0)));
        assert_eq!(parse_cgroup_v2_memory_bytes(b"bad"), None);

        assert_eq!(parse_cgroup_v2_cpu_max(b"max 100000\n"), Some(None));
        assert_eq!(
            parse_cgroup_v2_cpu_max(b"250000 100000\n"),
            Some(Some((250_000, 100_000)))
        );
        assert_eq!(parse_cgroup_v2_cpu_max(b"0 100000"), None);
        assert_eq!(compute_gomaxprocs(250_000, 100_000), Some(2));
        assert_eq!(compute_gomaxprocs(50_000, 100_000), Some(1));

        assert_eq!(parse_cgroup_v1_memory_bytes(b"-1\n"), Some(None));
        assert_eq!(
            parse_cgroup_v1_memory_bytes(b"536870912\n"),
            Some(Some(536_870_912))
        );
        assert_eq!(parse_cgroup_v1_memory_bytes(b"0\n"), None);
        assert_eq!(
            parse_cgroup_v1_cpu_quota_and_period(b"-1", b"100000"),
            Some(None)
        );
        assert_eq!(
            parse_cgroup_v1_cpu_quota_and_period(b"350000", b"100000"),
            Some(Some((350_000, 100_000)))
        );

        assert_eq!(parse_ratio_bps(b"0.9"), Some(9000));
        assert_eq!(parse_ratio_bps(b"0.85"), Some(8500));
        assert_eq!(parse_ratio_bps(b"1.0"), Some(10_000));
        assert_eq!(parse_ratio_bps(b".5"), Some(5000));
        assert_eq!(parse_ratio_bps(b"0"), None);
        assert_eq!(parse_ratio_bps(b"1.01"), None);
    }

    #[test]
    fn zstd_decoder_memory_and_profiles_match_go() {
        assert_eq!(
            zstd_decoder_memory(
                8 * 1024 * 1024,
                DEFAULT_ZSTD_MAX_WINDOW,
                DEFAULT_ZSTD_OVERHEAD_BYTES,
                DEFAULT_ZSTD_MAX_DECODER_MEMORY
            ),
            Some(20 * 1024 * 1024)
        );
        assert_eq!(
            zstd_decoder_memory(
                DEFAULT_ZSTD_MAX_WINDOW + 1,
                DEFAULT_ZSTD_MAX_WINDOW,
                DEFAULT_ZSTD_OVERHEAD_BYTES,
                DEFAULT_ZSTD_MAX_DECODER_MEMORY
            ),
            None
        );
        assert_eq!(zstd_decoder_memory(256, 256, 0, 512), Some(512));
        assert_eq!(zstd_decoder_memory(256, 256, 1, 512), None);

        assert_eq!(resolve_cpu_policy(None), CpuPolicy::Static);
        assert_eq!(resolve_cpu_policy(Some(b" NATIVE ")), CpuPolicy::Native);
        assert_eq!(
            parse_gc_profile(Some(b"latency-critical")),
            GcProfile::LatencyCritical
        );
        assert_eq!(
            parse_gc_profile(Some(b"memory")),
            GcProfile::MemoryConstrained
        );
        assert_eq!(parse_gc_profile(Some(b"ETL")), GcProfile::BatchEtl);
        assert_eq!(parse_gc_profile(Some(b"dynamic")), GcProfile::Adaptive);
        assert_eq!(
            profile_default_ratio_bps(GcProfile::MemoryConstrained),
            8000
        );
        assert_eq!(parse_byte_size_i64(b"64MiB"), Some(64 * 1024 * 1024));
        assert_eq!(
            parse_runtime_memory_limit(b"384MiB"),
            Some(384 * 1024 * 1024)
        );
        assert_eq!(
            resolve_profile_gogc(GcProfile::LatencyCritical, None, None, None),
            GogcOutcome::Value(75)
        );
        assert_eq!(
            resolve_profile_gogc(GcProfile::BatchEtl, None, None, None),
            GogcOutcome::SkippedBatchNoLimit
        );
        assert_eq!(
            resolve_profile_gogc(GcProfile::BatchEtl, None, Some(384 * 1024 * 1024), None),
            GogcOutcome::Off
        );
        assert_eq!(
            resolve_profile_gogc(GcProfile::Adaptive, Some(300), Some(300), Some(100)),
            GogcOutcome::Value(100)
        );
        assert_eq!(
            resolve_profile_gogc(GcProfile::Adaptive, None, None, Some(100)),
            GogcOutcome::Unset
        );
        assert_eq!(
            resolve_profile_gogc(GcProfile::Default, None, None, None),
            GogcOutcome::Unset
        );
        assert_eq!(
            resolve_profile_gogc(GcProfile::MemoryConstrained, None, None, None),
            GogcOutcome::Value(40)
        );
    }

    #[test]
    fn covers_profile_strings_and_parser_error_branches() {
        assert_eq!(GcProfile::Default.as_str(), "default");
        assert_eq!(GcProfile::LatencyCritical.as_str(), "latency_critical");
        assert_eq!(GcProfile::MemoryConstrained.as_str(), "memory_constrained");
        assert_eq!(GcProfile::BatchEtl.as_str(), "batch_etl");
        assert_eq!(GcProfile::Adaptive.as_str(), "adaptive");
        assert_eq!(profile_default_ratio_bps(GcProfile::Default), 9000);
        assert_eq!(parse_gc_profile(None), GcProfile::Default);
        assert_eq!(parse_gc_profile(Some(b"")), GcProfile::Default);
        assert_eq!(parse_gc_profile(Some(b"unknown_prof")), GcProfile::Default);
        assert_eq!(parse_gc_profile(Some(b"latency_extra")), GcProfile::Default);

        assert_eq!(parse_byte_size_i64(b"   "), None);
        assert_eq!(parse_byte_size_i64(b"abc"), None);
        assert_eq!(parse_byte_size_i64(b"1.5MiB"), Some(1024 * 1024));
        assert_eq!(parse_byte_size_i64(b"4KiB"), Some(4096));
        assert_eq!(parse_byte_size_i64(b"2GiB"), Some(2 * 1024 * 1024 * 1024));
        assert_eq!(
            parse_byte_size_i64(b"1TiB"),
            Some(1024 * 1024 * 1024 * 1024)
        );
        assert_eq!(parse_byte_size_i64(b"10bytes"), Some(10));
        assert_eq!(parse_byte_size_i64(b"10PiB"), None);

        assert_eq!(parse_runtime_memory_limit(b"   "), None);
        assert_eq!(
            parse_runtime_memory_limit(b"1TiB"),
            Some(1024 * 1024 * 1024 * 1024)
        );
        assert_eq!(
            parse_runtime_memory_limit(b"2GiB"),
            Some(2 * 1024 * 1024 * 1024)
        );
        assert_eq!(parse_runtime_memory_limit(b"8KiB"), Some(8192));
        assert_eq!(parse_runtime_memory_limit(b"1024B"), Some(1024));
        assert_eq!(parse_runtime_memory_limit(b"1024"), Some(1024));
        assert_eq!(parse_runtime_memory_limit(b"1PiB"), None);
        assert_eq!(parse_runtime_memory_limit(b"0B"), None);
        assert_eq!(parse_runtime_memory_limit(b"12aB"), None);

        assert_eq!(
            compute_gomemlimit(100, 10_001, 0),
            compute_gomemlimit(100, DEFAULT_MEMORY_RATIO_BPS, DEFAULT_MIN_HEADROOM_BYTES)
        );
        assert_eq!(compute_gomemlimit(1, 5000, 1), None);
        assert_eq!(compute_gomaxprocs(0, 100_000), None);
        assert_eq!(compute_gomaxprocs(100_000, 0), None);

        assert_eq!(parse_u64_ascii(b"  "), None);
        assert_eq!(parse_cgroup_v2_memory_bytes(b"18446744073709551615"), None);
        assert_eq!(parse_cgroup_v2_memory_bytes(b" "), None);
        assert_eq!(parse_cgroup_v2_cpu_max(b" "), None);
        assert_eq!(parse_cgroup_v2_cpu_max(b"100 "), None);
        assert_eq!(parse_cgroup_v2_cpu_max(b"100 200 extra"), None);
        assert_eq!(parse_cgroup_v2_cpu_max(b"100 0"), None);
        assert_eq!(parse_cgroup_v1_memory_bytes(b" "), None);
        assert_eq!(
            parse_cgroup_v1_memory_bytes(b"1125899906842624"),
            Some(None)
        );
        assert_eq!(parse_cgroup_v1_cpu_quota_and_period(b"100", b"0"), None);
        assert_eq!(parse_cgroup_v1_cpu_quota_and_period(b"0", b"100"), None);

        assert_eq!(parse_ratio_bps(b"  "), None);
        assert_eq!(parse_ratio_bps(b"0.1.2"), None);
        assert_eq!(parse_ratio_bps(b"0.9a"), None);
        assert_eq!(parse_ratio_bps(b"2.0"), None);
        assert_eq!(parse_ratio_bps(b"0.12345"), Some(1234));
        assert_eq!(parse_ratio_bps(b"."), None);

        assert_eq!(extraction_memory(1, 1, i64::MAX - 1, 0), None);
        assert_eq!(extraction_memory(1, 1, 1, i64::MAX - 2), None);
        assert_eq!(zstd_decoder_memory(0, 100, 0, 500), None);
        assert_eq!(zstd_decoder_memory(1, 0, 0, 500), None);
        assert_eq!(zstd_decoder_memory(10, 10, -1, 500), None);
        assert_eq!(zstd_decoder_memory(10, 10, 0, 0), None);
        assert_eq!(zstd_decoder_memory(100, 100, 0, 150), None);
    }
}

#[cfg(kani)]
mod kani_proofs {
    use super::*;

    #[kani::proof]
    fn verify_zstd_decoder_memory_matches_math_spec() {
        let win: i64 = kani::any();
        let max_win: i64 = kani::any();
        let overhead: i64 = kani::any();
        let max_dec: i64 = kani::any();

        let got = zstd_decoder_memory(win, max_win, overhead, max_dec);
        let exact = (win as i128) + (win as i128) + (overhead as i128);
        let fits = win > 0
            && max_win > 0
            && overhead >= 0
            && max_dec > 0
            && win <= max_win
            && exact <= (max_dec as i128);
        assert_eq!(got.is_some(), fits);
        if let Some(v) = got {
            assert_eq!(v as i128, exact);
            assert!(v >= 0 && v <= max_dec);
        }
    }

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
