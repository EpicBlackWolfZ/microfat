//! Cross-language conformance suite validating `microfat_stub_core` against
//! Lean 4 mathematical ground truth vectors in `../../formal/conformance/vectors.json`.

use std::collections::BTreeMap;
use std::path::Path;

use crate::cgroup::{
    compute_gomemlimit, effective_memory_limit, extraction_memory, retained_memory_limit,
    zstd_decoder_memory,
};
use crate::elffilter::{transform_arm64, transform_x86_64};
use crate::format::{parse_and_select_v2_index, FormatError};
use crate::microarch::{
    amd64_level_str, evaluate_amd64_rank, evaluate_arm64_mask, highest_arm64_level_str,
    parse_normalized_level_rank, should_replace_variant_with_policy, Arm64Features, HostCaps,
    SelectionPolicy, TargetArch, X86Features, ARM_LVL_V8_0, ARM_LVL_V8_5, ARM_LVL_V9_0,
};
use crate::sha256::{encode_hex_lower, Sha256};

#[derive(Debug, Clone)]
enum Json {
    Null,
    Bool(bool),
    Num(u64),
    Str(String),
    Arr(Vec<Json>),
    Obj(BTreeMap<String, Json>),
}

impl Json {
    fn as_obj(&self) -> &BTreeMap<String, Json> {
        match self {
            Json::Obj(m) => m,
            _ => panic!("expected json object, got {self:?}"),
        }
    }

    fn as_arr(&self) -> &[Json] {
        match self {
            Json::Arr(a) => a,
            _ => panic!("expected json array, got {self:?}"),
        }
    }

    fn as_str(&self) -> &str {
        match self {
            Json::Str(s) => s,
            _ => panic!("expected json string, got {self:?}"),
        }
    }

    fn as_u64(&self) -> u64 {
        match self {
            Json::Num(n) => *n,
            _ => panic!("expected json number, got {self:?}"),
        }
    }

    fn as_opt_i64(&self) -> Option<i64> {
        match self {
            Json::Null => None,
            Json::Num(n) => i64::try_from(*n).ok(),
            _ => panic!("expected null or i64 number, got {self:?}"),
        }
    }

    fn as_opt_str(&self) -> Option<&str> {
        match self {
            Json::Null => None,
            Json::Str(s) => Some(s),
            _ => panic!("expected null or string, got {self:?}"),
        }
    }

    fn as_bool(&self) -> bool {
        match self {
            Json::Bool(b) => *b,
            _ => panic!("expected bool, got {self:?}"),
        }
    }
}

struct JsonParser<'a> {
    bytes: &'a [u8],
    pos: usize,
}

impl<'a> JsonParser<'a> {
    fn skip_ws(&mut self) {
        while self.pos < self.bytes.len() && self.bytes[self.pos].is_ascii_whitespace() {
            self.pos += 1;
        }
    }

    fn peek(&mut self) -> u8 {
        self.skip_ws();
        self.bytes[self.pos]
    }

    fn expect(&mut self, ch: u8) {
        self.skip_ws();
        assert_eq!(self.bytes[self.pos], ch, "at offset {}", self.pos);
        self.pos += 1;
    }

    fn parse_value(&mut self) -> Json {
        match self.peek() {
            b'{' => self.parse_obj(),
            b'[' => self.parse_arr(),
            b'"' => Json::Str(self.parse_string()),
            b't' => {
                assert_eq!(&self.bytes[self.pos..self.pos + 4], b"true");
                self.pos += 4;
                Json::Bool(true)
            }
            b'f' => {
                assert_eq!(&self.bytes[self.pos..self.pos + 5], b"false");
                self.pos += 5;
                Json::Bool(false)
            }
            b'n' => {
                assert_eq!(&self.bytes[self.pos..self.pos + 4], b"null");
                self.pos += 4;
                Json::Null
            }
            b'0'..=b'9' => Json::Num(self.parse_u64()),
            other => panic!("unexpected byte {} at {}", other as char, self.pos),
        }
    }

    fn parse_string(&mut self) -> String {
        self.expect(b'"');
        let start = self.pos;
        while self.bytes[self.pos] != b'"' {
            self.pos += 1;
        }
        let s = core::str::from_utf8(&self.bytes[start..self.pos])
            .unwrap()
            .to_owned();
        self.pos += 1;
        s
    }

    fn parse_u64(&mut self) -> u64 {
        let start = self.pos;
        while self.pos < self.bytes.len() && self.bytes[self.pos].is_ascii_digit() {
            self.pos += 1;
        }
        core::str::from_utf8(&self.bytes[start..self.pos])
            .unwrap()
            .parse()
            .unwrap()
    }

    fn parse_arr(&mut self) -> Json {
        self.expect(b'[');
        let mut items = Vec::new();
        if self.peek() == b']' {
            self.pos += 1;
            return Json::Arr(items);
        }
        loop {
            items.push(self.parse_value());
            if self.peek() == b',' {
                self.pos += 1;
            } else {
                self.expect(b']');
                break;
            }
        }
        Json::Arr(items)
    }

    fn parse_obj(&mut self) -> Json {
        self.expect(b'{');
        let mut map = BTreeMap::new();
        if self.peek() == b'}' {
            self.pos += 1;
            return Json::Obj(map);
        }
        loop {
            let key = self.parse_string();
            self.expect(b':');
            let val = self.parse_value();
            map.insert(key, val);
            if self.peek() == b',' {
                self.pos += 1;
            } else {
                self.expect(b'}');
                break;
            }
        }
        Json::Obj(map)
    }
}

fn load_json_file(filename: &str) -> BTreeMap<String, Json> {
    let path = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("..")
        .join("..")
        .join("..")
        .join("formal")
        .join("conformance")
        .join(filename);
    let raw = std::fs::read(&path).expect("read conformance json");
    let mut parser = JsonParser {
        bytes: &raw,
        pos: 0,
    };
    match parser.parse_value() {
        Json::Obj(map) => map,
        _ => panic!("conformance root must be json object"),
    }
}

fn load_vectors() -> BTreeMap<String, Json> {
    load_json_file("vectors.json")
}

fn decode_hex(s: &str) -> Vec<u8> {
    assert_eq!(s.len() % 2, 0);
    let mut out = Vec::with_capacity(s.len() / 2);
    let bytes = s.as_bytes();
    for i in (0..bytes.len()).step_by(2) {
        let pair = core::str::from_utf8(&bytes[i..i + 2]).unwrap();
        out.push(u8::from_str_radix(pair, 16).unwrap());
    }
    out
}

#[test]
fn lean_conformance_format_v2() {
    let root = load_vectors();
    for entry in root["format_v2"].as_arr() {
        let obj = entry.as_obj();
        let name = obj["name"].as_str();
        let index_offset = obj["index_offset"].as_u64();
        let wire = decode_hex(obj["wire_hex"].as_str());
        let valid_index = obj["valid_index"].as_bool();
        let bounds_valid = obj["bounds_valid"].as_bool();

        let (expected_arch, host): (&[u8], HostCaps) = if name.contains("arm64") {
            (
                b"arm64",
                HostCaps::Arm64(ARM_LVL_V8_0 | ARM_LVL_V8_5 | ARM_LVL_V9_0),
            )
        } else {
            (b"amd64", HostCaps::Amd64(4))
        };

        let res = parse_and_select_v2_index(&wire, index_offset, expected_arch, host);
        if valid_index && bounds_valid {
            assert!(
                res.is_ok(),
                "{name}: expected valid index+bounds, got {res:?}"
            );
        } else {
            assert!(
                matches!(
                    res,
                    Err(FormatError::InvalidMagic
                        | FormatError::TrailingBytes
                        | FormatError::OverlappingSegments
                        | FormatError::InvalidDict
                        | FormatError::InvalidVariantBounds)
                ),
                "{name}: expected rejection, got {res:?}"
            );
        }
    }
}

#[test]
fn lean_conformance_microarch() {
    let root = load_vectors();

    for entry in root["microarch_amd64"].as_arr() {
        let obj = entry.as_obj();
        let name = obj["name"].as_str();
        let mask = obj["mask"].as_u64() as u32;
        let expected_level = obj["level"].as_str();
        let expected_rank = obj["rank"].as_u64() as u8;

        let rank = evaluate_amd64_rank(X86Features::new(mask));
        assert_eq!(rank, expected_rank, "amd64 rank mismatch for {name}");
        assert_eq!(
            amd64_level_str(rank),
            Some(expected_level),
            "amd64 level mismatch for {name}"
        );
    }

    for entry in root["microarch_arm64"].as_arr() {
        let obj = entry.as_obj();
        let name = obj["name"].as_str();
        let mask = obj["mask"].as_u64() as u32;
        let expected_supported = obj["supported_mask"].as_u64() as u16;
        let expected_highest = obj["highest_level"].as_str();

        let got_mask = evaluate_arm64_mask(Arm64Features::new(mask));
        assert_eq!(
            got_mask, expected_supported,
            "arm64 mask mismatch for {name}"
        );
        assert_eq!(
            highest_arm64_level_str(got_mask),
            expected_highest,
            "arm64 highest mismatch for {name}"
        );
    }

    for entry in root["microarch_selection"].as_arr() {
        let obj = entry.as_obj();
        let name = obj["name"].as_str();
        let arch = match obj["arch"].as_str() {
            "amd64" => TargetArch::Amd64,
            "arm64" => TargetArch::Arm64,
            other => panic!("unknown arch {other}"),
        };
        let host = match arch {
            TargetArch::Amd64 => HostCaps::Amd64(evaluate_amd64_rank(X86Features::new(
                obj["x86_mask"].as_u64() as u32,
            ))),
            TargetArch::Arm64 => HostCaps::Arm64(evaluate_arm64_mask(Arm64Features::new(
                obj["arm64_mask"].as_u64() as u32,
            ))),
        };
        let mut policy = SelectionPolicy::default_none();
        policy.force_rank = obj["force_level"]
            .as_opt_str()
            .map(|s| parse_normalized_level_rank(arch, s.as_bytes()));
        policy.max_rank = obj["max_level"]
            .as_opt_str()
            .map(|s| parse_normalized_level_rank(arch, s.as_bytes()));
        for dis in obj["disabled"].as_arr() {
            policy.push_disabled(parse_normalized_level_rank(arch, dis.as_str().as_bytes()));
        }

        let mut best: Option<(u8, String)> = None;
        for avail in obj["available"].as_arr() {
            let lbl = avail.as_str();
            let rank = parse_normalized_level_rank(arch, lbl.as_bytes());
            if should_replace_variant_with_policy(best.as_ref().map(|b| b.0), rank, host, &policy) {
                best = Some((rank, lbl.to_owned()));
            }
        }
        let got_lbl = best.map(|b| b.1);
        let expected_lbl = obj["expected_selected"].as_opt_str().map(str::to_owned);
        assert_eq!(got_lbl, expected_lbl, "selection mismatch for {name}");
    }
}

#[test]
fn lean_conformance_cgroup_math() {
    let root = load_vectors();

    for entry in root["cgroup_retained_memory"].as_arr() {
        let obj = entry.as_obj();
        let lim = obj["limit"].as_u64() as i64;
        let ret = obj["retained"].as_u64() as i64;
        let exp = obj["expected"].as_u64() as i64;
        assert_eq!(retained_memory_limit(lim, ret), exp);
    }

    for entry in root["cgroup_extraction_memory"].as_arr() {
        let obj = entry.as_obj();
        let p = obj["payload"].as_u64() as i64;
        let d = obj["dictionary"].as_u64() as i64;
        let dec = obj["decoder"].as_u64() as i64;
        let res = obj["reserve"].as_u64() as i64;
        let exp = obj["expected"].as_opt_i64();
        assert_eq!(extraction_memory(p, d, dec, res), exp);
    }

    for entry in root["cgroup_effective_memory"].as_arr() {
        let obj = entry.as_obj();
        let max_b = obj["max_bytes"].as_opt_i64();
        let high_b = obj["high_bytes"].as_opt_i64();
        let exp = obj["expected"].as_opt_i64();
        assert_eq!(effective_memory_limit(max_b, high_b), exp);
    }

    for entry in root["cgroup_gomemlimit"].as_arr() {
        let obj = entry.as_obj();
        let lim = obj["limit"].as_u64() as i64;
        let ratio_bps = obj["ratio_bps"].as_u64();
        let min_headroom = obj["min_headroom"].as_u64() as i64;
        let exp = obj["expected"].as_u64() as i64;
        assert_eq!(compute_gomemlimit(lim, ratio_bps, min_headroom), Some(exp));
    }

    for entry in root["cgroup_zstd_decoder_memory"].as_arr() {
        let obj = entry.as_obj();
        let win = obj["window"].as_u64() as i64;
        let max_win = obj["max_window"].as_u64() as i64;
        let ovh = obj["overhead"].as_u64() as i64;
        let max_dec = obj["max_decoder"].as_u64() as i64;
        let exp = obj["expected"].as_opt_i64();
        assert_eq!(zstd_decoder_memory(win, max_win, ovh, max_dec), exp);
    }
}

#[test]
fn lean_conformance_elf_filter() {
    let root = load_vectors();
    for entry in root["elf_filter"].as_arr() {
        let obj = entry.as_obj();
        let name = obj["name"].as_str();
        let arch = obj["arch"].as_str();
        let base_pos = obj["base_pos"].as_u64() as u32;
        let input = decode_hex(obj["input_hex"].as_str());
        let expected_encoded = decode_hex(obj["encoded_hex"].as_str());

        let mut work = input.clone();
        match arch {
            "x86_64" => {
                transform_x86_64(&mut work, base_pos, true);
                assert_eq!(work, expected_encoded, "{name} x86 encode mismatch");
                transform_x86_64(&mut work, base_pos, false);
                assert_eq!(work, input, "{name} x86 roundtrip mismatch");
            }
            "arm64" => {
                transform_arm64(&mut work, base_pos, true);
                assert_eq!(work, expected_encoded, "{name} arm64 encode mismatch");
                transform_arm64(&mut work, base_pos, false);
                assert_eq!(work, input, "{name} arm64 roundtrip mismatch");
            }
            other => panic!("unexpected elf arch {other}"),
        }
    }
}

#[test]
fn nist_cavp_sha256_conformance() {
    let root = load_json_file("sha256_cavp.json");
    let chunk_steps = [1usize, 3, 7, 13, 31, 63, 64, 65, 129];
    let verify_streaming = |name: &str, msg: &[u8], want_hex: &str| {
        let oneshot = encode_hex_lower(&Sha256::digest(msg));
        assert_eq!(
            core::str::from_utf8(&oneshot).unwrap(),
            want_hex,
            "{name} oneshot mismatch"
        );

        let mut h = Sha256::new();
        let mut off = 0usize;
        let mut step_idx = 0usize;
        while off < msg.len() {
            let take = core::cmp::min(chunk_steps[step_idx % chunk_steps.len()], msg.len() - off);
            h.update(&msg[off..off + take]);
            off += take;
            step_idx += 1;
        }
        let streamed = encode_hex_lower(&h.finalize());
        assert_eq!(
            core::str::from_utf8(&streamed).unwrap(),
            want_hex,
            "{name} streaming mismatch"
        );
    };

    for section in ["fips_180_4_vectors", "cavp_boundary_vectors"] {
        for entry in root[section].as_arr() {
            let obj = entry.as_obj();
            let name = obj["name"].as_str();
            let msg = decode_hex(obj["msg_hex"].as_str());
            let want = obj["sha256"].as_str();
            verify_streaming(name, &msg, want);
        }
    }

    let million = root["million_a_vector"].as_obj();
    let fill = decode_hex(million["byte_hex"].as_str());
    assert_eq!(fill.len(), 1);
    let payload = vec![fill[0]; million["count"].as_u64() as usize];
    verify_streaming(
        million["name"].as_str(),
        &payload,
        million["sha256"].as_str(),
    );

    let monte = root["shavs_monte_carlo"].as_obj();
    let seed_vec = decode_hex(monte["seed_hex"].as_str());
    let mut cur_seed: [u8; 32] = seed_vec.try_into().expect("seed must be 32 bytes");
    let rounds = monte["rounds"].as_u64();
    let inner = monte["inner_iterations"].as_u64();
    let mut checkpoints = BTreeMap::new();
    for cp in monte["checkpoints"].as_arr() {
        let obj = cp.as_obj();
        checkpoints.insert(obj["round"].as_u64(), obj["sha256"].as_str());
    }
    let mut verified = 0usize;
    let mut buf = [0u8; 96];
    for round in 0..rounds {
        let mut md0 = cur_seed;
        let mut md1 = cur_seed;
        let mut md2 = cur_seed;
        for _ in 0..inner {
            buf[0..32].copy_from_slice(&md0);
            buf[32..64].copy_from_slice(&md1);
            buf[64..96].copy_from_slice(&md2);
            let next = Sha256::digest(&buf);
            md0 = md1;
            md1 = md2;
            md2 = next;
        }
        cur_seed = md2;
        if let Some(want) = checkpoints.get(&round) {
            let got = encode_hex_lower(&cur_seed);
            assert_eq!(
                core::str::from_utf8(&got).unwrap(),
                *want,
                "monte carlo round {round} mismatch"
            );
            verified += 1;
        }
    }
    assert_eq!(verified, checkpoints.len());
}
