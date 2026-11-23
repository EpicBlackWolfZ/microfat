//! Format v2 EOF trailer and compact binary index parser matching `internal/format/format.go`.

use crate::microarch::{
    amd64_level_str, is_compatible_level, parse_level_rank, should_replace_variant_with_policy,
    HostCaps, SelectionPolicy, TargetArch,
};
use crate::sha256::{decode_hex_digest, Sha256};

pub const TRAILER_MAGIC: &[u8; 8] = b"\x00\xFA\x7FMICRO";
pub const TRAILER_SIZE: usize = 56;
pub const INDEX_MAGIC_V2: &[u8; 4] = b"\x00\xFAM2";
pub const FORMAT_VERSION_2: u16 = 2;

pub const MAX_INDEX_SIZE: u64 = 1024 * 1024;
pub const MAX_DICTIONARY_SIZE: u64 = 1024 * 1024;
pub const MAX_PAYLOAD_SIZE: u64 = 1024 * 1024 * 1024;

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
pub enum Compression {
    None,
    Lz4,
    Zstd,
}

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
pub enum FormatError {
    TooSmall,
    InvalidMagic,
    InvalidIndexOffset,
    InvalidIndexSize,
    IndexChecksumMismatch,
    TruncatedIndex,
    UnsupportedVersion,
    TrailingBytes,
    InvalidDict,
    InvalidChecksum,
    InvalidVariantBounds,
    OverlappingSegments,
    UnsupportedCompression,
    ZeroVariants,
    ArchMismatch,
    NoCompatibleVariant,
}

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
pub struct TrailerInfo {
    pub index_offset: u64,
    pub index_size: usize,
    pub expected_index_sha256: [u8; 32],
}

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
pub struct DictMeta {
    pub offset: u64,
    pub size: u64,
    pub id: u32,
    pub sha256: [u8; 32],
}

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
pub struct SelectedVariant<'a> {
    pub level: &'a [u8],
    pub offset: u64,
    pub compressed_size: u64,
    pub uncompressed_size: u64,
    pub sha256: [u8; 32],
    pub compression: Compression,
    pub rank: u8,
}

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
pub struct ValidatedIndex<'a> {
    pub dict: Option<DictMeta>,
    pub selected: SelectedVariant<'a>,
    pub variant_count: usize,
}

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
pub struct SegmentSpan {
    pub start: u64,
    pub end: u64,
}

#[inline]
pub const fn spans_overlap(a: SegmentSpan, b: SegmentSpan) -> bool {
    a.start < b.end && b.start < a.end
}

/// Validates dictionary metadata bounds (`dict_off_i >= 0`, `0 <= dict_size_i <= MAX_DICTIONARY_SIZE`,
/// `dict_off + dict_size <= index_offset` when non-empty) without overflow.
pub fn validate_dict_bounds(
    dict_off_i: i64,
    dict_size_i: i64,
    index_offset: u64,
) -> Result<Option<SegmentSpan>, FormatError> {
    if dict_off_i < 0 || dict_size_i < 0 {
        return Err(FormatError::InvalidDict);
    }
    let dict_off = dict_off_i as u64;
    let dict_size = dict_size_i as u64;
    if dict_size > MAX_DICTIONARY_SIZE {
        return Err(FormatError::InvalidDict);
    }
    if dict_size == 0 {
        return Ok(None);
    }
    let d_end = dict_off
        .checked_add(dict_size)
        .ok_or(FormatError::InvalidDict)?;
    if d_end > index_offset {
        return Err(FormatError::InvalidDict);
    }
    Ok(Some(SegmentSpan {
        start: dict_off,
        end: d_end,
    }))
}

/// Validates one variant's byte bounds and checks disjointness against the optional dictionary
/// and all previously validated variant segments.
pub fn validate_variant_bounds(
    off_i: i64,
    comp_i: i64,
    uncomp_i: i64,
    index_offset: u64,
    dict_span: Option<SegmentSpan>,
    prev_spans: &[SegmentSpan],
) -> Result<SegmentSpan, FormatError> {
    if off_i < 0 || comp_i <= 0 || uncomp_i <= 0 {
        return Err(FormatError::InvalidVariantBounds);
    }
    let offset = off_i as u64;
    let compressed_size = comp_i as u64;
    let uncompressed_size = uncomp_i as u64;
    if uncompressed_size > MAX_PAYLOAD_SIZE {
        return Err(FormatError::InvalidVariantBounds);
    }
    let end = offset
        .checked_add(compressed_size)
        .ok_or(FormatError::InvalidVariantBounds)?;
    if end > index_offset {
        return Err(FormatError::InvalidVariantBounds);
    }
    let span = SegmentSpan { start: offset, end };
    if let Some(ds) = dict_span {
        if spans_overlap(span, ds) {
            return Err(FormatError::OverlappingSegments);
        }
    }
    for prev in prev_spans {
        if spans_overlap(span, *prev) {
            return Err(FormatError::OverlappingSegments);
        }
    }
    Ok(span)
}

/// Returns true iff `candidate_rank` is compatible with `host` and strictly better than `current_best`.
pub const fn should_replace_variant(
    current_best: Option<u8>,
    candidate_rank: u8,
    host: HostCaps,
) -> bool {
    if !is_compatible_level(host, candidate_rank) {
        return false;
    }
    match current_best {
        None => true,
        Some(b) => candidate_rank > b,
    }
}

/// Parses and validates the 56-byte Format v2 EOF trailer against file size.
pub fn parse_trailer(file_size: u64, trailer: &[u8; TRAILER_SIZE]) -> Result<TrailerInfo, FormatError> {
    if file_size < TRAILER_SIZE as u64 {
        return Err(FormatError::TooSmall);
    }
    if &trailer[48..56] != TRAILER_MAGIC {
        return Err(FormatError::InvalidMagic);
    }
    let index_offset = u64::from_le_bytes([
        trailer[0], trailer[1], trailer[2], trailer[3], trailer[4], trailer[5], trailer[6],
        trailer[7],
    ]);
    let index_size_u64 = u64::from_le_bytes([
        trailer[8], trailer[9], trailer[10], trailer[11], trailer[12], trailer[13], trailer[14],
        trailer[15],
    ]);
    if index_size_u64 == 0 || index_size_u64 > MAX_INDEX_SIZE {
        return Err(FormatError::InvalidIndexSize);
    }
    let trailer_off = file_size - TRAILER_SIZE as u64;
    if index_offset > trailer_off {
        return Err(FormatError::InvalidIndexOffset);
    }
    let end = index_offset
        .checked_add(index_size_u64)
        .ok_or(FormatError::InvalidIndexOffset)?;
    if end != trailer_off {
        return Err(FormatError::InvalidIndexOffset);
    }
    let mut hash = [0u8; 32];
    hash.copy_from_slice(&trailer[16..48]);
    Ok(TrailerInfo {
        index_offset,
        index_size: index_size_u64 as usize,
        expected_index_sha256: hash,
    })
}

/// Verifies index digest against trailer SHA-256 and delegates to full structural validation.
pub fn verify_and_select_v2<'a>(
    trailer: &TrailerInfo,
    index_bytes: &'a [u8],
    host: HostCaps,
) -> Result<ValidatedIndex<'a>, FormatError> {
    if index_bytes.len() != trailer.index_size {
        return Err(FormatError::InvalidIndexSize);
    }
    let actual = Sha256::digest(index_bytes);
    if actual != trailer.expected_index_sha256 {
        return Err(FormatError::IndexChecksumMismatch);
    }
    parse_and_select_v2_index(
        index_bytes,
        trailer.index_offset,
        host.expected_arch_bytes(),
        host,
    )
}

struct Cursor<'a> {
    data: &'a [u8],
    pos: usize,
}

impl<'a> Cursor<'a> {
    fn take(&mut self, n: usize) -> Result<&'a [u8], FormatError> {
        let end = self.pos.checked_add(n).ok_or(FormatError::TruncatedIndex)?;
        if end > self.data.len() {
            return Err(FormatError::TruncatedIndex);
        }
        let s = &self.data[self.pos..end];
        self.pos = end;
        Ok(s)
    }

    fn u8(&mut self) -> Result<u8, FormatError> {
        Ok(self.take(1)?[0])
    }

    fn u16_le(&mut self) -> Result<u16, FormatError> {
        let s = self.take(2)?;
        Ok(u16::from_le_bytes([s[0], s[1]]))
    }

    fn u32_le(&mut self) -> Result<u32, FormatError> {
        let s = self.take(4)?;
        Ok(u32::from_le_bytes([s[0], s[1], s[2], s[3]]))
    }

    fn i64_le(&mut self) -> Result<i64, FormatError> {
        let s = self.take(8)?;
        Ok(i64::from_le_bytes([
            s[0], s[1], s[2], s[3], s[4], s[5], s[6], s[7],
        ]))
    }
}

fn decode_one_variant<'a>(
    cur: &mut Cursor<'a>,
    index_offset: u64,
    dict_span: Option<SegmentSpan>,
    prev_spans: &[SegmentSpan],
    arch: TargetArch,
) -> Result<(SelectedVariant<'a>, SegmentSpan), FormatError> {
    let lvl_len = cur.u8()? as usize;
    let level = cur.take(lvl_len)?;
    if level.is_empty() {
        return Err(FormatError::InvalidVariantBounds);
    }
    let off_i = cur.i64_le()?;
    let comp_i = cur.i64_le()?;
    let uncomp_i = cur.i64_le()?;
    let span = validate_variant_bounds(off_i, comp_i, uncomp_i, index_offset, dict_span, prev_spans)?;
    let offset = off_i as u64;
    let compressed_size = comp_i as u64;
    let uncompressed_size = uncomp_i as u64;

    let sha_len = cur.u8()? as usize;
    let sha_ascii = cur.take(sha_len)?;
    let sha256 = decode_hex_digest(sha_ascii).ok_or(FormatError::InvalidChecksum)?;

    let comp_len = cur.u8()? as usize;
    let comp_bytes = cur.take(comp_len)?;
    let compression = match comp_bytes {
        b"" | b"zstd" => Compression::Zstd,
        b"lz4" => Compression::Lz4,
        b"none" => Compression::None,
        _ => return Err(FormatError::UnsupportedCompression),
    };

    let rank = parse_level_rank(arch, level);
    Ok((
        SelectedVariant {
            level,
            offset,
            compressed_size,
            uncompressed_size,
            sha256,
            compression,
            rank,
        },
        span,
    ))
}

/// Parses a Format v2 compact binary index, validates bounds & pairwise segment disjointness
/// without heap allocation, rejects trailing bytes, and selects the optimal compatible variant.
pub fn parse_and_select_v2_index<'a>(
    index_bytes: &'a [u8],
    index_offset: u64,
    expected_arch: &[u8],
    host: HostCaps,
) -> Result<ValidatedIndex<'a>, FormatError> {
    parse_and_select_v2_index_with_policy(
        index_bytes,
        index_offset,
        expected_arch,
        host,
        &SelectionPolicy::default_none(),
    )
}

/// Parses a Format v2 compact binary index and applies explicit `SelectionPolicy` overrides
/// while preserving strict host CPU compatibility (SIGILL-freedom) and segment disjointness.
pub fn parse_and_select_v2_index_with_policy<'a>(
    index_bytes: &'a [u8],
    index_offset: u64,
    expected_arch: &[u8],
    host: HostCaps,
    policy: &SelectionPolicy,
) -> Result<ValidatedIndex<'a>, FormatError> {
    match host {
        HostCaps::Amd64(r) if amd64_level_str(r).is_none() => {
            return Err(FormatError::NoCompatibleVariant);
        }
        HostCaps::Arm64(0) => return Err(FormatError::NoCompatibleVariant),
        _ => {}
    }
    let mut cur = Cursor {
        data: index_bytes,
        pos: 0,
    };
    if cur.take(4)? != INDEX_MAGIC_V2 {
        return Err(FormatError::InvalidMagic);
    }
    let version = cur.u16_le()?;
    if version != FORMAT_VERSION_2 {
        return Err(FormatError::UnsupportedVersion);
    }
    let _created_unix = cur.i64_le()?;
    let dict_off_i = cur.i64_le()?;
    let dict_size_i = cur.i64_le()?;
    let dict_id = cur.u32_le()?;

    let dict_span = validate_dict_bounds(dict_off_i, dict_size_i, index_offset)?;

    let dict_sha_len = cur.u8()? as usize;
    let dict_sha_ascii = cur.take(dict_sha_len)?;

    let dict = if let Some(ds) = dict_span {
        let d_hash = decode_hex_digest(dict_sha_ascii).ok_or(FormatError::InvalidChecksum)?;
        Some(DictMeta {
            offset: ds.start,
            size: ds.end - ds.start,
            id: dict_id,
            sha256: d_hash,
        })
    } else {
        None
    };

    let os_len = cur.u8()? as usize;
    let _target_os = cur.take(os_len)?;
    let arch_len = cur.u8()? as usize;
    let target_arch = cur.take(arch_len)?;
    if !expected_arch.is_empty() && target_arch != expected_arch {
        return Err(FormatError::ArchMismatch);
    }
    let app_len = cur.u16_le()? as usize;
    let _app_name = cur.take(app_len)?;
    let variant_count = cur.u16_le()? as usize;
    if variant_count == 0 {
        return Err(FormatError::ZeroVariants);
    }

    const FAST_SPANS: usize = 32;
    let mut stack_spans = [SegmentSpan { start: 0, end: 0 }; FAST_SPANS];
    let variants_start = cur.pos;
    let mut best: Option<SelectedVariant<'a>> = None;

    let arch = host.target_arch();
    for i in 0..variant_count {
        let check_slice = if i < FAST_SPANS { &stack_spans[..i] } else { &[] };
        let (cand, span) =
            decode_one_variant(&mut cur, index_offset, dict_span, check_slice, arch)?;
        if i < FAST_SPANS {
            stack_spans[i] = span;
        } else {
            // Re-scan prior entries without allocation when variant_count > 32.
            let mut scan = Cursor {
                data: index_bytes,
                pos: variants_start,
            };
            for _ in 0..i {
                let (_, prev_span) =
                    decode_one_variant(&mut scan, index_offset, dict_span, &[], arch)?;
                if spans_overlap(span, prev_span) {
                    return Err(FormatError::OverlappingSegments);
                }
            }
        }

        if should_replace_variant_with_policy(best.map(|b| b.rank), cand.rank, host, policy) {
            best = Some(cand);
        }
    }

    if cur.pos != index_bytes.len() {
        return Err(FormatError::TrailingBytes);
    }

    let selected = best.ok_or(FormatError::NoCompatibleVariant)?;
    Ok(ValidatedIndex {
        dict,
        selected,
        variant_count,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::microarch::{ARM_LVL_V8_0, ARM_LVL_V8_5, ARM_LVL_V9_0};
    use crate::sha256::encode_hex_lower;

    type TestVariantSpec<'a> = (&'a [u8], i64, i64, i64, &'a [u8]);

    fn build_valid_index(target_arch: &[u8], variants: &[TestVariantSpec<'_>]) -> Vec<u8> {
        let mut out = Vec::new();
        out.extend_from_slice(INDEX_MAGIC_V2);
        out.extend_from_slice(&FORMAT_VERSION_2.to_le_bytes());
        out.extend_from_slice(&(-42i64).to_le_bytes()); // negative CreatedUnix allowed
        out.extend_from_slice(&0i64.to_le_bytes());
        out.extend_from_slice(&0i64.to_le_bytes());
        out.extend_from_slice(&0u32.to_le_bytes());
        out.push(0); // dict sha len
        out.push(5);
        out.extend_from_slice(b"linux");
        out.push(target_arch.len() as u8);
        out.extend_from_slice(target_arch);
        out.extend_from_slice(&4u16.to_le_bytes());
        out.extend_from_slice(b"demo");
        out.extend_from_slice(&(variants.len() as u16).to_le_bytes());

        let fake_hash = encode_hex_lower(&Sha256::digest(b"payload"));
        for &(lvl, off, comp, uncomp, codec) in variants {
            out.push(lvl.len() as u8);
            out.extend_from_slice(lvl);
            out.extend_from_slice(&off.to_le_bytes());
            out.extend_from_slice(&comp.to_le_bytes());
            out.extend_from_slice(&uncomp.to_le_bytes());
            out.push(64);
            out.extend_from_slice(&fake_hash);
            out.push(codec.len() as u8);
            out.extend_from_slice(codec);
        }
        out
    }

    #[test]
    fn parses_valid_and_selects_highest_compatible() {
        let idx = build_valid_index(
            b"amd64",
            &[
                (b"v1", 1000, 100, 200, b"zstd"),
                (b"v2", 1100, 100, 200, b"lz4"),
                (b"v4", 1200, 100, 200, b"none"),
            ],
        );
        let mut trailer = [0u8; TRAILER_SIZE];
        trailer[0..8].copy_from_slice(&2000u64.to_le_bytes());
        trailer[8..16].copy_from_slice(&(idx.len() as u64).to_le_bytes());
        trailer[16..48].copy_from_slice(&Sha256::digest(&idx));
        trailer[48..56].copy_from_slice(TRAILER_MAGIC);

        let t = parse_trailer(2000 + idx.len() as u64 + TRAILER_SIZE as u64, &trailer).unwrap();
        let parsed = verify_and_select_v2(&t, &idx, HostCaps::Amd64(3)).unwrap();
        assert_eq!(parsed.selected.level, b"v2");
        assert_eq!(parsed.selected.compression, Compression::Lz4);
        assert_eq!(parsed.variant_count, 3);
    }

    #[test]
    fn parses_arm64_and_respects_non_linear_dag() {
        // Host supports v8.0, v8.5, and v9.0, but NOT v8.6 or v9.1.
        let host = HostCaps::Arm64(ARM_LVL_V8_0 | ARM_LVL_V8_5 | ARM_LVL_V9_0);
        let idx = build_valid_index(
            b"arm64",
            &[
                (b"v8.0", 1000, 100, 200, b"none"),
                (b"v8.6", 1100, 100, 200, b"zstd"),
                (b"v9.0", 1200, 100, 200, b"lz4"),
                (b"v9.1", 1300, 100, 200, b"zstd"),
            ],
        );
        let parsed = parse_and_select_v2_index(&idx, 2000, b"arm64", host).unwrap();
        assert_eq!(parsed.selected.level, b"v9.0");

        let idx_no_90 = build_valid_index(
            b"arm64",
            &[
                (b"v8.5", 1000, 100, 200, b"none"),
                (b"v8.6", 1100, 100, 200, b"zstd"),
                (b"v9.1", 1200, 100, 200, b"zstd"),
            ],
        );
        let parsed_fallback = parse_and_select_v2_index(&idx_no_90, 2000, b"arm64", host).unwrap();
        assert_eq!(parsed_fallback.selected.level, b"v8.5");
    }

    #[test]
    fn rejects_trailing_bytes_and_overlaps() {
        let mut idx = build_valid_index(
            b"amd64",
            &[
                (b"v1", 1000, 150, 200, b"zstd"),
                (b"v2", 1100, 100, 200, b"zstd"),
            ],
        );
        assert_eq!(
            parse_and_select_v2_index(&idx, 2000, b"amd64", HostCaps::Amd64(2)),
            Err(FormatError::OverlappingSegments)
        );

        idx = build_valid_index(b"amd64", &[(b"v1", 1000, 100, 200, b"")]);
        idx.push(0x99);
        assert_eq!(
            parse_and_select_v2_index(&idx, 2000, b"amd64", HostCaps::Amd64(2)),
            Err(FormatError::TrailingBytes)
        );
    }

    #[test]
    fn enforces_selection_policy_overrides() {
        let idx = build_valid_index(
            b"amd64",
            &[
                (b"v1", 1000, 100, 200, b"zstd"),
                (b"v2", 1100, 100, 200, b"zstd"),
                (b"v3", 1200, 100, 200, b"zstd"),
                (b"v4", 1300, 100, 200, b"zstd"),
            ],
        );
        let host = HostCaps::Amd64(3);

        let p_force = SelectionPolicy {
            force_rank: Some(1),
            ..SelectionPolicy::default_none()
        };
        assert_eq!(
            parse_and_select_v2_index_with_policy(&idx, 2000, b"amd64", host, &p_force)
                .unwrap()
                .selected
                .level,
            b"v1"
        );

        let p_force_unsafe = SelectionPolicy {
            force_rank: Some(4),
            ..SelectionPolicy::default_none()
        };
        assert_eq!(
            parse_and_select_v2_index_with_policy(&idx, 2000, b"amd64", host, &p_force_unsafe),
            Err(FormatError::NoCompatibleVariant)
        );

        let mut p_dis = SelectionPolicy {
            max_rank: Some(3),
            ..SelectionPolicy::default_none()
        };
        p_dis.push_disabled(3);
        assert_eq!(
            parse_and_select_v2_index_with_policy(&idx, 2000, b"amd64", host, &p_dis)
                .unwrap()
                .selected
                .level,
            b"v2"
        );
    }
}

#[cfg(kani)]
mod kani_proofs {
    use super::*;

    #[kani::proof]
    fn verify_trailer_never_panics_and_enforces_exact_layout() {
        let file_size: u64 = kani::any();
        let trailer: [u8; TRAILER_SIZE] = kani::any();
        if let Ok(info) = parse_trailer(file_size, &trailer) {
            assert!(file_size >= TRAILER_SIZE as u64);
            assert!(info.index_size >= 1 && info.index_size as u64 <= MAX_INDEX_SIZE);
            assert_eq!(
                info.index_offset + info.index_size as u64,
                file_size - TRAILER_SIZE as u64
            );
        }
    }

    #[kani::proof]
    #[kani::unwind(34)]
    fn verify_decode_hex_digest_no_panic() {
        let raw: [u8; 64] = kani::any();
        let len: usize = kani::any();
        kani::assume(len <= 64);
        let res = decode_hex_digest(&raw[..len]);
        assert_eq!(res.is_some(), len == 64 && res.is_some());
        if len != 64 {
            assert!(res.is_none());
        }
    }

    #[kani::proof]
    #[kani::unwind(5)]
    fn verify_cursor_primitives_safe() {
        let buf: [u8; 64] = kani::any();
        let len: usize = kani::any();
        let take_n: usize = kani::any();
        kani::assume(len <= 64);

        let mut cur = Cursor {
            data: &buf[..len],
            pos: 0,
        };
        let _ = cur.take(take_n);
        let _ = cur.u8();
        let _ = cur.u16_le();
        let _ = cur.u32_le();
        let _ = cur.i64_le();
        assert!(cur.pos <= len);
    }

    #[kani::proof]
    #[kani::unwind(6)]
    fn verify_segment_bounds_disjointness_and_selection() {
        let index_offset: u64 = kani::any();
        let dict_off: i64 = kani::any();
        let dict_size: i64 = kani::any();
        let is_arm64: bool = kani::any();
        let host = if is_arm64 {
            let mask: u16 = kani::any();
            HostCaps::Arm64(mask)
        } else {
            let host_rank: u8 = kani::any();
            kani::assume((1..=4).contains(&host_rank));
            HostCaps::Amd64(host_rank)
        };

        let dict_res = validate_dict_bounds(dict_off, dict_size, index_offset);
        if let Ok(dict_span) = dict_res {
            if let Some(ds) = dict_span {
                assert!(ds.start < ds.end && ds.end <= index_offset);
                assert!(ds.end - ds.start <= MAX_DICTIONARY_SIZE);
            }

            let count: usize = kani::any();
            kani::assume((1..=4).contains(&count));

            let mut spans = [SegmentSpan { start: 0, end: 0 }; 4];
            let mut ranks = [0u8; 4];
            let mut best_rank: Option<u8> = None;

            for i in 0..count {
                let off_i: i64 = kani::any();
                let comp_i: i64 = kani::any();
                let uncomp_i: i64 = kani::any();
                let cand_rank: u8 = kani::any();

                let span = match validate_variant_bounds(
                    off_i,
                    comp_i,
                    uncomp_i,
                    index_offset,
                    dict_span,
                    &spans[..i],
                ) {
                    Ok(s) => s,
                    Err(_) => return,
                };
                assert!(span.start < span.end && span.end <= index_offset);
                if let Some(ds) = dict_span {
                    assert!(!spans_overlap(span, ds));
                }
                for prev in &spans[..i] {
                    assert!(!spans_overlap(span, *prev));
                }
                spans[i] = span;
                ranks[i] = cand_rank;
                if should_replace_variant(best_rank, cand_rank, host) {
                    best_rank = Some(cand_rank);
                }
            }

            if let Some(chosen) = best_rank {
                assert!(is_compatible_level(host, chosen));
                for r in &ranks[..count] {
                    if is_compatible_level(host, *r) {
                        assert!(chosen >= *r);
                    }
                }
            } else {
                for r in &ranks[..count] {
                    assert!(!is_compatible_level(host, *r));
                }
            }
        }
    }
}
