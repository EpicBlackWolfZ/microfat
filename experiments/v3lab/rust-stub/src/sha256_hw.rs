//! Runtime-dispatched x86-64 SHA-NI accelerated SHA-256 with pure-Rust core fallback.

use microfat_stub_core::sha256::Sha256;

#[cfg(target_arch = "x86_64")]
const IV: [u32; 8] = [
    0x6a09_e667,
    0xbb67_ae85,
    0x3c6e_f372,
    0xa54f_f53a,
    0x510e_527f,
    0x9b05_688c,
    0x1f83_d9ab,
    0x5be0_cd19,
];

#[cfg(target_arch = "x86_64")]
const K: [u32; 64] = [
    0x428a_2f98,
    0x7137_4491,
    0xb5c0_fbcf,
    0xe9b5_dba5,
    0x3956_c25b,
    0x59f1_11f1,
    0x923f_82a4,
    0xab1c_5ed5,
    0xd807_aa98,
    0x1283_5b01,
    0x2431_85be,
    0x550c_7dc3,
    0x72be_5d74,
    0x80de_b1fe,
    0x9bdc_06a7,
    0xc19b_f174,
    0xe49b_69c1,
    0xefbe_4786,
    0x0fc1_9dc6,
    0x240c_a1cc,
    0x2de9_2c6f,
    0x4a74_84aa,
    0x5cb0_a9dc,
    0x76f9_88da,
    0x983e_5152,
    0xa831_c66d,
    0xb003_27c8,
    0xbf59_7fc7,
    0xc6e0_0bf3,
    0xd5a7_9147,
    0x06ca_6351,
    0x1429_2967,
    0x27b7_0a85,
    0x2e1b_2138,
    0x4d2c_6dfc,
    0x5338_0d13,
    0x650a_7354,
    0x766a_0abb,
    0x81c2_c92e,
    0x9272_2c85,
    0xa2bf_e8a1,
    0xa81a_664b,
    0xc24b_8b70,
    0xc76c_51a3,
    0xd192_e819,
    0xd699_0624,
    0xf40e_3585,
    0x106a_a070,
    0x19a4_c116,
    0x1e37_6c08,
    0x2748_774c,
    0x34b0_bcb5,
    0x391c_0cb3,
    0x4ed8_aa4a,
    0x5b9c_ca4f,
    0x682e_6ff3,
    0x748f_82ee,
    0x78a5_636f,
    0x84c8_7814,
    0x8cc7_0208,
    0x90be_fffa,
    0xa450_6ceb,
    0xbef9_a3f7,
    0xc671_78f2,
];

#[cfg(target_arch = "x86_64")]
fn has_x86_sha_ni() -> bool {
    let r0 = core::arch::x86_64::__cpuid_count(0, 0);
    if r0.eax < 7 {
        return false;
    }
    let r1 = core::arch::x86_64::__cpuid_count(1, 0);
    let sse_ready =
        (r1.edx & (1 << 26)) != 0 && (r1.ecx & (1 << 9)) != 0 && (r1.ecx & (1 << 19)) != 0;
    if !sse_ready {
        return false;
    }
    let r7 = core::arch::x86_64::__cpuid_count(7, 0);
    (r7.ebx & (1 << 29)) != 0
}

#[cfg(target_arch = "x86_64")]
#[target_feature(enable = "sha,sse2,ssse3,sse4.1")]
unsafe fn compress_blocks_shani(state: &mut [u32; 8], blocks: &[u8]) {
    use core::arch::x86_64::{
        _mm_add_epi32, _mm_alignr_epi8, _mm_loadu_si128, _mm_set_epi32, _mm_set_epi8,
        _mm_sha256msg1_epu32, _mm_sha256msg2_epu32, _mm_sha256rnds2_epu32, _mm_shuffle_epi32,
        _mm_shuffle_epi8, _mm_storeu_si128,
    };

    macro_rules! step4 {
        ($state0:ident, $state1:ident, $msg:expr, $k_base:expr) => {{
            let k_vec = _mm_set_epi32(
                K[$k_base + 3] as i32,
                K[$k_base + 2] as i32,
                K[$k_base + 1] as i32,
                K[$k_base] as i32,
            );
            let wk = _mm_add_epi32($msg, k_vec);
            $state1 = _mm_sha256rnds2_epu32($state1, $state0, wk);
            let wk_hi = _mm_shuffle_epi32::<0x0E>(wk);
            $state0 = _mm_sha256rnds2_epu32($state0, $state1, wk_hi);
        }};
    }

    macro_rules! next_msg {
        ($w0:expr, $w1:expr, $w2:expr, $w3:expr) => {{
            let t = _mm_sha256msg1_epu32($w0, $w1);
            let t2 = _mm_add_epi32(t, _mm_alignr_epi8::<4>($w3, $w2));
            _mm_sha256msg2_epu32(t2, $w3)
        }};
    }

    let bswap = _mm_set_epi8(12, 13, 14, 15, 8, 9, 10, 11, 4, 5, 6, 7, 0, 1, 2, 3);
    let mut off = 0usize;
    while off + 64 <= blocks.len() {
        let ptr = blocks.as_ptr().add(off);
        let mut msg0 = _mm_shuffle_epi8(_mm_loadu_si128(ptr.cast()), bswap);
        let mut msg1 = _mm_shuffle_epi8(_mm_loadu_si128(ptr.add(16).cast()), bswap);
        let mut msg2 = _mm_shuffle_epi8(_mm_loadu_si128(ptr.add(32).cast()), bswap);
        let mut msg3 = _mm_shuffle_epi8(_mm_loadu_si128(ptr.add(48).cast()), bswap);

        let mut state0 = _mm_set_epi32(
            state[0] as i32,
            state[1] as i32,
            state[4] as i32,
            state[5] as i32,
        );
        let mut state1 = _mm_set_epi32(
            state[2] as i32,
            state[3] as i32,
            state[6] as i32,
            state[7] as i32,
        );

        step4!(state0, state1, msg0, 0);
        step4!(state0, state1, msg1, 4);
        step4!(state0, state1, msg2, 8);
        step4!(state0, state1, msg3, 12);

        let mut round = 16usize;
        while round < 64 {
            msg0 = next_msg!(msg0, msg1, msg2, msg3);
            step4!(state0, state1, msg0, round);

            msg1 = next_msg!(msg1, msg2, msg3, msg0);
            step4!(state0, state1, msg1, round + 4);

            msg2 = next_msg!(msg2, msg3, msg0, msg1);
            step4!(state0, state1, msg2, round + 8);

            msg3 = next_msg!(msg3, msg0, msg1, msg2);
            step4!(state0, state1, msg3, round + 12);

            round += 16;
        }

        let mut s0 = [0u32; 4];
        let mut s1 = [0u32; 4];
        _mm_storeu_si128(s0.as_mut_ptr().cast(), state0);
        _mm_storeu_si128(s1.as_mut_ptr().cast(), state1);

        state[0] = state[0].wrapping_add(s0[3]);
        state[1] = state[1].wrapping_add(s0[2]);
        state[2] = state[2].wrapping_add(s1[3]);
        state[3] = state[3].wrapping_add(s1[2]);
        state[4] = state[4].wrapping_add(s0[1]);
        state[5] = state[5].wrapping_add(s0[0]);
        state[6] = state[6].wrapping_add(s1[1]);
        state[7] = state[7].wrapping_add(s1[0]);

        off += 64;
    }
}

enum Engine {
    #[cfg(target_arch = "x86_64")]
    ShaNi {
        state: [u32; 8],
        total_bytes: u64,
        buf: [u8; 64],
        buf_len: usize,
    },
    Software(Sha256),
}

/// Streaming SHA-256 with automatic x86 SHA-NI runtime dispatch.
pub struct FastSha256(Engine);

impl FastSha256 {
    pub fn new() -> Self {
        #[cfg(target_arch = "x86_64")]
        if has_x86_sha_ni() {
            return Self(Engine::ShaNi {
                state: IV,
                total_bytes: 0,
                buf: [0u8; 64],
                buf_len: 0,
            });
        }
        Self(Engine::Software(Sha256::new()))
    }

    pub fn digest(data: &[u8]) -> [u8; 32] {
        let mut h = Self::new();
        h.update(data);
        h.finalize()
    }

    pub fn update(&mut self, data: &[u8]) {
        match &mut self.0 {
            #[cfg(target_arch = "x86_64")]
            Engine::ShaNi {
                state,
                total_bytes,
                buf,
                buf_len,
            } => {
                *total_bytes = total_bytes.wrapping_add(data.len() as u64);
                let mut rest = data;
                if *buf_len > 0 {
                    let take = core::cmp::min(64 - *buf_len, rest.len());
                    buf[*buf_len..*buf_len + take].copy_from_slice(&rest[..take]);
                    *buf_len += take;
                    rest = &rest[take..];
                    if *buf_len == 64 {
                        // SAFETY: Constructor verified CPUID SHA+SSE2+SSSE3+SSE4.1 before choosing ShaNi.
                        unsafe { compress_blocks_shani(state, buf) };
                        *buf_len = 0;
                    }
                }
                let full_len = rest.len() & !63;
                if full_len > 0 {
                    // SAFETY: Constructor verified CPUID SHA+SSE2+SSSE3+SSE4.1 before choosing ShaNi.
                    unsafe { compress_blocks_shani(state, &rest[..full_len]) };
                    rest = &rest[full_len..];
                }
                if !rest.is_empty() {
                    buf[..rest.len()].copy_from_slice(rest);
                    *buf_len = rest.len();
                }
            }
            Engine::Software(inner) => inner.update(data),
        }
    }

    pub fn finalize(self) -> [u8; 32] {
        match self.0 {
            #[cfg(target_arch = "x86_64")]
            Engine::ShaNi {
                mut state,
                total_bytes,
                mut buf,
                mut buf_len,
            } => {
                let bit_len = total_bytes.wrapping_mul(8);
                buf[buf_len] = 0x80;
                buf_len += 1;
                if buf_len > 56 {
                    buf[buf_len..64].fill(0);
                    // SAFETY: Constructor verified CPUID SHA+SSE2+SSSE3+SSE4.1 before choosing ShaNi.
                    unsafe { compress_blocks_shani(&mut state, &buf) };
                    buf_len = 0;
                }
                buf[buf_len..56].fill(0);
                buf[56..64].copy_from_slice(&bit_len.to_be_bytes());
                // SAFETY: Constructor verified CPUID SHA+SSE2+SSSE3+SSE4.1 before choosing ShaNi.
                unsafe { compress_blocks_shani(&mut state, &buf) };

                let mut out = [0u8; 32];
                for (i, w) in state.iter().enumerate() {
                    out[i * 4..(i + 1) * 4].copy_from_slice(&w.to_be_bytes());
                }
                out
            }
            Engine::Software(inner) => inner.finalize(),
        }
    }
}

impl Default for FastSha256 {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use microfat_stub_core::sha256::{decode_hex_digest, encode_hex_lower};
    use std::path::Path;

    fn unhex_bytes(hex: &str) -> Vec<u8> {
        hex.as_bytes()
            .as_chunks::<2>()
            .0
            .iter()
            .map(|c| u8::from_str_radix(core::str::from_utf8(c).unwrap(), 16).unwrap())
            .collect()
    }

    fn extract_quoted_field<'a>(block: &'a str, key: &str) -> Option<&'a str> {
        let needle = format!("\"{key}\": \"");
        let start = block.find(&needle)? + needle.len();
        let end = start + block[start..].find('"')?;
        Some(&block[start..end])
    }

    fn verify_hw_streaming(msg: &[u8], expected_hex: &str) {
        let expected = decode_hex_digest(expected_hex.as_bytes()).expect("valid sha256 hex");
        assert_eq!(FastSha256::digest(msg), expected);

        let steps = [1usize, 3, 7, 13, 31, 63, 64, 65, 129];
        let mut h = FastSha256::new();
        let mut off = 0usize;
        let mut idx = 0usize;
        while off < msg.len() {
            let take = core::cmp::min(steps[idx % steps.len()], msg.len() - off);
            h.update(&msg[off..off + take]);
            off += take;
            idx += 1;
        }
        assert_eq!(h.finalize(), expected);
    }

    #[test]
    fn nist_cavp_sha256_hw_conformance() {
        let path = Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("..")
            .join("..")
            .join("..")
            .join("formal")
            .join("conformance")
            .join("sha256_cavp.json");
        let raw = std::fs::read_to_string(path).expect("read sha256_cavp.json");
        let (vectors_part, rest) = raw
            .split_once("\"million_a_vector\"")
            .expect("million_a_vector section");
        let mut vector_count = 0usize;
        for chunk in vectors_part.split('{').skip(1) {
            if let (Some(msg_hex), Some(want)) = (
                extract_quoted_field(chunk, "msg_hex"),
                extract_quoted_field(chunk, "sha256"),
            ) {
                let msg = unhex_bytes(msg_hex);
                verify_hw_streaming(&msg, want);
                vector_count += 1;
            }
        }
        assert!(vector_count >= 25, "expected FIPS 180-4 + CAVP vectors");

        let (million_part, monte_part) = rest
            .split_once("\"shavs_monte_carlo\"")
            .expect("shavs_monte_carlo section");
        let million_sha = extract_quoted_field(million_part, "sha256").expect("million sha256");
        verify_hw_streaming(&vec![b'a'; 1_000_000], million_sha);

        let seed_hex = extract_quoted_field(monte_part, "seed_hex").expect("seed_hex");
        let mut cur_seed = decode_hex_digest(seed_hex.as_bytes()).expect("32-byte seed");
        let mut checkpoints = std::collections::BTreeMap::new();
        for chunk in monte_part.split('{').skip(1) {
            if let (Some(round_pos), Some(sha)) = (
                chunk.find("\"round\":"),
                extract_quoted_field(chunk, "sha256"),
            ) {
                let after = chunk[round_pos + 8..].trim_start();
                let digits_len = after
                    .find(|c: char| !c.is_ascii_digit())
                    .unwrap_or(after.len());
                let round: usize = after[..digits_len].parse().expect("round integer");
                checkpoints.insert(round, sha.to_string());
            }
        }
        assert!(!checkpoints.is_empty());

        let mut verified = 0usize;
        let mut buf = [0u8; 96];
        for round in 0..100usize {
            let mut md0 = cur_seed;
            let mut md1 = cur_seed;
            let mut md2 = cur_seed;
            for _ in 0..1000usize {
                buf[0..32].copy_from_slice(&md0);
                buf[32..64].copy_from_slice(&md1);
                buf[64..96].copy_from_slice(&md2);
                let next = FastSha256::digest(&buf);
                md0 = md1;
                md1 = md2;
                md2 = next;
            }
            cur_seed = md2;
            if let Some(want) = checkpoints.get(&round) {
                let got = encode_hex_lower(&cur_seed);
                assert_eq!(
                    core::str::from_utf8(&got).unwrap(),
                    want.as_str(),
                    "hw monte carlo round {round} mismatch"
                );
                verified += 1;
            }
        }
        assert_eq!(verified, checkpoints.len());
    }

    #[test]
    fn differential_against_core_sha256() {
        assert_eq!(FastSha256::digest(b""), Sha256::digest(b""));
        assert_eq!(FastSha256::digest(b"abc"), Sha256::digest(b"abc"));

        let mut payload = vec![0u8; 1024 * 1024 + 137];
        let mut seed = 0x9E37_79B9_7F4A_7C15u64;
        for b in &mut payload {
            seed = seed.wrapping_mul(6364136223846793005).wrapping_add(1);
            *b = (seed >> 24) as u8;
        }

        for len in 0..=257 {
            assert_eq!(
                FastSha256::digest(&payload[..len]),
                Sha256::digest(&payload[..len]),
                "mismatch at length {len}"
            );
        }

        // Streaming misaligned chunk boundaries over 1 MiB+.
        let mut fast = FastSha256::new();
        let mut slow = Sha256::new();
        let mut off = 0usize;
        for step in [1usize, 7, 63, 64, 65, 191, 4093, 32771]
            .into_iter()
            .cycle()
        {
            if off >= payload.len() {
                break;
            }
            let take = core::cmp::min(step, payload.len() - off);
            fast.update(&payload[off..off + take]);
            slow.update(&payload[off..off + take]);
            off += take;
        }
        assert_eq!(fast.finalize(), slow.finalize());
    }
}
