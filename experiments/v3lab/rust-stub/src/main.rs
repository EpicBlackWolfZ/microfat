//! Ultra-lean verified Format v2 launcher stub for static Linux x86-64 and ARM64 binaries.
//! Release binary compiles cleanly under `#![no_std]` + `alloc` with zero `libstd` unwinding/DWARF bloat.
#![cfg_attr(not(test), no_std)]
#![cfg_attr(not(test), no_main)]
#![cfg_attr(test, allow(dead_code))]

extern crate alloc;

#[cfg_attr(not(test), link(name = "c"))]
extern "C" {}

mod cpuid;
mod sha256_hw;

use alloc::vec;
use alloc::vec::Vec;
#[cfg(not(test))]
use core::alloc::{GlobalAlloc, Layout};
use core::cmp;
use core::hash::Hasher as _;
use core::ptr;

use cpuid::{detect_host_caps, is_skylake_x_or_cascade_lake};
use lz4_flex::block::{decompress_into, decompress_into_with_dict};
use microfat_stub_core::cgroup::{
    compute_gomaxprocs, compute_gomemlimit, effective_memory_limit, extraction_memory,
    parse_byte_size_i64, parse_cgroup_v1_cpu_quota_and_period, parse_cgroup_v1_memory_bytes,
    parse_cgroup_v2_cpu_max, parse_cgroup_v2_memory_bytes, parse_gc_profile, parse_ratio_bps,
    parse_runtime_memory_limit, profile_default_ratio_bps, resolve_cpu_policy,
    resolve_profile_gogc, retained_memory_limit, zstd_decoder_memory, CpuPolicy, GcProfile,
    GogcOutcome, DEFAULT_MIN_HEADROOM_BYTES, DEFAULT_ZSTD_MAX_DECODER_MEMORY,
    DEFAULT_ZSTD_MAX_WINDOW, DEFAULT_ZSTD_OVERHEAD_BYTES, UNLIMITED_CGROUP_V1_THRESHOLD,
};
use microfat_stub_core::format::{
    parse_and_select_v2_index_with_policy, parse_trailer, Compression, DictMeta, FormatError,
    SelectedVariant, TRAILER_SIZE,
};
use microfat_stub_core::microarch::{
    highest_arm64_level_str, is_compatible_level, parse_normalized_level_rank, HostCaps,
    SelectionPolicy,
};
#[cfg(test)]
use microfat_stub_core::microarch::TargetArch;
use sha256_hw::FastSha256;
use twox_hash::XxHash32;
use zrip_core::dict::Dictionary as ZstdDictionary;
use zrip_core::error::DecompressError as ZstdDecompressError;
use zrip_core::frame::header::parse_frame_header;
use zrip_core::frame::ZSTD_MAGIC;
use zrip_decode::push::{PushDecoder as ZstdPushDecoder, Status as ZstdStatus};

const AT_SECURE_KEY: usize = 23;
const IO_BUF_SIZE: usize = 256 * 1024;
const MAX_ZSTD_WINDOW: u64 = 128 * 1024 * 1024;
const MIN_ZSTD_WINDOW: u64 = 1024;
const ZSTD_SKIPPABLE_MASK: u32 = 0xFFFF_FFF0;
const ZSTD_SKIPPABLE_BASE: u32 = 0x184D_2A50;
const ZSTD_MAX_SKIPPABLE_FRAMES: usize = 8;
const NONE_COPY_ALLOWANCE: i64 = 128 * 1024;
const LZ4_DECODER_ALLOWANCE: i64 = 12 * 1024 * 1024 + 32 * 1024;
const ZSTD_FALLBACK_ALLOWANCE: i64 = 16 * 1024 * 1024 + 256 * 1024;
const MFD_EXEC: libc::c_uint = 0x0010;
const SEAL_MASK: libc::c_int =
    libc::F_SEAL_SEAL | libc::F_SEAL_SHRINK | libc::F_SEAL_GROW | libc::F_SEAL_WRITE;
const UNSAFE_CACHE_MODE_BITS: libc::mode_t =
    libc::S_IWGRP | libc::S_IWOTH | libc::S_ISUID | libc::S_ISGID | libc::S_ISVTX;

const LZ4F_MAGIC: u32 = 0x184D_2204;
const LZ4F_LEGACY_MAGIC: u32 = 0x184C_2102;
const LZ4_SKIPPABLE_MASK: u32 = 0xFFFF_FFF0;
const LZ4_SKIPPABLE_BASE: u32 = 0x184D_2A50;
const LZ4_DICT_MAX: usize = 64 * 1024;

#[cfg(not(test))]
struct LibcAllocator;

#[cfg(not(test))]
unsafe impl GlobalAlloc for LibcAllocator {
    unsafe fn alloc(&self, layout: Layout) -> *mut u8 {
        if layout.size() == 0 {
            return ptr::NonNull::<u8>::dangling().as_ptr();
        }
        if layout.align() <= 16 {
            // SAFETY: libc::malloc satisfies alignment <= max_align_t on Linux musl x86_64/aarch64.
            return unsafe { libc::malloc(layout.size()).cast::<u8>() };
        }
        let mut p: *mut libc::c_void = ptr::null_mut();
        let align = layout.align().max(core::mem::size_of::<usize>());
        // SAFETY: `p` is stack-allocated and `align` is a power of two >= pointer size.
        if unsafe { libc::posix_memalign(&mut p, align, layout.size()) } != 0 {
            return ptr::null_mut();
        }
        p.cast::<u8>()
    }

    unsafe fn alloc_zeroed(&self, layout: Layout) -> *mut u8 {
        if layout.size() == 0 {
            return ptr::NonNull::<u8>::dangling().as_ptr();
        }
        if layout.align() <= 16 {
            // SAFETY: `libc::calloc` returns zeroed storage aligned to at least 16 bytes.
            return unsafe { libc::calloc(1, layout.size()).cast::<u8>() };
        }
        // SAFETY: `self.alloc` returns a valid `layout.size()` block when non-null.
        let p = unsafe { self.alloc(layout) };
        if !p.is_null() {
            // SAFETY: `p` is valid for `layout.size()` writable bytes.
            unsafe { ptr::write_bytes(p, 0, layout.size()) };
        }
        p
    }

    unsafe fn dealloc(&self, ptr: *mut u8, layout: Layout) {
        if layout.size() == 0 || ptr.is_null() {
            return;
        }
        // SAFETY: `ptr` came from `malloc`/`calloc`/`posix_memalign`.
        unsafe { libc::free(ptr.cast()) };
    }

    unsafe fn realloc(&self, ptr: *mut u8, layout: Layout, new_size: usize) -> *mut u8 {
        if layout.align() <= 16 {
            // SAFETY: standard `libc::realloc` contract preserves prefix bytes up to min(old, new).
            return unsafe { libc::realloc(ptr.cast(), new_size).cast::<u8>() };
        }
        let new_layout = match Layout::from_size_align(new_size, layout.align()) {
            Ok(l) => l,
            Err(_) => return ptr::null_mut(),
        };
        // SAFETY: delegates to `self.alloc`, copies min size, and frees old block.
        let new_ptr = unsafe { self.alloc(new_layout) };
        if !new_ptr.is_null() {
            let copy_len = cmp::min(layout.size(), new_size);
            // SAFETY: non-overlapping newly allocated block and live old block.
            unsafe {
                ptr::copy_nonoverlapping(ptr, new_ptr, copy_len);
                self.dealloc(ptr, layout);
            }
        }
        new_ptr
    }
}

#[cfg(not(test))]
#[global_allocator]
static ALLOC: LibcAllocator = LibcAllocator;

#[cfg(not(test))]
#[panic_handler]
fn panic_handler(_info: &core::panic::PanicInfo<'_>) -> ! {
    fatal("internal panic")
}

/// Program entrypoint invoked by C runtime startup (`crt1.o`).
///
/// # Safety
/// Caller (`libc` C runtime) must pass a valid `argc` and NULL-terminated `argv` array.
#[cfg(not(test))]
#[no_mangle]
pub unsafe extern "C" fn main(
    argc: libc::c_int,
    argv: *const *const libc::c_char,
) -> libc::c_int {
    run_launcher(argc, argv);
    0
}

extern "C" {
    static environ: *const *const libc::c_char;
}

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
enum ExecMode {
    Auto,
    Memfd,
    Cache,
}

#[derive(Debug)]
enum CacheOutcome {
    Hit(OwnedFd),
    Miss,
}

#[derive(Debug, Eq, PartialEq)]
enum CacheError {
    Unsafe(&'static str),
}

#[derive(Debug)]
struct OwnedFd(libc::c_int);

impl OwnedFd {
    fn from_raw(fd: libc::c_int) -> Self {
        Self(fd)
    }

    fn as_raw_fd(&self) -> libc::c_int {
        self.0
    }

    fn into_raw(mut self) -> libc::c_int {
        let fd = self.0;
        self.0 = -1;
        fd
    }
}

impl Drop for OwnedFd {
    fn drop(&mut self) {
        if self.0 >= 0 {
            // SAFETY: `self.0` is an open owned file descriptor.
            unsafe {
                let _ = libc::close(self.0);
            }
        }
    }
}

fn last_errno() -> i32 {
    // SAFETY: `__errno_location` returns a valid thread-local errno pointer on Linux.
    unsafe { *libc::__errno_location() }
}

fn clear_errno() {
    // SAFETY: `__errno_location` returns a valid thread-local errno pointer on Linux.
    unsafe {
        *libc::__errno_location() = 0;
    }
}

fn write_stderr(bytes: &[u8]) {
    let mut rest = bytes;
    while !rest.is_empty() {
        // SAFETY: fd 2 is standard error and `rest` is valid readable memory.
        let n = unsafe { libc::write(2, rest.as_ptr().cast(), rest.len()) };
        if n < 0 {
            if last_errno() == libc::EINTR {
                continue;
            }
            break;
        }
        if n == 0 {
            break;
        }
        rest = &rest[n as usize..];
    }
}

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
enum DiagMode {
    Off,
    Debug,
    Json,
}

fn resolve_diag_mode_from(debug_raw: Option<&[u8]>, log_raw: Option<&[u8]>) -> DiagMode {
    let dbg = debug_raw.unwrap_or(b"");
    let log = log_raw.unwrap_or(b"");
    if dbg.is_empty() && log.is_empty() {
        return DiagMode::Off;
    }
    if (dbg == b"0" || dbg.eq_ignore_ascii_case(b"false")) && log.is_empty() {
        return DiagMode::Off;
    }
    if log.eq_ignore_ascii_case(b"json") {
        return DiagMode::Json;
    }
    DiagMode::Debug
}

fn resolve_diag_mode() -> DiagMode {
    resolve_diag_mode_from(
        getenv_bytes(b"MICROFAT_DEBUG\0"),
        getenv_bytes(b"MICROFAT_LOG\0"),
    )
}

fn monotonic_ns() -> u64 {
    // SAFETY: `libc::timespec` is POD initialized by `clock_gettime`.
    let mut ts: libc::timespec = unsafe { core::mem::zeroed() };
    // SAFETY: `CLOCK_MONOTONIC` writes into valid stack `ts`.
    if unsafe { libc::clock_gettime(libc::CLOCK_MONOTONIC, &mut ts) } != 0 {
        return 0;
    }
    (ts.tv_sec.max(0) as u64)
        .saturating_mul(1_000_000_000)
        .saturating_add(ts.tv_nsec.max(0) as u64)
}

fn realtime_ns() -> i64 {
    // SAFETY: `libc::timespec` is POD initialized by `clock_gettime`.
    let mut ts: libc::timespec = unsafe { core::mem::zeroed() };
    // SAFETY: `CLOCK_REALTIME` writes into valid stack `ts`.
    if unsafe { libc::clock_gettime(libc::CLOCK_REALTIME, &mut ts) } != 0 {
        return 1;
    }
    let ns = (ts.tv_sec.max(0) as i64)
        .saturating_mul(1_000_000_000)
        .saturating_add(ts.tv_nsec.max(0) as i64);
    if ns <= 0 {
        1
    } else {
        ns
    }
}

fn push_json_escaped(out: &mut Vec<u8>, bytes: &[u8]) {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    for &b in bytes {
        match b {
            b'"' => out.extend_from_slice(b"\\\""),
            b'\\' => out.extend_from_slice(b"\\\\"),
            b'\n' => out.extend_from_slice(b"\\n"),
            b'\r' => out.extend_from_slice(b"\\r"),
            b'\t' => out.extend_from_slice(b"\\t"),
            0..=0x1f => {
                out.extend_from_slice(b"\\u00");
                out.push(HEX[(b >> 4) as usize]);
                out.push(HEX[(b & 0x0f) as usize]);
            }
            _ => out.push(b),
        }
    }
}

fn fatal_privilege(detail: &str) -> ! {
    write_stderr(b"[microfat] error: privilege-bearing launcher execution is unsupported: ");
    write_stderr(detail.as_bytes());
    write_stderr(b"\n");
    // SAFETY: terminates process immediately with exit status 1.
    unsafe { libc::_exit(1) }
}

fn emit_json_error(msg_bytes: &[u8]) {
    let mut out = Vec::with_capacity(128 + msg_bytes.len());
    out.extend_from_slice(b"[microfat] {\"event\":\"error\",\"timestamp_unix_nano\":");
    let mut ts_buf = [0u8; 24];
    let ts_dec = format_u64_decimal(realtime_ns() as u64, &mut ts_buf);
    out.extend_from_slice(ts_dec);
    out.extend_from_slice(b",\"stage\":\"launcher_main\",\"error\":\"");
    push_json_escaped(&mut out, msg_bytes);
    out.extend_from_slice(b"\"}\n");
    write_stderr(&out);
}

fn fatal(msg: &str) -> ! {
    if resolve_diag_mode() == DiagMode::Json {
        emit_json_error(msg.as_bytes());
    }
    write_stderr(b"[microfat] error: ");
    write_stderr(msg.as_bytes());
    write_stderr(b"\n");
    // SAFETY: terminates process immediately with exit status 1.
    unsafe { libc::_exit(1) }
}

fn fatal_pair(prefix: &str, detail: &str) -> ! {
    if resolve_diag_mode() == DiagMode::Json {
        let mut combined = Vec::with_capacity(prefix.len() + 2 + detail.len());
        combined.extend_from_slice(prefix.as_bytes());
        combined.extend_from_slice(b": ");
        combined.extend_from_slice(detail.as_bytes());
        emit_json_error(&combined);
    }
    write_stderr(b"[microfat] error: ");
    write_stderr(prefix.as_bytes());
    write_stderr(b": ");
    write_stderr(detail.as_bytes());
    write_stderr(b"\n");
    // SAFETY: terminates process immediately with exit status 1.
    unsafe { libc::_exit(1) }
}

fn format_u64_decimal(mut n: u64, buf: &mut [u8; 24]) -> &[u8] {
    if n == 0 {
        buf[23] = b'0';
        return &buf[23..];
    }
    let mut pos = buf.len();
    while n > 0 {
        pos -= 1;
        buf[pos] = b'0' + (n % 10) as u8;
        n /= 10;
    }
    &buf[pos..]
}

fn bytes_to_cstr(bytes: &[u8]) -> Result<Vec<u8>, CacheError> {
    if bytes.contains(&0) {
        return Err(CacheError::Unsafe("path contains interior NUL byte"));
    }
    let mut out = Vec::with_capacity(bytes.len() + 1);
    out.extend_from_slice(bytes);
    out.push(0);
    Ok(out)
}

fn getenv_bytes(name_nul: &[u8]) -> Option<&'static [u8]> {
    debug_assert!(name_nul.ends_with(&[0]));
    // SAFETY: `name_nul` is NUL-terminated and returned pointer points into process env.
    let p = unsafe { libc::getenv(name_nul.as_ptr().cast()) };
    if p.is_null() {
        return None;
    }
    // SAFETY: `libc::getenv` returns a NUL-terminated C string when non-null.
    let len = unsafe { libc::strlen(p) };
    // SAFETY: valid C string slice of `len` bytes.
    Some(unsafe { core::slice::from_raw_parts(p.cast::<u8>(), len) })
}

fn trim_ascii_bytes(mut s: &[u8]) -> &[u8] {
    while let [first, rest @ ..] = s {
        if first.is_ascii_whitespace() {
            s = rest;
        } else {
            break;
        }
    }
    while let [rest @ .., last] = s {
        if last.is_ascii_whitespace() {
            s = rest;
        } else {
            break;
        }
    }
    s
}

fn open_cstr_readonly(c_path: *const libc::c_char) -> Result<OwnedFd, i32> {
    loop {
        // SAFETY: caller provides a valid NUL-terminated C string.
        let fd = unsafe { libc::open(c_path, libc::O_RDONLY | libc::O_CLOEXEC) };
        if fd >= 0 {
            return Ok(OwnedFd::from_raw(fd));
        }
        let err = last_errno();
        if err != libc::EINTR {
            return Err(err);
        }
    }
}

fn read_cstr_bounded(c_path: *const libc::c_char, max_len: usize) -> Result<Vec<u8>, i32> {
    let fd = open_cstr_readonly(c_path)?;
    let mut out = Vec::new();
    let mut buf = [0u8; 512];
    loop {
        // SAFETY: `fd` is valid and `buf` is stack-allocated writable memory.
        let n = unsafe { libc::read(fd.as_raw_fd(), buf.as_mut_ptr().cast(), buf.len()) };
        if n < 0 {
            let err = last_errno();
            if err == libc::EINTR {
                continue;
            }
            return Err(err);
        }
        if n == 0 {
            return Ok(out);
        }
        let n_usize = n as usize;
        if out.len().saturating_add(n_usize) > max_len {
            return Err(libc::E2BIG);
        }
        out.extend_from_slice(&buf[..n_usize]);
    }
}

fn pread_exact(fd: libc::c_int, mut buf: &mut [u8], mut offset: u64) -> Result<(), i32> {
    while !buf.is_empty() {
        let Ok(off_t_pos) = libc::off_t::try_from(offset) else {
            return Err(libc::EOVERFLOW);
        };
        // SAFETY: `fd` is open and `buf` points to writable memory.
        let n = unsafe { libc::pread(fd, buf.as_mut_ptr().cast(), buf.len(), off_t_pos) };
        if n < 0 {
            let err = last_errno();
            if err == libc::EINTR {
                continue;
            }
            return Err(err);
        }
        if n == 0 {
            return Err(libc::EIO);
        }
        let used = n as usize;
        buf = &mut buf[used..];
        offset = offset.saturating_add(used as u64);
    }
    Ok(())
}

fn pread_up_to(fd: libc::c_int, buf: &mut [u8], offset: u64) -> Result<usize, i32> {
    let mut pos = 0usize;
    while pos < buf.len() {
        let Ok(off_t_pos) = libc::off_t::try_from(offset.saturating_add(pos as u64)) else {
            return Err(libc::EOVERFLOW);
        };
        // SAFETY: `fd` is open and `&mut buf[pos..]` is writable.
        let n = unsafe {
            libc::pread(
                fd,
                buf[pos..].as_mut_ptr().cast(),
                buf.len() - pos,
                off_t_pos,
            )
        };
        if n < 0 {
            let err = last_errno();
            if err == libc::EINTR {
                continue;
            }
            return Err(err);
        }
        if n == 0 {
            break;
        }
        pos += n as usize;
    }
    Ok(pos)
}

fn write_all_fd(fd: libc::c_int, mut data: &[u8]) -> Result<(), i32> {
    while !data.is_empty() {
        // SAFETY: `fd` is an open writable descriptor and `data` is readable.
        let n = unsafe { libc::write(fd, data.as_ptr().cast(), data.len()) };
        if n < 0 {
            let err = last_errno();
            if err == libc::EINTR {
                continue;
            }
            return Err(err);
        }
        if n == 0 {
            return Err(libc::EIO);
        }
        data = &data[n as usize..];
    }
    Ok(())
}

fn fstat_fd(fd: libc::c_int) -> Option<libc::stat> {
    // SAFETY: `libc::stat` is POD populated on success by `libc::fstat`.
    let mut st: libc::stat = unsafe { core::mem::zeroed() };
    // SAFETY: `fd` is open and `&mut st` is writable stack storage.
    if unsafe { libc::fstat(fd, &mut st) } != 0 {
        return None;
    }
    Some(st)
}

fn is_safe_capability_xattr_result(ret: libc::ssize_t, errno: i32) -> bool {
    if ret >= 0 {
        return false;
    }
    errno == libc::ENODATA || errno == libc::ENOTSUP
}

fn at_secure_nonzero() -> bool {
    clear_errno();
    // SAFETY: `libc::getauxval` takes a kernel auxv tag without pointer arguments.
    let val = unsafe { libc::getauxval(libc::AT_SECURE) };
    if val != 0 {
        return true;
    }
    if last_errno() != libc::ENOENT {
        return false;
    }

    let auxv = match read_cstr_bounded(c"/proc/self/auxv".as_ptr(), 4096) {
        Ok(b) => b,
        Err(_) => fatal_privilege("cannot read /proc/self/auxv to verify AT_SECURE"),
    };
    let word = core::mem::size_of::<usize>();
    let stride = word * 2;
    let mut off = 0usize;
    while off + stride <= auxv.len() {
        let Ok(key_bytes) = auxv[off..off + word].try_into() else {
            break;
        };
        let Ok(val_bytes) = auxv[off + word..off + stride].try_into() else {
            break;
        };
        let key = usize::from_ne_bytes(key_bytes);
        let entry_val = usize::from_ne_bytes(val_bytes);
        if key == 0 && entry_val == 0 {
            break;
        }
        if key == AT_SECURE_KEY && entry_val != 0 {
            return true;
        }
        off += stride;
    }
    false
}

fn check_unprivileged() {
    // SAFETY: `getuid`/`geteuid`/`getgid`/`getegid` take no pointers and cannot fail.
    unsafe {
        if libc::getuid() != libc::geteuid() || libc::getgid() != libc::getegid() {
            fatal_privilege("uid/gid mismatch");
        }
    }
    if at_secure_nonzero() {
        fatal_privilege("AT_SECURE != 0");
    }
    // SAFETY: static NUL-terminated path & xattr key with NULL buffer (size probe).
    let ret = unsafe {
        libc::getxattr(
            c"/proc/self/exe".as_ptr(),
            c"security.capability".as_ptr(),
            ptr::null_mut(),
            0,
        )
    };
    if !is_safe_capability_xattr_result(ret, last_errno()) {
        fatal_privilege("executable carries file capabilities or capabilities are uncheckable");
    }
}

fn resolve_exec_mode_from(exec_val: Option<&[u8]>, dispatch_val: Option<&[u8]>) -> ExecMode {
    let raw = exec_val
        .map(trim_ascii_bytes)
        .filter(|s| !s.is_empty())
        .or_else(|| dispatch_val.map(trim_ascii_bytes).filter(|s| !s.is_empty()))
        .unwrap_or(b"");
    if raw.eq_ignore_ascii_case(b"cache") {
        ExecMode::Cache
    } else if raw.eq_ignore_ascii_case(b"memfd") {
        ExecMode::Memfd
    } else {
        ExecMode::Auto
    }
}

fn resolve_exec_mode() -> ExecMode {
    resolve_exec_mode_from(
        getenv_bytes(b"MICROFAT_EXEC_MODE\0"),
        getenv_bytes(b"MICROFAT_DISPATCH_MODE\0"),
    )
}

fn resolve_selection_policy_full(
    host: HostCaps,
    force_raw: Option<&[u8]>,
    max_raw: Option<&[u8]>,
    disable_raw: Option<&[u8]>,
    policy_raw: Option<&[u8]>,
    avx512_prot_raw: Option<&[u8]>,
    is_skylake_x: bool,
) -> (SelectionPolicy, Option<&'static str>, Option<Vec<u8>>) {
    let arch = host.target_arch();
    let mut policy = SelectionPolicy::default_none();
    let mut applied: Option<&'static str> = None;
    let mut reason: Option<Vec<u8>> = None;

    if let Some(f) = force_raw.map(trim_ascii_bytes).filter(|s| !s.is_empty()) {
        let rank = parse_normalized_level_rank(arch, f);
        policy.force_rank = Some(rank);
        let mut r = Vec::with_capacity(b"MICROFAT_FORCE_LEVEL=".len() + f.len());
        r.extend_from_slice(b"MICROFAT_FORCE_LEVEL=");
        r.extend_from_slice(f);
        return (policy, Some("force_level"), Some(r));
    }

    if let Some(m) = max_raw.map(trim_ascii_bytes).filter(|s| !s.is_empty()) {
        let rank = parse_normalized_level_rank(arch, m);
        if rank != 0 {
            policy.max_rank = Some(rank);
            applied = Some("max_level");
            let mut r = Vec::with_capacity(b"MICROFAT_MAX_LEVEL=".len() + m.len());
            r.extend_from_slice(b"MICROFAT_MAX_LEVEL=");
            r.extend_from_slice(m);
            reason = Some(r);
        }
    }

    let policy_name = policy_raw.map(trim_ascii_bytes).unwrap_or(b"");
    let prot_val = avx512_prot_raw.map(trim_ascii_bytes).unwrap_or(b"");
    let safe_avx512 = policy_name.eq_ignore_ascii_case(b"safe_avx512")
        || policy_name.eq_ignore_ascii_case(b"no_downclock")
        || prot_val == b"1"
        || prot_val.eq_ignore_ascii_case(b"true")
        || prot_val.eq_ignore_ascii_case(b"yes")
        || prot_val.eq_ignore_ascii_case(b"on");

    if safe_avx512 && is_skylake_x {
        if let HostCaps::Amd64(r) = host {
            if r >= 4 && policy.max_rank.is_none_or(|mr| 3 < mr) {
                policy.max_rank = Some(3);
                applied = Some("avx512_downclock_protection");
                reason = Some(
                    b"Intel Skylake-X/Cascade Lake AVX-512 downclocking protection applied (capped at v3)"
                        .to_vec(),
                );
            }
        }
    }

    if let Some(d) = disable_raw.map(trim_ascii_bytes).filter(|s| !s.is_empty()) {
        for part in d.split(|&b| b == b',') {
            let tok = trim_ascii_bytes(part);
            if !tok.is_empty() {
                let rank = parse_normalized_level_rank(arch, tok);
                if rank != 0 {
                    policy.push_disabled(rank);
                    if applied.is_none() {
                        applied = Some("disable_variants");
                        let mut r =
                            Vec::with_capacity(b"MICROFAT_DISABLE_VARIANTS=".len() + d.len());
                        r.extend_from_slice(b"MICROFAT_DISABLE_VARIANTS=");
                        r.extend_from_slice(d);
                        reason = Some(r);
                    }
                }
            }
        }
    }

    (policy, applied, reason)
}

#[cfg(test)]
fn resolve_selection_policy_from(
    arch: TargetArch,
    force_raw: Option<&[u8]>,
    max_raw: Option<&[u8]>,
    disable_raw: Option<&[u8]>,
) -> (SelectionPolicy, Option<&'static str>) {
    let dummy_host = match arch {
        TargetArch::Amd64 => HostCaps::Amd64(4),
        TargetArch::Arm64 => HostCaps::Arm64(1),
    };
    let (policy, applied, _) = resolve_selection_policy_full(
        dummy_host,
        force_raw,
        max_raw,
        disable_raw,
        None,
        None,
        false,
    );
    (policy, applied)
}

struct MappedFile {
    ptr: *mut libc::c_void,
    len: usize,
}

impl MappedFile {
    fn try_map_fd(fd: libc::c_int, len: u64) -> Option<Self> {
        let len_usize = usize::try_from(len).ok()?;
        if len_usize == 0 || len_usize > isize::MAX as usize {
            return None;
        }
        // SAFETY: `fd` is open and `len_usize` fits inside the regular file.
        let p = unsafe {
            libc::mmap(
                ptr::null_mut(),
                len_usize,
                libc::PROT_READ,
                libc::MAP_PRIVATE,
                fd,
                0,
            )
        };
        if p == libc::MAP_FAILED {
            return None;
        }
        Some(Self {
            ptr: p,
            len: len_usize,
        })
    }

    fn as_slice(&self) -> &[u8] {
        // SAFETY: read-only mapping of `self.len` bytes valid until `Drop`.
        unsafe { core::slice::from_raw_parts(self.ptr.cast::<u8>(), self.len) }
    }
}

impl Drop for MappedFile {
    fn drop(&mut self) {
        if self.len > 0 {
            // SAFETY: unmaps pointer previously returned by `libc::mmap`.
            unsafe {
                let _ = libc::munmap(self.ptr, self.len);
            }
        }
    }
}

struct MappedWritableFile {
    ptr: *mut libc::c_void,
    len: usize,
}

impl MappedWritableFile {
    fn try_map_fd(fd: libc::c_int, len: u64) -> Option<Self> {
        let len_usize = usize::try_from(len).ok()?;
        if len_usize == 0 || len_usize > isize::MAX as usize {
            return None;
        }
        // SAFETY: writable `MAP_SHARED` mapping into `ftruncate`d descriptor; must drop before sealing.
        let p = unsafe {
            libc::mmap(
                ptr::null_mut(),
                len_usize,
                libc::PROT_READ | libc::PROT_WRITE,
                libc::MAP_SHARED,
                fd,
                0,
            )
        };
        if p == libc::MAP_FAILED {
            return None;
        }
        Some(Self {
            ptr: p,
            len: len_usize,
        })
    }

    fn as_mut_slice(&mut self) -> &mut [u8] {
        // SAFETY: exclusive `&mut self` borrow of `self.len` mapped writable bytes.
        unsafe { core::slice::from_raw_parts_mut(self.ptr.cast::<u8>(), self.len) }
    }
}

impl Drop for MappedWritableFile {
    fn drop(&mut self) {
        if self.len > 0 {
            // SAFETY: unmaps pointer previously returned by `libc::mmap`.
            unsafe {
                let _ = libc::munmap(self.ptr, self.len);
            }
        }
    }
}

enum ByteCow<'a> {
    Borrowed(&'a [u8]),
    Owned(Vec<u8>),
}

impl ByteCow<'_> {
    fn as_slice(&self) -> &[u8] {
        match self {
            Self::Borrowed(s) => s,
            Self::Owned(v) => v,
        }
    }
}

fn read_range<'a>(
    fd: libc::c_int,
    mapped: Option<&'a MappedFile>,
    offset: u64,
    len: usize,
    err_msg: &str,
) -> ByteCow<'a> {
    if let Some(m) = mapped {
        if let Ok(start) = usize::try_from(offset) {
            if let Some(end) = start.checked_add(len) {
                if let Some(slice) = m.as_slice().get(start..end) {
                    return ByteCow::Borrowed(slice);
                }
            }
        }
    }
    let mut buf = vec![0u8; len];
    if pread_exact(fd, &mut buf, offset).is_err() {
        fatal(err_msg);
    }
    ByteCow::Owned(buf)
}

fn encode_hex_sha256(digest: &[u8; 32]) -> [u8; 64] {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut out = [0u8; 64];
    for (i, &b) in digest.iter().enumerate() {
        out[i * 2] = HEX[(b >> 4) as usize];
        out[i * 2 + 1] = HEX[(b & 0x0f) as usize];
    }
    out
}

fn encode_hex_8(bytes: &[u8; 8]) -> [u8; 16] {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut out = [0u8; 16];
    for (i, &b) in bytes.iter().enumerate() {
        out[i * 2] = HEX[(b >> 4) as usize];
        out[i * 2 + 1] = HEX[(b & 0x0f) as usize];
    }
    out
}

fn random_bytes_8() -> [u8; 8] {
    let mut buf = [0u8; 8];
    let mut filled = 0usize;
    while filled < buf.len() {
        // SAFETY: `buf[filled..]` is valid writable stack storage.
        let n = unsafe {
            libc::getrandom(
                buf[filled..].as_mut_ptr().cast(),
                buf.len() - filled,
                0,
            )
        };
        if n > 0 {
            filled += n as usize;
            continue;
        }
        if n < 0 && last_errno() == libc::EINTR {
            continue;
        }
        break;
    }
    if filled == buf.len() {
        return buf;
    }

    let Ok(fd) = open_cstr_readonly(c"/dev/urandom".as_ptr()) else {
        fatal("cannot obtain random bytes for cache temp file");
    };
    while filled < buf.len() {
        // SAFETY: `fd` is open and `buf[filled..]` is valid writable memory.
        let n = unsafe { libc::read(fd.as_raw_fd(), buf[filled..].as_mut_ptr().cast(), buf.len() - filled) };
        if n > 0 {
            filled += n as usize;
            continue;
        }
        if n < 0 && last_errno() == libc::EINTR {
            continue;
        }
        fatal("cannot read /dev/urandom for cache temp file");
    }
    buf
}

fn hash_fd_stream(fd: libc::c_int, expected_size: u64) -> Result<[u8; 32], &'static str> {
    if let Some(m) = MappedFile::try_map_fd(fd, expected_size) {
        return Ok(FastSha256::digest(m.as_slice()));
    }
    let mut hasher = FastSha256::new();
    let mut buf = vec![0u8; IO_BUF_SIZE];
    let mut total = 0u64;
    loop {
        // SAFETY: `fd` is open and `buf` is valid writable memory.
        let n = unsafe { libc::read(fd, buf.as_mut_ptr().cast(), buf.len()) };
        if n < 0 {
            if last_errno() == libc::EINTR {
                continue;
            }
            return Err("read error hashing cached descriptor");
        }
        if n == 0 {
            break;
        }
        total = total.saturating_add(n as u64);
        if total > expected_size {
            return Err("cached entry exceeds expected size");
        }
        hasher.update(&buf[..n as usize]);
    }
    if total != expected_size {
        return Err("cached entry truncated");
    }
    Ok(hasher.finalize())
}

fn mapped_variant_slice<'a>(
    mapped: Option<&'a MappedFile>,
    variant: &SelectedVariant<'_>,
) -> Option<&'a [u8]> {
    let m = mapped?;
    let start = usize::try_from(variant.offset).ok()?;
    let len = usize::try_from(variant.compressed_size).ok()?;
    let end = start.checked_add(len)?;
    m.as_slice().get(start..end)
}

fn zstd_err_msg(e: ZstdDecompressError) -> &'static str {
    match e {
        ZstdDecompressError::BadMagic => "zstd bad frame magic",
        ZstdDecompressError::BadFrameHeader => "zstd bad frame header",
        ZstdDecompressError::BadBlockHeader | ZstdDecompressError::BadBlockType => {
            "zstd bad block header"
        }
        ZstdDecompressError::CorruptLiterals | ZstdDecompressError::CorruptSequences => {
            "zstd corrupt block stream"
        }
        ZstdDecompressError::BlockTooLarge | ZstdDecompressError::WindowTooLarge { .. } => {
            "zstd window or block exceeds maximum"
        }
        ZstdDecompressError::InvalidOffset => "zstd invalid match offset",
        ZstdDecompressError::FrameSizeMismatch | ZstdDecompressError::OutputTooSmall => {
            "decompressed payload exceeds declared UncompressedSize"
        }
        ZstdDecompressError::BadFseTable
        | ZstdDecompressError::BadHuffmanWeights
        | ZstdDecompressError::BadHuffmanStream => "zstd corrupt entropy table",
        ZstdDecompressError::ChecksumMismatch { .. } => "zstd frame checksum mismatch",
        ZstdDecompressError::DictMismatch { .. }
        | ZstdDecompressError::DictRequired
        | ZstdDecompressError::InvalidDictionary => "zstd dictionary error",
        ZstdDecompressError::InputExhausted => "truncated zstd frame",
        ZstdDecompressError::ExtraBytes => "unexpected trailing bytes in zstd frame",
    }
}

fn parse_zstd_dict(dict_bytes: Option<&[u8]>) -> Result<Option<ZstdDictionary>, &'static str> {
    let Some(raw_dict) = dict_bytes else {
        return Ok(None);
    };
    ZstdDictionary::from_bytes(raw_dict)
        .map(Some)
        .map_err(|_| "invalid zstd dictionary")
}

fn decompress_zstd_slice(
    src: &[u8],
    dst: &mut [u8],
    dict_bytes: Option<&[u8]>,
    max_window: u64,
    hasher: &mut FastSha256,
) -> Result<usize, &'static str> {
    let eff_win = max_window.clamp(MIN_ZSTD_WINDOW, MAX_ZSTD_WINDOW);
    let mut dec = match parse_zstd_dict(dict_bytes)? {
        Some(dict) => ZstdPushDecoder::with_dict_and_limit(dict, dst.len()),
        None => ZstdPushDecoder::with_limit(dst.len()),
    }
    .max_window(eff_win);

    let mut in_pos = 0usize;
    let mut out_pos = 0usize;
    loop {
        let progress = dec
            .decode(&src[in_pos..], &mut dst[out_pos..])
            .map_err(zstd_err_msg)?;
        in_pos += progress.consumed;
        if progress.produced > 0 {
            let end = out_pos + progress.produced;
            hasher.update(&dst[out_pos..end]);
            out_pos = end;
        }
        match progress.status {
            ZstdStatus::NeedsOutput => {
                return Err("decompressed payload exceeds declared UncompressedSize");
            }
            ZstdStatus::NeedsInput => {
                if in_pos >= src.len() || (progress.consumed == 0 && progress.produced == 0) {
                    dec.finish().map_err(zstd_err_msg)?;
                    return Ok(out_pos);
                }
            }
            ZstdStatus::Finished => {
                if in_pos >= src.len() {
                    dec.finish().map_err(zstd_err_msg)?;
                    return Ok(out_pos);
                }
            }
        }
    }
}

fn lz4_block_max_size(bd: u8) -> Result<usize, &'static str> {
    if (bd & 0x8F) != 0 {
        return Err("invalid lz4 frame BD flags");
    }
    match (bd >> 4) & 0x07 {
        4 => Ok(64 * 1024),
        5 => Ok(256 * 1024),
        6 => Ok(1024 * 1024),
        7 => Ok(4 * 1024 * 1024),
        _ => Err("unsupported lz4 block max size"),
    }
}

fn lz4_copy_from_slice_to_dst(
    chunk: &[u8],
    dst: &mut [u8],
    out_pos: &mut usize,
    content_hasher: Option<&mut XxHash32>,
    sha_hasher: &mut FastSha256,
) -> Result<(), &'static str> {
    let end = out_pos
        .checked_add(chunk.len())
        .filter(|&e| e <= dst.len())
        .ok_or("decompressed payload exceeds declared UncompressedSize")?;
    dst[*out_pos..end].copy_from_slice(chunk);
    sha_hasher.update(chunk);
    if let Some(ch) = content_hasher {
        ch.write(chunk);
    }
    *out_pos = end;
    Ok(())
}

fn decompress_lz4_slice(
    src: &[u8],
    dst: &mut [u8],
    sha_hasher: &mut FastSha256,
) -> Result<usize, &'static str> {
    let mut pos = 0usize;
    let mut out_pos = 0usize;

    while pos < src.len() {
        if src.len() - pos < 4 {
            return Err("truncated lz4 frame magic");
        }
        let magic = u32::from_le_bytes([src[pos], src[pos + 1], src[pos + 2], src[pos + 3]]);
        pos += 4;

        if (magic & LZ4_SKIPPABLE_MASK) == LZ4_SKIPPABLE_BASE {
            if src.len() - pos < 4 {
                return Err("truncated lz4 skippable frame size");
            }
            let skip_len = u32::from_le_bytes([src[pos], src[pos + 1], src[pos + 2], src[pos + 3]])
                as usize;
            pos += 4;
            if src.len() - pos < skip_len {
                return Err("truncated lz4 skippable frame payload");
            }
            pos += skip_len;
            continue;
        }

        if magic == LZ4F_LEGACY_MAGIC {
            const LEGACY_MAX_BLOCK: usize = 8 * 1024 * 1024;
            while pos < src.len() {
                if src.len() - pos < 4 {
                    return Err("truncated legacy lz4 block header");
                }
                let raw_size =
                    u32::from_le_bytes([src[pos], src[pos + 1], src[pos + 2], src[pos + 3]])
                        as usize;
                pos += 4;
                if src.len() - pos < raw_size {
                    return Err("truncated legacy lz4 block payload");
                }
                let cap_end = cmp::min(dst.len(), out_pos.saturating_add(LEGACY_MAX_BLOCK));
                let n = decompress_into(&src[pos..pos + raw_size], &mut dst[out_pos..cap_end])
                    .map_err(|_| "lz4 legacy block decompression failed")?;
                pos += raw_size;
                sha_hasher.update(&dst[out_pos..out_pos + n]);
                out_pos += n;
            }
            continue;
        }

        if magic != LZ4F_MAGIC {
            return Err("invalid lz4 frame magic");
        }
        if src.len() - pos < 3 {
            return Err("truncated lz4 frame descriptor");
        }

        let desc_start = pos;
        let flg = src[pos];
        let bd = src[pos + 1];
        pos += 2;

        if (flg & 0b1100_0000) != 0b0100_0000 || (flg & 0b0000_0010) != 0 {
            return Err("unsupported lz4 frame flags");
        }
        let block_indep = (flg & 0b0010_0000) != 0;
        let block_checksum = (flg & 0b0001_0000) != 0;
        let has_content_size = (flg & 0b0000_1000) != 0;
        let has_content_checksum = (flg & 0b0000_0100) != 0;
        let has_dict_id = (flg & 0b0000_0001) != 0;

        let block_max = lz4_block_max_size(bd)?;

        let expected_content_size = if has_content_size {
            if src.len() - pos < 8 {
                return Err("truncated lz4 content size");
            }
            let sz = u64::from_le_bytes(src[pos..pos + 8].try_into().unwrap());
            pos += 8;
            Some(sz)
        } else {
            None
        };
        if has_dict_id {
            if src.len() - pos < 4 {
                return Err("truncated lz4 dictionary id");
            }
            return Err("lz4 dictionary id unsupported");
        }

        if src.len() - pos < 1 {
            return Err("truncated lz4 header checksum");
        }
        let hdr_cksum_got = src[pos];
        let hdr_cksum_exp = ((XxHash32::oneshot(0, &src[desc_start..pos]) >> 8) & 0xff) as u8;
        if hdr_cksum_got != hdr_cksum_exp {
            return Err("lz4 frame header checksum mismatch");
        }
        pos += 1;

        let mut frame_written = 0u64;
        let frame_out_start = out_pos;
        let mut content_hasher = if has_content_checksum {
            Some(XxHash32::with_seed(0))
        } else {
            None
        };

        loop {
            if src.len() - pos < 4 {
                return Err("truncated lz4 block header");
            }
            let block_hdr =
                u32::from_le_bytes([src[pos], src[pos + 1], src[pos + 2], src[pos + 3]]);
            pos += 4;
            if block_hdr == 0 {
                break;
            }
            let is_uncompressed = (block_hdr & 0x8000_0000) != 0;
            let blk_len = (block_hdr & 0x7FFF_FFFF) as usize;
            if blk_len > block_max || src.len() - pos < blk_len {
                return Err("lz4 block size invalid or truncated");
            }
            let blk_data = &src[pos..pos + blk_len];
            pos += blk_len;

            if block_checksum {
                if src.len() - pos < 4 {
                    return Err("truncated lz4 block checksum");
                }
                let exp = u32::from_le_bytes([src[pos], src[pos + 1], src[pos + 2], src[pos + 3]]);
                pos += 4;
                if XxHash32::oneshot(0, blk_data) != exp {
                    return Err("lz4 block checksum mismatch");
                }
            }

            if is_uncompressed {
                lz4_copy_from_slice_to_dst(
                    blk_data,
                    dst,
                    &mut out_pos,
                    content_hasher.as_mut(),
                    sha_hasher,
                )?;
                frame_written = frame_written.saturating_add(blk_data.len() as u64);
            } else {
                let cap_end = cmp::min(dst.len(), out_pos.saturating_add(block_max));
                let dec_res = if block_indep {
                    decompress_into(blk_data, &mut dst[out_pos..cap_end])
                } else {
                    let (prefix, tail) = dst.split_at_mut(out_pos);
                    let tail_len = cmp::min(tail.len(), block_max);
                    let dict_start =
                        cmp::max(frame_out_start, out_pos.saturating_sub(LZ4_DICT_MAX));
                    let ext_dict = &prefix[dict_start..out_pos];
                    decompress_into_with_dict(blk_data, &mut tail[..tail_len], ext_dict)
                };
                let n = dec_res.map_err(|_| "lz4 block decompression failed")?;
                let emitted = &dst[out_pos..out_pos + n];
                sha_hasher.update(emitted);
                if let Some(ch) = content_hasher.as_mut() {
                    ch.write(emitted);
                }
                out_pos += n;
                frame_written = frame_written.saturating_add(n as u64);
            }
        }

        if let Some(expected_sz) = expected_content_size {
            if frame_written != expected_sz {
                return Err("lz4 frame content size mismatch");
            }
        }
        if let Some(ch) = content_hasher {
            if src.len() - pos < 4 {
                return Err("truncated lz4 content checksum");
            }
            let exp = u32::from_le_bytes([src[pos], src[pos + 1], src[pos + 2], src[pos + 3]]);
            pos += 4;
            if ch.finish_32() != exp {
                return Err("lz4 frame content checksum mismatch");
            }
        }
    }

    Ok(out_pos)
}

fn read_variant_bytes(fd: libc::c_int, variant: &SelectedVariant<'_>) -> Result<Vec<u8>, &'static str> {
    let comp_len = usize::try_from(variant.compressed_size)
        .map_err(|_| "compressed variant size overflows usize")?;
    let mut buf = vec![0u8; comp_len];
    pread_exact(fd, &mut buf, variant.offset).map_err(|_| "failed reading compressed payload")?;
    Ok(buf)
}

fn write_verified_chunk(
    target_fd: libc::c_int,
    data: &[u8],
    written: &mut u64,
    expected_size: u64,
    sha_hasher: &mut FastSha256,
) -> Result<(), &'static str> {
    let next = written
        .checked_add(data.len() as u64)
        .filter(|&w| w <= expected_size)
        .ok_or("decompressed payload exceeds declared UncompressedSize")?;
    write_all_fd(target_fd, data).map_err(|_| "short or failed write to target descriptor")?;
    sha_hasher.update(data);
    *written = next;
    Ok(())
}

fn decompress_zstd_streaming_fd(
    src_fd: libc::c_int,
    variant: &SelectedVariant<'_>,
    dict_bytes: Option<&[u8]>,
    max_window: u64,
    target_fd: libc::c_int,
    sha_hasher: &mut FastSha256,
) -> Result<u64, &'static str> {
    let eff_win = max_window.clamp(MIN_ZSTD_WINDOW, MAX_ZSTD_WINDOW);
    let max_out = usize::try_from(variant.uncompressed_size).unwrap_or(usize::MAX);
    let mut dec = match parse_zstd_dict(dict_bytes)? {
        Some(dict) => ZstdPushDecoder::with_dict_and_limit(dict, max_out),
        None => ZstdPushDecoder::with_limit(max_out),
    }
    .max_window(eff_win);

    let mut in_buf = vec![0u8; IO_BUF_SIZE];
    let mut out_buf = vec![0u8; IO_BUF_SIZE];
    let mut remaining = variant.compressed_size;
    let mut offset = variant.offset;
    let mut written = 0u64;

    loop {
        if remaining == 0 {
            loop {
                let p = dec.decode(&[], &mut out_buf).map_err(zstd_err_msg)?;
                if p.produced > 0 {
                    write_verified_chunk(
                        target_fd,
                        &out_buf[..p.produced],
                        &mut written,
                        variant.uncompressed_size,
                        sha_hasher,
                    )?;
                }
                if p.status != ZstdStatus::NeedsOutput {
                    break;
                }
            }
            dec.finish().map_err(zstd_err_msg)?;
            return Ok(written);
        }

        let to_read = cmp::min(in_buf.len() as u64, remaining) as usize;
        pread_exact(src_fd, &mut in_buf[..to_read], offset)
            .map_err(|_| "failed reading zstd payload chunk")?;
        offset = offset.saturating_add(to_read as u64);
        remaining -= to_read as u64;

        let mut in_pos = 0usize;
        loop {
            let p = dec
                .decode(&in_buf[in_pos..to_read], &mut out_buf)
                .map_err(zstd_err_msg)?;
            in_pos += p.consumed;
            if p.produced > 0 {
                write_verified_chunk(
                    target_fd,
                    &out_buf[..p.produced],
                    &mut written,
                    variant.uncompressed_size,
                    sha_hasher,
                )?;
            }
            match p.status {
                ZstdStatus::NeedsOutput => continue,
                ZstdStatus::NeedsInput | ZstdStatus::Finished => {
                    if in_pos >= to_read {
                        break;
                    }
                    if p.consumed == 0 && p.produced == 0 {
                        return Err("zstd decoder stalled");
                    }
                }
            }
        }
    }
}

fn extract_variant_to_fd(
    src_fd: libc::c_int,
    mapped: Option<&MappedFile>,
    variant: &SelectedVariant<'_>,
    dict_bytes: Option<&[u8]>,
    zstd_max_window: u64,
    target_fd: libc::c_int,
) -> Result<(), &'static str> {
    let mapped_slice = mapped_variant_slice(mapped, variant);

    // Zero-copy mmap fast path: decode & hash in L1/L2 cache with zero write() syscalls.
    if let Some(mut dst_map) = MappedWritableFile::try_map_fd(target_fd, variant.uncompressed_size) {
        let mut sha_hasher = FastSha256::new();
        let dst = dst_map.as_mut_slice();
        let written = match (variant.compression, mapped_slice) {
            (Compression::None, Some(src)) => {
                if src.len() != dst.len() {
                    return Err("uncompressed payload size mismatch");
                }
                for (in_chunk, out_chunk) in
                    src.chunks(IO_BUF_SIZE).zip(dst.chunks_mut(IO_BUF_SIZE))
                {
                    out_chunk.copy_from_slice(in_chunk);
                    sha_hasher.update(in_chunk);
                }
                dst.len()
            }
            (Compression::None, None) => {
                pread_exact(src_fd, dst, variant.offset)
                    .map_err(|_| "failed reading uncompressed payload")?;
                for chunk in dst.chunks(IO_BUF_SIZE) {
                    sha_hasher.update(chunk);
                }
                dst.len()
            }
            (Compression::Lz4, Some(src)) => {
                decompress_lz4_slice(src, dst, &mut sha_hasher)?
            }
            (Compression::Lz4, None) => {
                let raw = read_variant_bytes(src_fd, variant)?;
                decompress_lz4_slice(&raw, dst, &mut sha_hasher)?
            }
            (Compression::Zstd, Some(src)) => {
                decompress_zstd_slice(src, dst, dict_bytes, zstd_max_window, &mut sha_hasher)?
            }
            (Compression::Zstd, None) => {
                let raw = read_variant_bytes(src_fd, variant)?;
                decompress_zstd_slice(&raw, dst, dict_bytes, zstd_max_window, &mut sha_hasher)?
            }
        };
        // CRITICAL: unmap writable MAP_SHARED view BEFORE caller applies F_SEAL_WRITE!
        drop(dst_map);

        if written as u64 != variant.uncompressed_size {
            return Err("decompressed payload byte count mismatch");
        }
        if sha_hasher.finalize() != variant.sha256 {
            return Err("decompressed payload SHA-256 checksum mismatch");
        }
        return Ok(());
    }

    // Fallback when writable target mmap is unavailable.
    let mut sha_hasher = FastSha256::new();
    let written = match variant.compression {
        Compression::None => {
            if let Some(src) = mapped_slice {
                let mut w = 0u64;
                write_verified_chunk(
                    target_fd,
                    src,
                    &mut w,
                    variant.uncompressed_size,
                    &mut sha_hasher,
                )?;
                w
            } else {
                let mut buf = vec![0u8; IO_BUF_SIZE];
                let mut rem = variant.compressed_size;
                let mut off = variant.offset;
                let mut w = 0u64;
                while rem > 0 {
                    let take = cmp::min(buf.len() as u64, rem) as usize;
                    pread_exact(src_fd, &mut buf[..take], off)
                        .map_err(|_| "failed reading payload")?;
                    write_verified_chunk(
                        target_fd,
                        &buf[..take],
                        &mut w,
                        variant.uncompressed_size,
                        &mut sha_hasher,
                    )?;
                    off = off.saturating_add(take as u64);
                    rem -= take as u64;
                }
                w
            }
        }
        Compression::Zstd => decompress_zstd_streaming_fd(
            src_fd,
            variant,
            dict_bytes,
            zstd_max_window,
            target_fd,
            &mut sha_hasher,
        )?,
        Compression::Lz4 => {
            let uncomp_len = usize::try_from(variant.uncompressed_size)
                .map_err(|_| "uncompressed size overflows usize")?;
            let mut full_dst = vec![0u8; uncomp_len];
            let n = match mapped_slice {
                Some(src) => decompress_lz4_slice(src, &mut full_dst, &mut sha_hasher)?,
                None => {
                    let raw = read_variant_bytes(src_fd, variant)?;
                    decompress_lz4_slice(&raw, &mut full_dst, &mut sha_hasher)?
                }
            };
            write_all_fd(target_fd, &full_dst[..n])
                .map_err(|_| "failed writing decompressed lz4 payload")?;
            n as u64
        }
    };

    if written != variant.uncompressed_size {
        return Err("decompressed payload byte count mismatch");
    }
    if sha_hasher.finalize() != variant.sha256 {
        return Err("decompressed payload SHA-256 checksum mismatch");
    }
    Ok(())
}

#[derive(Copy, Clone, Debug, Eq, PartialEq)]
struct CgroupLimits {
    version: i32,
    effective_memory_limit: Option<i64>,
    quota_us: i64,
    period_us: i64,
}

fn path_join_cstr(base: &[u8], rel: &[u8]) -> Vec<u8> {
    let mut out = Vec::with_capacity(base.len() + rel.len() + 2);
    out.extend_from_slice(base);
    let clean_rel = trim_leading_slashes(rel);
    if !clean_rel.is_empty() {
        if !out.ends_with(b"/") {
            out.push(b'/');
        }
        out.extend_from_slice(clean_rel);
    }
    out.push(0);
    out
}

fn trim_leading_slashes(mut s: &[u8]) -> &[u8] {
    while let [b'/', rest @ ..] = s {
        s = rest;
    }
    s
}

fn cstr_exists(c_path: &[u8]) -> bool {
    // SAFETY: `libc::stat` is POD populated on success; `c_path` is NUL-terminated.
    let mut st: libc::stat = unsafe { core::mem::zeroed() };
    // SAFETY: valid NUL-terminated path & writable stack stat buffer.
    unsafe { libc::stat(c_path.as_ptr().cast(), &mut st) == 0 }
}

type ProcCgroupPaths = (Option<Vec<u8>>, Option<Vec<u8>>, Option<Vec<u8>>);

fn parse_proc_cgroup_bytes(data: &[u8]) -> ProcCgroupPaths {
    let mut v2_path: Option<Vec<u8>> = None;
    let mut v1_mem: Option<Vec<u8>> = None;
    let mut v1_cpu: Option<Vec<u8>> = None;

    for raw_line in data.split(|&b| b == b'\n') {
        let line = trim_ascii_bytes(raw_line);
        if line.is_empty() || line.starts_with(b"#") {
            continue;
        }
        let mut parts = line.splitn(3, |&b| b == b':');
        let Some(hid) = parts.next().map(trim_ascii_bytes) else {
            continue;
        };
        let Some(ctrls) = parts.next().map(trim_ascii_bytes) else {
            continue;
        };
        let Some(rel) = parts.next().map(trim_ascii_bytes) else {
            continue;
        };
        if hid == b"0" && ctrls.is_empty() {
            v2_path = Some(rel.to_vec());
            continue;
        }
        for c in ctrls.split(|&b| b == b',') {
            let ctrl = trim_ascii_bytes(c);
            if ctrl == b"memory" {
                v1_mem = Some(rel.to_vec());
            } else if ctrl == b"cpu" || (ctrl == b"cpu,cpuacct" && v1_cpu.is_none()) {
                v1_cpu = Some(rel.to_vec());
            }
        }
    }
    (v2_path, v1_mem, v1_cpu)
}

fn parent_rel_slice(rel: &[u8]) -> Option<&[u8]> {
    let trimmed = trim_leading_slashes(rel);
    if trimmed.is_empty() {
        return None;
    }
    match trimmed.iter().rposition(|&b| b == b'/') {
        Some(idx) => Some(&trimmed[..idx]),
        None => Some(b""),
    }
}

fn validate_rel_cgroup_path(rel: &[u8]) -> bool {
    if rel.contains(&0) {
        return false;
    }
    for comp in rel.split(|&b| b == b'/') {
        if comp == b".." {
            return false;
        }
    }
    true
}

fn resolve_cgroup_limits_custom(root: &[u8], proc_path_nul: &[u8]) -> Option<CgroupLimits> {
    let root_c = path_join_cstr(root, b"");
    if !cstr_exists(&root_c) {
        return None;
    }
    let proc_data = read_cstr_bounded(proc_path_nul.as_ptr().cast(), 64 * 1024).ok()?;
    let (v2_rel_opt, v1_mem_rel, v1_cpu_rel) = parse_proc_cgroup_bytes(&proc_data);

    let is_v2 = cstr_exists(&path_join_cstr(root, b"memory.max"))
        || cstr_exists(&path_join_cstr(root, b"memory.high"))
        || cstr_exists(&path_join_cstr(root, b"cgroup.controllers"));

    if is_v2 {
        let rel = v2_rel_opt?;
        if !validate_rel_cgroup_path(&rel) {
            return None;
        }
        let mut min_max: Option<i64> = None;
        let mut min_high: Option<i64> = None;
        let mut min_quota_ratio: Option<(i64, i64)> = None;
        let mut cur: Option<&[u8]> = Some(rel.as_slice());

        while let Some(r) = cur {
            let dir_slice_len = if r.is_empty() { 0 } else { r.len() };
            let dir_rel = &r[..dir_slice_len];
            if !cstr_exists(&path_join_cstr(root, dir_rel)) {
                return None;
            }
            let max_c = path_join_cstr(root, &append_subpath(dir_rel, b"memory.max"));
            if let Ok(bytes) = read_cstr_bounded(max_c.as_ptr().cast(), 128) {
                if let Some(Some(v)) = parse_cgroup_v2_memory_bytes(&bytes) {
                    min_max = Some(min_max.map_or(v, |m| m.min(v)));
                }
            }
            let high_c = path_join_cstr(root, &append_subpath(dir_rel, b"memory.high"));
            if let Ok(bytes) = read_cstr_bounded(high_c.as_ptr().cast(), 128) {
                if let Some(Some(v)) = parse_cgroup_v2_memory_bytes(&bytes) {
                    min_high = Some(min_high.map_or(v, |m| m.min(v)));
                }
            }
            let cpu_c = path_join_cstr(root, &append_subpath(dir_rel, b"cpu.max"));
            if let Ok(bytes) = read_cstr_bounded(cpu_c.as_ptr().cast(), 128) {
                if let Some(Some((q, p))) = parse_cgroup_v2_cpu_max(&bytes) {
                    let tighter = match min_quota_ratio {
                        None => true,
                        Some((mq, mp)) => (q as i128) * (mp as i128) < (mq as i128) * (p as i128),
                    };
                    if tighter {
                        min_quota_ratio = Some((q, p));
                    }
                }
            }
            cur = parent_rel_slice(r);
        }

        let (quota_us, period_us) = min_quota_ratio.unwrap_or((0, 0));
        return Some(CgroupLimits {
            version: 2,
            effective_memory_limit: effective_memory_limit(min_max, min_high),
            quota_us,
            period_us,
        });
    }

    // Cgroup v1 lookup (supporting controller subdirs or direct test fixtures).
    if v1_mem_rel.is_none() && v1_cpu_rel.is_none() {
        return None;
    }

    let mem_base = if cstr_exists(&path_join_cstr(root, b"memory")) {
        let mut b = root.to_vec();
        b.extend_from_slice(b"/memory");
        b
    } else if cstr_exists(&path_join_cstr(root, b"memory.limit_in_bytes")) {
        root.to_vec()
    } else {
        Vec::new()
    };

    let mut eff_mem: Option<i64> = None;
    if let Some(ref mrel) = v1_mem_rel {
        if !mem_base.is_empty() && validate_rel_cgroup_path(mrel) {
            let mut cur: Option<&[u8]> = Some(mrel.as_slice());
            while let Some(r) = cur {
                if !cstr_exists(&path_join_cstr(&mem_base, r)) {
                    return None;
                }
                let mc = path_join_cstr(&mem_base, &append_subpath(r, b"memory.limit_in_bytes"));
                if let Ok(bytes) = read_cstr_bounded(mc.as_ptr().cast(), 128) {
                    if let Some(Some(v)) = parse_cgroup_v1_memory_bytes(&bytes) {
                        eff_mem = Some(eff_mem.map_or(v, |m| m.min(v)));
                    }
                }
                cur = parent_rel_slice(r);
            }
        }
    }

    let cpu_base = if cstr_exists(&path_join_cstr(root, b"cpu")) {
        let mut b = root.to_vec();
        b.extend_from_slice(b"/cpu");
        b
    } else if cstr_exists(&path_join_cstr(root, b"cpu,cpuacct")) {
        let mut b = root.to_vec();
        b.extend_from_slice(b"/cpu,cpuacct");
        b
    } else if cstr_exists(&path_join_cstr(root, b"cpu.cfs_quota_us")) {
        root.to_vec()
    } else {
        Vec::new()
    };

    let mut min_quota_ratio: Option<(i64, i64)> = None;
    if let Some(ref crel) = v1_cpu_rel {
        if !cpu_base.is_empty() && validate_rel_cgroup_path(crel) {
            let mut cur: Option<&[u8]> = Some(crel.as_slice());
            while let Some(r) = cur {
                if !cstr_exists(&path_join_cstr(&cpu_base, r)) {
                    return None;
                }
                let qc = path_join_cstr(&cpu_base, &append_subpath(r, b"cpu.cfs_quota_us"));
                let pc = path_join_cstr(&cpu_base, &append_subpath(r, b"cpu.cfs_period_us"));
                if let (Ok(qb), Ok(pb)) = (
                    read_cstr_bounded(qc.as_ptr().cast(), 128),
                    read_cstr_bounded(pc.as_ptr().cast(), 128),
                ) {
                    if let Some(Some((q, p))) = parse_cgroup_v1_cpu_quota_and_period(&qb, &pb) {
                        let tighter = match min_quota_ratio {
                            None => true,
                            Some((mq, mp)) => {
                                (q as i128) * (mp as i128) < (mq as i128) * (p as i128)
                            }
                        };
                        if tighter {
                            min_quota_ratio = Some((q, p));
                        }
                    }
                }
                cur = parent_rel_slice(r);
            }
        }
    }

    let (quota_us, period_us) = min_quota_ratio.unwrap_or((0, 0));
    Some(CgroupLimits {
        version: 1,
        effective_memory_limit: eff_mem,
        quota_us,
        period_us,
    })
}

fn append_subpath(dir_rel: &[u8], file: &[u8]) -> Vec<u8> {
    let clean = trim_leading_slashes(dir_rel);
    if clean.is_empty() {
        return file.to_vec();
    }
    let mut out = Vec::with_capacity(clean.len() + 1 + file.len());
    out.extend_from_slice(clean);
    out.push(b'/');
    out.extend_from_slice(file);
    out
}

fn inspect_zstd_frame_window(
    self_fd: libc::c_int,
    mapped: Option<&MappedFile>,
    variant: &SelectedVariant<'_>,
) -> Result<Option<u64>, &'static str> {
    let mut cur_off = variant.offset;
    let mut remaining = variant.compressed_size;
    let mut prefix_buf = [0u8; 18];

    for _ in 0..=ZSTD_MAX_SKIPPABLE_FRAMES {
        if remaining < 4 {
            return Ok(None);
        }
        let slice: &[u8] = if let Some(m) = mapped {
            let Some(start) = usize::try_from(cur_off).ok() else {
                return Ok(None);
            };
            let take = cmp::min(remaining as usize, prefix_buf.len());
            let Some(end) = start.checked_add(take) else {
                return Ok(None);
            };
            let Some(s) = m.as_slice().get(start..end) else {
                return Ok(None);
            };
            s
        } else {
            let to_read = cmp::min(remaining as usize, prefix_buf.len());
            let n = pread_up_to(self_fd, &mut prefix_buf[..to_read], cur_off)
                .map_err(|_| "cannot read zstd frame header for budget check")?;
            &prefix_buf[..n]
        };

        if slice.len() < 4 {
            return Ok(None);
        }
        let magic = u32::from_le_bytes([slice[0], slice[1], slice[2], slice[3]]);
        if (magic & ZSTD_SKIPPABLE_MASK) == ZSTD_SKIPPABLE_BASE {
            if slice.len() < 8 {
                return Ok(None);
            }
            let skip_len = u32::from_le_bytes([slice[4], slice[5], slice[6], slice[7]]) as u64;
            let skip_total = 8u64.saturating_add(skip_len);
            if skip_total > remaining {
                return Ok(None);
            }
            cur_off = cur_off.saturating_add(skip_total);
            remaining -= skip_total;
            continue;
        }

        if magic != ZSTD_MAGIC {
            return Ok(None);
        }

        match parse_frame_header(slice) {
            Ok(hdr) => {
                if hdr.window_size > MAX_ZSTD_WINDOW {
                    return Err("zstd frame window exceeds maximum allowed size");
                }
                let raw_win = if hdr.window_size == 0 {
                    hdr.frame_content_size.unwrap_or(MIN_ZSTD_WINDOW)
                } else {
                    hdr.window_size
                };
                if raw_win > MAX_ZSTD_WINDOW {
                    return Err("zstd frame window exceeds maximum allowed size");
                }
                return Ok(Some(raw_win.max(MIN_ZSTD_WINDOW)));
            }
            Err(ZstdDecompressError::WindowTooLarge { .. }) => {
                return Err("zstd frame window exceeds maximum allowed size");
            }
            Err(_) => return Ok(None),
        }
    }

    Ok(None)
}

fn check_extraction_budget(
    limits: Option<CgroupLimits>,
    self_fd: libc::c_int,
    mapped: Option<&MappedFile>,
    variant: &SelectedVariant<'_>,
    dict: Option<DictMeta>,
) -> Result<u64, &'static str> {
    let payload_i64 =
        i64::try_from(variant.uncompressed_size).map_err(|_| "payload size overflows i64")?;
    let dict_i64 = match dict {
        Some(d) => i64::try_from(d.size).map_err(|_| "dictionary size overflows i64")?,
        None => 0,
    };

    let (decoder_i64, zstd_max_window) = match variant.compression {
        Compression::None => (NONE_COPY_ALLOWANCE, MAX_ZSTD_WINDOW),
        Compression::Lz4 => (LZ4_DECODER_ALLOWANCE, MAX_ZSTD_WINDOW),
        Compression::Zstd => match inspect_zstd_frame_window(self_fd, mapped, variant)? {
            Some(win) => {
                let win_i64 = i64::try_from(win)
                    .map_err(|_| "zstd frame window exceeds maximum allowed size")?;
                let mem = zstd_decoder_memory(
                    win_i64,
                    DEFAULT_ZSTD_MAX_WINDOW,
                    DEFAULT_ZSTD_OVERHEAD_BYTES,
                    DEFAULT_ZSTD_MAX_DECODER_MEMORY,
                )
                .ok_or("zstd frame window exceeds maximum allowed size")?;
                (mem, win)
            }
            None => (ZSTD_FALLBACK_ALLOWANCE, MAX_ZSTD_WINDOW),
        },
    };

    let Some(lim) = limits else {
        return Ok(zstd_max_window);
    };
    let Some(ceiling) = lim.effective_memory_limit else {
        return Ok(zstd_max_window);
    };
    if ceiling >= UNLIMITED_CGROUP_V1_THRESHOLD {
        return Ok(zstd_max_window);
    }

    let estimate = extraction_memory(
        payload_i64,
        dict_i64,
        decoder_i64,
        DEFAULT_MIN_HEADROOM_BYTES,
    )
    .ok_or("extraction memory budget overflow")?;
    if estimate > ceiling {
        return Err("estimated extraction memory exceeds cgroup ceiling");
    }
    Ok(zstd_max_window)
}

fn runtime_autotune_enabled(
    autotune_raw: Option<&[u8]>,
    dry_run_raw: Option<&[u8]>,
) -> bool {
    if let Some(a) = autotune_raw.map(trim_ascii_bytes) {
        if a == b"0" || a.eq_ignore_ascii_case(b"false") {
            return false;
        }
    }
    if let Some(d) = dry_run_raw.map(trim_ascii_bytes) {
        if d == b"1" || d.eq_ignore_ascii_case(b"true") {
            return false;
        }
    }
    true
}

fn page_aligned_retained_bytes(uncompressed_size: u64, is_memfd: bool) -> i64 {
    if !is_memfd {
        return 0;
    }
    // SAFETY: `libc::sysconf(libc::_SC_PAGESIZE)` is safe and requires no pointers.
    let ps = unsafe { libc::sysconf(libc::_SC_PAGESIZE) };
    let page = if ps > 0 { ps as u64 } else { 4096 }.max(4096);
    let rounded = uncompressed_size
        .saturating_add(page - 1)
        / page
        * page;
    i64::try_from(rounded).unwrap_or(0)
}

fn env_key_of(entry: &[u8]) -> &[u8] {
    match entry.iter().position(|&b| b == b'=') {
        Some(pos) => &entry[..pos],
        None => entry,
    }
}

fn is_microfat_internal_env(k: &[u8]) -> bool {
    matches!(
        k,
        b"MICROFAT_ORIGINAL_EXE"
            | b"MICROFAT_SELECTED_VARIANT"
            | b"MICROFAT_HOST_ARCH"
            | b"MICROFAT_HOST_LEVEL"
            | b"MICROFAT_EXEC_MODE"
            | b"MICROFAT_DISPATCH_MODE"
            | b"MICROFAT_SELECTED_SHA256"
            | b"MICROFAT_SELECTED_SIZE"
            | b"MICROFAT_POLICY_APPLIED"
            | b"MICROFAT_OVERRIDE_REASON"
            | b"MICROFAT_CGROUP_VERSION"
            | b"MICROFAT_CGROUP_LIMIT_BYTES"
            | b"MICROFAT_CGROUP_HIGH_BYTES"
            | b"MICROFAT_CGROUP_EFFECTIVE_LIMIT_BYTES"
            | b"MICROFAT_CGROUP_CPUS"
            | b"MICROFAT_CGROUP_GOMEMLIMIT"
            | b"MICROFAT_CGROUP_GOMAXPROCS"
            | b"MICROFAT_CGROUP_GOGC"
            | b"MICROFAT_CGROUP_GC_PROFILE"
            | b"MICROFAT_CGROUP_GOGC_SKIPPED_REASON"
    )
}

fn build_exec_env_from(
    base_env: &[&[u8]],
    updates: &[(&[u8], &[u8])],
) -> Vec<Vec<u8>> {
    let mut entries: Vec<Vec<u8>> = Vec::with_capacity(base_env.len() + updates.len());
    for &raw in base_env {
        let key = env_key_of(raw);
        if is_microfat_internal_env(key) || updates.iter().any(|(uk, _)| *uk == key) {
            continue;
        }
        let mut line = Vec::with_capacity(raw.len() + 1);
        line.extend_from_slice(raw);
        line.push(0);
        entries.push(line);
    }
    for (k, v) in updates {
        let mut line = Vec::with_capacity(k.len() + 1 + v.len() + 1);
        line.extend_from_slice(k);
        line.push(b'=');
        line.extend_from_slice(v);
        line.push(0);
        entries.push(line);
    }
    entries
}

fn format_quota_2dec(quota_us: i64, period_us: i64) -> Vec<u8> {
    if quota_us <= 0 || period_us <= 0 {
        return b"0.00".to_vec();
    }
    let mut q = quota_us as u64;
    let mut p = period_us as u64;
    while q > (u64::MAX / 200) && p >= 2 {
        q /= 2;
        p /= 2;
    }
    let scaled = (q.saturating_mul(100).saturating_add(p / 2)) / p.max(1);
    let whole = scaled / 100;
    let frac = (scaled % 100) as u8;
    let mut buf = [0u8; 24];
    let w_dec = format_u64_decimal(whole, &mut buf);
    let mut out = Vec::with_capacity(w_dec.len() + 3);
    out.extend_from_slice(w_dec);
    out.push(b'.');
    out.push(b'0' + (frac / 10));
    out.push(b'0' + (frac % 10));
    out
}

fn collect_ambient_env() -> Vec<&'static [u8]> {
    let mut out = Vec::new();
    // SAFETY: `environ` is a NULL-terminated array of NUL-terminated C strings on Linux.
    let mut cur = unsafe { environ };
    if cur.is_null() {
        return out;
    }
    // SAFETY: iterates until NULL terminator in ambient `environ`.
    unsafe {
        while !(*cur).is_null() {
            let p = *cur;
            let len = libc::strlen(p);
            out.push(core::slice::from_raw_parts(p.cast::<u8>(), len));
            cur = cur.add(1);
        }
    }
    out
}

fn readlink_proc_self_exe(argv0_fallback: &[u8]) -> Vec<u8> {
    let mut buf = [0u8; 4096];
    // SAFETY: static path & writable stack buffer.
    let n = unsafe { libc::readlink(c"/proc/self/exe".as_ptr(), buf.as_mut_ptr().cast(), buf.len()) };
    if n > 0 && (n as usize) <= buf.len() {
        return buf[..n as usize].to_vec();
    }
    argv0_fallback.to_vec()
}

fn env_val_of<'a>(env_lines: &'a [Vec<u8>], key: &[u8]) -> Option<&'a [u8]> {
    for line in env_lines {
        let trimmed = line.strip_suffix(b"\0").unwrap_or(line.as_slice());
        if let Some(rest) = trimmed.strip_prefix(key) {
            if let Some(val) = rest.strip_prefix(b"=") {
                return Some(val);
            }
        }
    }
    None
}

fn build_runtime_exec_env(
    original_exe: &[u8],
    host: HostCaps,
    variant: &SelectedVariant<'_>,
    exec_mode_str: &[u8],
    policy_applied: Option<&'static str>,
    override_reason: Option<&[u8]>,
    limits: Option<CgroupLimits>,
) -> Vec<Vec<u8>> {
    let ambient = collect_ambient_env();
    let ambient_gomemlimit = ambient
        .iter()
        .find(|e| env_key_of(e) == b"GOMEMLIMIT")
        .and_then(|e| e.iter().position(|&b| b == b'=').map(|i| &e[i + 1..]));
    let has_gomemlimit = ambient_gomemlimit.is_some();
    let has_gomaxprocs = ambient.iter().any(|e| env_key_of(e) == b"GOMAXPROCS");
    let has_gogc = ambient.iter().any(|e| env_key_of(e) == b"GOGC");

    let sha_hex = encode_hex_sha256(&variant.sha256);
    let mut size_buf = [0u8; 24];
    let size_bytes = format_u64_decimal(variant.uncompressed_size, &mut size_buf);

    let arch_bytes = host.expected_arch_bytes();
    let mut host_lvl_buf = [0u8; 8];
    let host_lvl_bytes: &[u8] = match host {
        HostCaps::Amd64(r) => {
            host_lvl_buf[0] = b'v';
            host_lvl_buf[1] = b'0' + cmp::min(r, 9);
            &host_lvl_buf[..2]
        }
        HostCaps::Arm64(mask) => highest_arm64_level_str(mask).as_bytes(),
    };

    let mut owned_vals: Vec<(&[u8], Vec<u8>)> = Vec::with_capacity(18);
    owned_vals.push((b"MICROFAT_ORIGINAL_EXE", original_exe.to_vec()));
    owned_vals.push((b"MICROFAT_SELECTED_VARIANT", variant.level.to_vec()));
    owned_vals.push((b"MICROFAT_HOST_ARCH", arch_bytes.to_vec()));
    owned_vals.push((b"MICROFAT_HOST_LEVEL", host_lvl_bytes.to_vec()));
    owned_vals.push((b"MICROFAT_EXEC_MODE", exec_mode_str.to_vec()));
    owned_vals.push((b"MICROFAT_DISPATCH_MODE", exec_mode_str.to_vec()));
    owned_vals.push((b"MICROFAT_SELECTED_SHA256", sha_hex.to_vec()));
    owned_vals.push((b"MICROFAT_SELECTED_SIZE", size_bytes.to_vec()));
    if let Some(pa) = policy_applied {
        owned_vals.push((b"MICROFAT_POLICY_APPLIED", pa.as_bytes().to_vec()));
        if let Some(or_bytes) = override_reason {
            owned_vals.push((b"MICROFAT_OVERRIDE_REASON", or_bytes.to_vec()));
        }
    }

    let autotune = runtime_autotune_enabled(
        getenv_bytes(b"MICROFAT_AUTOTUNE\0"),
        getenv_bytes(b"MICROFAT_DRY_RUN\0"),
    );

    if let Some(lim) = limits {
        let mut ver_buf = [0u8; 24];
        owned_vals.push((
            b"MICROFAT_CGROUP_VERSION",
            format_u64_decimal(lim.version as u64, &mut ver_buf).to_vec(),
        ));

        let gc_profile = parse_gc_profile(getenv_bytes(b"MICROFAT_GC_PROFILE\0"));
        if gc_profile != GcProfile::Default {
            owned_vals.push((
                b"MICROFAT_CGROUP_GC_PROFILE",
                gc_profile.as_str().as_bytes().to_vec(),
            ));
        }

        let ratio_bps = getenv_bytes(b"MICROFAT_MEM_RATIO\0")
            .and_then(parse_ratio_bps)
            .unwrap_or_else(|| profile_default_ratio_bps(gc_profile));

        let mut computed_gomemlimit: Option<i64> = None;
        if let Some(ceiling) = lim.effective_memory_limit {
            if ceiling >= 0 {
                let mut lim_buf = [0u8; 24];
                let ce_bytes = format_u64_decimal(ceiling as u64, &mut lim_buf).to_vec();
                owned_vals.push((b"MICROFAT_CGROUP_LIMIT_BYTES", ce_bytes.clone()));
                owned_vals.push((b"MICROFAT_CGROUP_EFFECTIVE_LIMIT_BYTES", ce_bytes));
            }
            let retained = page_aligned_retained_bytes(
                variant.uncompressed_size,
                exec_mode_str == b"memfd",
            );
            let adj_limit = retained_memory_limit(ceiling, retained);
            if let Some(budget) =
                compute_gomemlimit(adj_limit, ratio_bps, DEFAULT_MIN_HEADROOM_BYTES)
            {
                computed_gomemlimit = Some(budget);
                let mut mem_buf = [0u8; 24];
                let dec = format_u64_decimal(budget as u64, &mut mem_buf);
                let mut val = Vec::with_capacity(dec.len() + 1);
                val.extend_from_slice(dec);
                val.push(b'B');
                owned_vals.push((b"MICROFAT_CGROUP_GOMEMLIMIT", val.clone()));
                if autotune && !has_gomemlimit {
                    owned_vals.push((b"GOMEMLIMIT", val));
                }
            }
        }

        if lim.quota_us > 0 && lim.period_us > 0 {
            owned_vals.push((
                b"MICROFAT_CGROUP_CPUS",
                format_quota_2dec(lim.quota_us, lim.period_us),
            ));
        }

        if resolve_cpu_policy(getenv_bytes(b"MICROFAT_CPU_POLICY\0")) == CpuPolicy::Static {
            if let Some(cpus) = compute_gomaxprocs(lim.quota_us, lim.period_us) {
                let mut cpu_buf = [0u8; 24];
                let val = format_u64_decimal(cpus as u64, &mut cpu_buf).to_vec();
                owned_vals.push((b"MICROFAT_CGROUP_GOMAXPROCS", val.clone()));
                if autotune && !has_gomaxprocs {
                    owned_vals.push((b"GOMAXPROCS", val));
                }
            }
        }

        let eff_batch_limit = ambient_gomemlimit
            .and_then(parse_runtime_memory_limit)
            .or(computed_gomemlimit);
        let live_heap = getenv_bytes(b"MICROFAT_LIVE_HEAP_ESTIMATE\0").and_then(parse_byte_size_i64);
        match resolve_profile_gogc(gc_profile, computed_gomemlimit, eff_batch_limit, live_heap) {
            GogcOutcome::Value(g) => {
                let mut g_buf = [0u8; 24];
                let g_val = format_u64_decimal(g as u64, &mut g_buf).to_vec();
                owned_vals.push((b"MICROFAT_CGROUP_GOGC", g_val.clone()));
                if autotune && !has_gogc {
                    owned_vals.push((b"GOGC", g_val));
                }
            }
            GogcOutcome::Off => {
                owned_vals.push((b"MICROFAT_CGROUP_GOGC", b"off".to_vec()));
                if autotune && !has_gogc {
                    owned_vals.push((b"GOGC", b"off".to_vec()));
                }
            }
            GogcOutcome::SkippedBatchNoLimit => {
                if !has_gogc {
                    owned_vals.push((
                        b"MICROFAT_CGROUP_GOGC_SKIPPED_REASON",
                        b"batch_etl GOGC tuning skipped (no finite effective memory ceiling)"
                            .to_vec(),
                    ));
                }
            }
            GogcOutcome::Unset => {}
        }
    }

    let pairs: Vec<(&[u8], &[u8])> = owned_vals
        .iter()
        .map(|(k, v)| (*k, v.as_slice()))
        .collect();
    build_exec_env_from(&ambient, &pairs)
}

#[allow(clippy::too_many_arguments)]
fn emit_dispatch_telemetry(
    diag: DiagMode,
    host: HostCaps,
    variant: &SelectedVariant<'_>,
    exec_mode_str: &[u8],
    policy_applied: Option<&'static str>,
    override_reason: Option<&[u8]>,
    limits: Option<CgroupLimits>,
    env_lines: &[Vec<u8>],
    cold_extract: bool,
    decomp_ns: u64,
    total_ns: u64,
) {
    if diag == DiagMode::Off {
        return;
    }

    let decomp_us: u64 = if cold_extract {
        (decomp_ns / 1000).max(1)
    } else {
        0
    };
    let total_us: u64 = (total_ns / 1000).max(decomp_us).max(1);

    let arch_bytes = host.expected_arch_bytes();
    let mut host_lvl_buf = [0u8; 8];
    let host_lvl_bytes: &[u8] = match host {
        HostCaps::Amd64(r) => {
            host_lvl_buf[0] = b'v';
            host_lvl_buf[1] = b'0' + cmp::min(r, 9);
            &host_lvl_buf[..2]
        }
        HostCaps::Arm64(mask) => highest_arm64_level_str(mask).as_bytes(),
    };

    let mem_limit = env_val_of(env_lines, b"GOMEMLIMIT").unwrap_or(b"");
    let max_procs = env_val_of(env_lines, b"GOMAXPROCS").unwrap_or(b"");
    let gogc_val = env_val_of(env_lines, b"GOGC").unwrap_or(b"");
    let gc_prof = env_val_of(env_lines, b"MICROFAT_CGROUP_GC_PROFILE").unwrap_or(b"");

    if diag == DiagMode::Json {
        let mut out = Vec::with_capacity(512);
        let mut num_buf = [0u8; 24];
        out.extend_from_slice(b"[microfat] {\"event\":\"dispatch\",\"timestamp_unix_nano\":");
        out.extend_from_slice(format_u64_decimal(realtime_ns() as u64, &mut num_buf));
        out.extend_from_slice(b",\"host_arch\":\"");
        push_json_escaped(&mut out, arch_bytes);
        out.extend_from_slice(b"\",\"host_level\":\"");
        push_json_escaped(&mut out, host_lvl_bytes);
        out.extend_from_slice(b"\",\"selected_variant\":\"");
        push_json_escaped(&mut out, variant.level);
        out.extend_from_slice(b"\",\"selected_sha256\":\"");
        let sha_hex = encode_hex_sha256(&variant.sha256);
        out.extend_from_slice(&sha_hex);
        out.extend_from_slice(b"\",\"selected_size_bytes\":");
        out.extend_from_slice(format_u64_decimal(variant.uncompressed_size, &mut num_buf));
        out.extend_from_slice(b",\"exec_mode\":\"");
        push_json_escaped(&mut out, exec_mode_str);
        out.push(b'"');
        if let Some(pa) = policy_applied {
            out.extend_from_slice(b",\"policy_applied\":\"");
            push_json_escaped(&mut out, pa.as_bytes());
            out.push(b'"');
        }
        if let Some(or_bytes) = override_reason {
            out.extend_from_slice(b",\"policy_reason\":\"");
            push_json_escaped(&mut out, or_bytes);
            out.push(b'"');
        }
        if let Some(lim) = limits {
            out.extend_from_slice(b",\"cgroup_version\":");
            out.extend_from_slice(format_u64_decimal(lim.version as u64, &mut num_buf));
            if let Some(ce) = lim.effective_memory_limit {
                if ce >= 0 {
                    out.extend_from_slice(b",\"cgroup_mem_limit_bytes\":");
                    out.extend_from_slice(format_u64_decimal(ce as u64, &mut num_buf));
                }
            }
            if lim.quota_us > 0 && lim.period_us > 0 {
                out.extend_from_slice(b",\"cgroup_cpu_quota\":");
                out.extend_from_slice(&format_quota_2dec(lim.quota_us, lim.period_us));
            }
        }
        if !mem_limit.is_empty() {
            out.extend_from_slice(b",\"gomemlimit\":\"");
            push_json_escaped(&mut out, mem_limit);
            out.push(b'"');
        }
        if !max_procs.is_empty() {
            out.extend_from_slice(b",\"gomaxprocs\":\"");
            push_json_escaped(&mut out, max_procs);
            out.push(b'"');
        }
        if !gogc_val.is_empty() {
            out.extend_from_slice(b",\"gogc\":\"");
            push_json_escaped(&mut out, gogc_val);
            out.push(b'"');
        }
        if !gc_prof.is_empty() {
            out.extend_from_slice(b",\"gc_profile\":\"");
            push_json_escaped(&mut out, gc_prof);
            out.push(b'"');
        }
        if decomp_us > 0 {
            out.extend_from_slice(b",\"decompression_duration_us\":");
            out.extend_from_slice(format_u64_decimal(decomp_us, &mut num_buf));
        }
        out.extend_from_slice(b",\"total_launcher_us\":");
        out.extend_from_slice(format_u64_decimal(total_us, &mut num_buf));
        out.extend_from_slice(b"}\n");
        write_stderr(&out);
        return;
    }

    let mut out = Vec::with_capacity(256);
    let mut num_buf = [0u8; 24];
    out.extend_from_slice(b"[microfat:debug] host_arch=");
    out.extend_from_slice(arch_bytes);
    out.extend_from_slice(b" host_level=");
    out.extend_from_slice(host_lvl_bytes);
    out.extend_from_slice(b" selected_variant=");
    out.extend_from_slice(variant.level);
    out.extend_from_slice(b" exec_mode=");
    out.extend_from_slice(exec_mode_str);
    out.extend_from_slice(b" gomemlimit=");
    out.extend_from_slice(mem_limit);
    out.extend_from_slice(b" gomaxprocs=");
    out.extend_from_slice(max_procs);
    if !gogc_val.is_empty() {
        out.extend_from_slice(b" gogc=");
        out.extend_from_slice(gogc_val);
    }
    if !gc_prof.is_empty() {
        out.extend_from_slice(b" gc_profile=");
        out.extend_from_slice(gc_prof);
    }
    if let Some(pa) = policy_applied {
        out.extend_from_slice(b" policy=");
        out.extend_from_slice(pa.as_bytes());
        out.extend_from_slice(b" policy_reason=\"");
        out.extend_from_slice(override_reason.unwrap_or(b""));
        out.push(b'"');
    }
    out.extend_from_slice(b" decompress_us=");
    out.extend_from_slice(format_u64_decimal(decomp_us, &mut num_buf));
    out.extend_from_slice(b" total_us=");
    out.extend_from_slice(format_u64_decimal(total_us, &mut num_buf));
    out.push(b'\n');
    write_stderr(&out);
}

fn validate_stat_metadata(
    st: &libc::stat,
    euid: libc::uid_t,
    must_be_dir: bool,
) -> Result<(), CacheError> {
    let kind = st.st_mode & libc::S_IFMT;
    let expected_kind = if must_be_dir {
        libc::S_IFDIR
    } else {
        libc::S_IFREG
    };
    if kind != expected_kind {
        return Err(CacheError::Unsafe(if must_be_dir {
            "cache directory path is not a directory"
        } else {
            "unsafe non-regular cache entry"
        }));
    }
    if st.st_uid != euid {
        return Err(CacheError::Unsafe(if must_be_dir {
            "cache directory owned by unexpected uid"
        } else {
            "cache entry owned by unexpected uid"
        }));
    }
    if (st.st_mode & UNSAFE_CACHE_MODE_BITS) != 0 {
        return Err(CacheError::Unsafe(if must_be_dir {
            "cache directory has unsafe write or special mode bits"
        } else {
            "cache entry has unsafe write or special mode bits"
        }));
    }
    Ok(())
}

fn open_secure_cache_dir(
    dir_bytes: &[u8],
    euid: libc::uid_t,
    tighten: bool,
) -> Result<Option<OwnedFd>, CacheError> {
    let c_dir = bytes_to_cstr(dir_bytes)?;
    let flags = libc::O_RDONLY | libc::O_DIRECTORY | libc::O_NOFOLLOW | libc::O_CLOEXEC;
    // SAFETY: `c_dir` is a valid NUL-terminated C string.
    let raw_fd = unsafe { libc::open(c_dir.as_ptr().cast(), flags) };
    if raw_fd < 0 {
        return match last_errno() {
            libc::ENOENT => Ok(None),
            libc::ELOOP => Err(CacheError::Unsafe("unsafe cache directory symlink")),
            libc::ENOTDIR => Err(CacheError::Unsafe("cache directory path is not a directory")),
            _ => Ok(None),
        };
    }
    let fd = OwnedFd::from_raw(raw_fd);
    let st = fstat_fd(fd.as_raw_fd()).ok_or(CacheError::Unsafe("unable to fstat cache directory"))?;
    match validate_stat_metadata(&st, euid, true) {
        Ok(()) => Ok(Some(fd)),
        Err(_) if tighten && (st.st_mode & libc::S_IFMT) == libc::S_IFDIR && st.st_uid == euid => {
            // SAFETY: open descriptor to own-UID directory tightened to 0700.
            if unsafe { libc::fchmod(fd.as_raw_fd(), 0o700) } != 0 {
                return Err(CacheError::Unsafe("cannot tighten cache directory mode"));
            }
            let st2 = fstat_fd(fd.as_raw_fd())
                .ok_or(CacheError::Unsafe("unable to re-fstat cache directory"))?;
            validate_stat_metadata(&st2, euid, true)?;
            Ok(Some(fd))
        }
        Err(e) => Err(e),
    }
}

fn mkdir_p_0700(dir_bytes: &[u8]) {
    if !dir_bytes.starts_with(b"/") || dir_bytes.contains(&0) {
        return;
    }
    let mut cur = Vec::with_capacity(dir_bytes.len() + 1);
    for comp in dir_bytes[1..].split(|&b| b == b'/') {
        if comp.is_empty() {
            continue;
        }
        cur.push(b'/');
        cur.extend_from_slice(comp);
        cur.push(0);
        // SAFETY: `cur` is NUL-terminated; fails cleanly with EEXIST if component exists.
        unsafe {
            let _ = libc::mkdir(cur.as_ptr().cast(), 0o700);
        }
        cur.pop();
    }
}

fn validate_cache_entry_at_fd(
    dir_fd: libc::c_int,
    expected_sha256: &[u8; 32],
    expected_size: u64,
    euid: libc::uid_t,
) -> Result<Option<OwnedFd>, CacheError> {
    let Ok(expected_size_i64) = i64::try_from(expected_size) else {
        return Ok(None);
    };
    let hex_name = encode_hex_sha256(expected_sha256);
    let mut name_buf = [0u8; 65];
    name_buf[..64].copy_from_slice(&hex_name);
    let name_ptr = name_buf.as_ptr().cast::<libc::c_char>();

    let flags = libc::O_RDONLY | libc::O_NOFOLLOW | libc::O_NONBLOCK | libc::O_CLOEXEC;
    // SAFETY: `dir_fd` is a validated open directory fd and `name_ptr` is NUL-terminated.
    let raw_fd = unsafe { libc::openat(dir_fd, name_ptr, flags) };
    if raw_fd < 0 {
        let errno = last_errno();
        if errno == libc::ENOENT {
            return Ok(None);
        }
        if errno == libc::ELOOP {
            return Err(CacheError::Unsafe("refusal to execute symlink at cache entry"));
        }
        // SAFETY: `libc::stat` is POD populated on success by `libc::fstatat`.
        let mut st_link: libc::stat = unsafe { core::mem::zeroed() };
        // SAFETY: `dir_fd` is valid and `name_ptr` is NUL-terminated.
        if unsafe { libc::fstatat(dir_fd, name_ptr, &mut st_link, libc::AT_SYMLINK_NOFOLLOW) } != 0
        {
            return Err(CacheError::Unsafe("cannot inspect cache entry after open failure"));
        }
        validate_stat_metadata(&st_link, euid, false)?;
        return Ok(None);
    }

    let entry_fd = OwnedFd::from_raw(raw_fd);
    let st =
        fstat_fd(entry_fd.as_raw_fd()).ok_or(CacheError::Unsafe("unable to fstat cache entry"))?;
    validate_stat_metadata(&st, euid, false)?;

    if (st.st_mode & libc::S_IXUSR) == 0 || st.st_size != expected_size_i64 {
        return Ok(None);
    }
    let Ok(actual_sha) = hash_fd_stream(entry_fd.as_raw_fd(), expected_size) else {
        return Ok(None);
    };
    if actual_sha != *expected_sha256 {
        return Ok(None);
    }
    Ok(Some(entry_fd))
}

fn validate_cache_replacement_at(
    dir_fd: libc::c_int,
    name_cstr: &[u8],
    euid: libc::uid_t,
) -> Result<(), CacheError> {
    // SAFETY: `libc::stat` is POD populated by `libc::fstatat`.
    let mut st: libc::stat = unsafe { core::mem::zeroed() };
    // SAFETY: `dir_fd` is valid open dir and `name_cstr` is NUL-terminated.
    if unsafe {
        libc::fstatat(
            dir_fd,
            name_cstr.as_ptr().cast(),
            &mut st,
            libc::AT_SYMLINK_NOFOLLOW,
        )
    } != 0
    {
        if last_errno() == libc::ENOENT {
            return Ok(());
        }
        return Err(CacheError::Unsafe("cannot inspect cache replacement target"));
    }
    validate_stat_metadata(&st, euid, false)
}

fn append_sub_dir(base: &[u8], suffix: &[u8]) -> Vec<u8> {
    let mut out = Vec::with_capacity(base.len() + 1 + suffix.len());
    out.extend_from_slice(base);
    if !base.ends_with(b"/") {
        out.push(b'/');
    }
    out.extend_from_slice(suffix);
    out
}

fn cache_dir_candidates(euid: libc::uid_t) -> Result<(Vec<Vec<u8>>, bool), CacheError> {
    if let Some(explicit) = getenv_bytes(b"MICROFAT_CACHE_DIR\0") {
        let trimmed = trim_ascii_bytes(explicit);
        if !trimmed.is_empty() {
            if !trimmed.starts_with(b"/") {
                return Err(CacheError::Unsafe("MICROFAT_CACHE_DIR must be absolute"));
            }
            return Ok((vec![trimmed.to_vec()], true));
        }
    }

    let mut candidates = Vec::with_capacity(2);
    if let Some(xdg) = getenv_bytes(b"XDG_CACHE_HOME\0").map(trim_ascii_bytes) {
        if xdg.starts_with(b"/") {
            candidates.push(append_sub_dir(xdg, b"microfat"));
        }
    } else if let Some(home) = getenv_bytes(b"HOME\0").map(trim_ascii_bytes) {
        if home.starts_with(b"/") {
            let dot_cache = append_sub_dir(home, b".cache");
            candidates.push(append_sub_dir(&dot_cache, b"microfat"));
        }
    }

    let tmp_root = getenv_bytes(b"TMPDIR\0")
        .map(trim_ascii_bytes)
        .filter(|s| s.starts_with(b"/"))
        .unwrap_or(b"/tmp");
    let mut uid_buf = [0u8; 24];
    let uid_dec = format_u64_decimal(euid as u64, &mut uid_buf);
    let mut leaf = Vec::with_capacity(10 + uid_dec.len());
    leaf.extend_from_slice(b".microfat-");
    leaf.extend_from_slice(uid_dec);
    candidates.push(append_sub_dir(tmp_root, &leaf));
    Ok((candidates, false))
}

fn try_warm_cache(
    expected_sha256: &[u8; 32],
    expected_size: u64,
) -> Result<CacheOutcome, CacheError> {
    // SAFETY: `libc::geteuid` takes no pointers and cannot fail.
    let euid = unsafe { libc::geteuid() };
    let (candidates, explicit) = match cache_dir_candidates(euid) {
        Ok(c) => c,
        Err(_) => return Ok(CacheOutcome::Miss),
    };
    for dir in &candidates {
        let dir_fd = match open_secure_cache_dir(dir, euid, false) {
            Ok(Some(fd)) => fd,
            Ok(None) | Err(_) => {
                if explicit {
                    return Ok(CacheOutcome::Miss);
                }
                continue;
            }
        };
        return match validate_cache_entry_at_fd(
            dir_fd.as_raw_fd(),
            expected_sha256,
            expected_size,
            euid,
        )? {
            Some(entry_fd) => Ok(CacheOutcome::Hit(entry_fd)),
            None => Ok(CacheOutcome::Miss),
        };
    }
    Ok(CacheOutcome::Miss)
}

fn ensure_secure_cache_dir(euid: libc::uid_t) -> Result<OwnedFd, CacheError> {
    let (candidates, explicit) = cache_dir_candidates(euid)?;
    if explicit {
        let dir = &candidates[0];
        mkdir_p_0700(dir);
        return match open_secure_cache_dir(dir, euid, false)? {
            Some(fd) => Ok(fd),
            None => Err(CacheError::Unsafe("cannot open or create MICROFAT_CACHE_DIR")),
        };
    }
    for dir in &candidates {
        mkdir_p_0700(dir);
        if let Ok(Some(fd)) = open_secure_cache_dir(dir, euid, true) {
            return Ok(fd);
        }
    }
    Err(CacheError::Unsafe("unable to resolve secure cache directory"))
}

struct TempUnlinkGuard<'a> {
    dir_fd: libc::c_int,
    tmp_cstr: &'a [u8],
    armed: bool,
}

impl Drop for TempUnlinkGuard<'_> {
    fn drop(&mut self) {
        if self.armed {
            // SAFETY: best-effort cleanup of unpublished temp file inside pinned `dir_fd`.
            unsafe {
                let _ = libc::unlinkat(self.dir_fd, self.tmp_cstr.as_ptr().cast(), 0);
            }
        }
    }
}

#[allow(clippy::too_many_arguments)]
fn materialize_and_open_in_dir_fd(
    dir_fd: libc::c_int,
    euid: libc::uid_t,
    src_fd: libc::c_int,
    mapped: Option<&MappedFile>,
    variant: &SelectedVariant<'_>,
    dict_bytes: Option<&[u8]>,
    zstd_max_window: u64,
) -> Result<OwnedFd, &'static str> {
    let hex_name = encode_hex_sha256(&variant.sha256);
    let mut name_buf = [0u8; 65];
    name_buf[..64].copy_from_slice(&hex_name);

    validate_cache_replacement_at(dir_fd, &name_buf, euid).map_err(|e| match e {
        CacheError::Unsafe(msg) => msg,
    })?;

    let mut tmp_name = [0u8; 27];
    let mut created_fd: Option<OwnedFd> = None;
    for _ in 0..64 {
        let rnd = encode_hex_8(&random_bytes_8());
        tmp_name[..6].copy_from_slice(b".exec-");
        tmp_name[6..22].copy_from_slice(&rnd);
        tmp_name[22..26].copy_from_slice(b".tmp");
        tmp_name[26] = 0;

        let flags = libc::O_RDWR | libc::O_CREAT | libc::O_EXCL | libc::O_CLOEXEC;
        // SAFETY: `dir_fd` is valid and `tmp_name` is NUL-terminated.
        let fd = unsafe { libc::openat(dir_fd, tmp_name.as_ptr().cast(), flags, 0o700) };
        if fd >= 0 {
            created_fd = Some(OwnedFd::from_raw(fd));
            break;
        }
        if last_errno() != libc::EEXIST {
            return Err("cannot create temp cache file");
        }
    }
    let tmp_fd = created_fd.ok_or("exhausted temp cache file name attempts")?;
    let mut guard = TempUnlinkGuard {
        dir_fd,
        tmp_cstr: &tmp_name,
        armed: true,
    };

    let Ok(size_off) = libc::off_t::try_from(variant.uncompressed_size) else {
        return Err("uncompressed size overflows off_t");
    };
    // SAFETY: `tmp_fd` is a newly created writable regular file descriptor.
    if unsafe { libc::ftruncate(tmp_fd.as_raw_fd(), size_off) } != 0 {
        return Err("ftruncate temp cache file failed");
    }

    extract_variant_to_fd(
        src_fd,
        mapped,
        variant,
        dict_bytes,
        zstd_max_window,
        tmp_fd.as_raw_fd(),
    )?;

    // SAFETY: `tmp_fd` is open and owned exclusively before rename.
    if unsafe { libc::fchmod(tmp_fd.as_raw_fd(), 0o700) } != 0 {
        return Err("fchmod temp cache file failed");
    }
    // SAFETY: `tmp_fd` is open.
    if unsafe { libc::fsync(tmp_fd.as_raw_fd()) } != 0 {
        return Err("fsync temp cache file failed");
    }
    drop(tmp_fd);

    // SAFETY: atomic POSIX publication within the same verified `dir_fd`.
    if unsafe {
        libc::renameat(
            dir_fd,
            tmp_name.as_ptr().cast(),
            dir_fd,
            name_buf.as_ptr().cast(),
        )
    } != 0
    {
        return Err("renameat temp cache file failed");
    }
    guard.armed = false;

    match validate_cache_entry_at_fd(dir_fd, &variant.sha256, variant.uncompressed_size, euid) {
        Ok(Some(fd)) => Ok(fd),
        Ok(None) => {
            // SAFETY: remove freshly published regular target whose post-publish verification failed.
            unsafe {
                let _ = libc::unlinkat(dir_fd, name_buf.as_ptr().cast(), 0);
            }
            Err("post-publish cache verification failed")
        }
        Err(CacheError::Unsafe(msg)) => Err(msg),
    }
}

fn create_sealable_memfd(expected_size: u64) -> Result<OwnedFd, i32> {
    let base_flags = (libc::MFD_CLOEXEC | libc::MFD_ALLOW_SEALING) as libc::c_uint;
    // SAFETY: static NUL-terminated string passed to `libc::memfd_create`.
    let mut raw_fd = unsafe { libc::memfd_create(c"microfat-rs".as_ptr(), base_flags | MFD_EXEC) };
    if raw_fd < 0 && last_errno() == libc::EINVAL {
        // SAFETY: retry once without `MFD_EXEC` strictly for older kernels (< 6.3).
        raw_fd = unsafe { libc::memfd_create(c"microfat-rs".as_ptr(), base_flags) };
    }
    if raw_fd < 0 {
        return Err(last_errno());
    }
    let memfd = OwnedFd::from_raw(raw_fd);
    let Ok(size_off) = libc::off_t::try_from(expected_size) else {
        return Err(libc::EOVERFLOW);
    };
    // SAFETY: `memfd` is an open writable memfd descriptor.
    if unsafe { libc::ftruncate(memfd.as_raw_fd(), size_off) } != 0 {
        return Err(last_errno());
    }
    Ok(memfd)
}

fn format_err_msg(err: FormatError) -> &'static str {
    match err {
        FormatError::TooSmall | FormatError::InvalidMagic => {
            "not a valid microfat fat binary (missing magic trailer)"
        }
        FormatError::InvalidIndexOffset => "reading binary manifest: index offset beyond trailer",
        FormatError::InvalidIndexSize => {
            "reading binary manifest: index size does not match trailer boundary"
        }
        FormatError::IndexChecksumMismatch => {
            "reading binary manifest: index SHA-256 checksum mismatch"
        }
        FormatError::TruncatedIndex => "reading binary manifest: truncated binary index",
        FormatError::UnsupportedVersion => "reading binary manifest: unsupported format version",
        FormatError::TrailingBytes => "reading binary manifest: unexpected trailing bytes in index",
        FormatError::InvalidDict => "reading binary manifest: invalid dictionary metadata",
        FormatError::InvalidChecksum => "reading binary manifest: invalid SHA-256 digest string",
        FormatError::InvalidVariantBounds => "validating variant boundaries: invalid bounds",
        FormatError::OverlappingSegments => {
            "validating variant boundaries: variant payloads overlap or are unsorted"
        }
        FormatError::UnsupportedCompression => "unsupported compression codec",
        FormatError::ZeroVariants => "index contains zero variants",
        FormatError::ArchMismatch => "target architecture mismatch",
        FormatError::NoCompatibleVariant => "no compatible microarchitecture variant found for host CPU",
    }
}

fn load_verified_dict<'a>(
    fd: libc::c_int,
    mapped: Option<&'a MappedFile>,
    dict: Option<DictMeta>,
) -> Option<ByteCow<'a>> {
    let d = dict?;
    let dict_len = usize::try_from(d.size).unwrap_or_else(|_| fatal("dictionary size overflow"));
    let data = read_range(fd, mapped, d.offset, dict_len, "failed reading shared dictionary");
    if FastSha256::digest(data.as_slice()) != d.sha256 {
        fatal("shared dictionary SHA-256 checksum mismatch");
    }
    Some(data)
}

fn format_proc_fd_path(fd: libc::c_int, buf: &mut [u8; 32]) -> *const libc::c_char {
    const PREFIX: &[u8] = b"/proc/self/fd/";
    buf[..PREFIX.len()].copy_from_slice(PREFIX);
    let mut tmp = [0u8; 24];
    let dec = format_u64_decimal(fd as u64, &mut tmp);
    let total = PREFIX.len() + dec.len();
    buf[PREFIX.len()..total].copy_from_slice(dec);
    buf[total] = 0;
    buf.as_ptr().cast()
}

fn exec_verified_fd(
    target_fd: OwnedFd,
    argc: libc::c_int,
    argv: *const *const libc::c_char,
    env_lines: &[Vec<u8>],
) -> i32 {
    let mut path_buf = [0u8; 32];
    let path_ptr = format_proc_fd_path(target_fd.as_raw_fd(), &mut path_buf);

    let fallback_argv: [*const libc::c_char; 2] = [c"microfat".as_ptr(), ptr::null()];
    let exec_argv = if argc > 0 && !argv.is_null() {
        argv
    } else {
        fallback_argv.as_ptr()
    };

    let mut envp: Vec<*const libc::c_char> = Vec::with_capacity(env_lines.len() + 1);
    for line in env_lines {
        envp.push(line.as_ptr().cast());
    }
    envp.push(ptr::null());

    let raw_fd = target_fd.into_raw();
    // SAFETY: `path_ptr`, `exec_argv`, and `envp` are valid NULL-terminated arrays/strings.
    unsafe {
        libc::execve(path_ptr, exec_argv, envp.as_ptr());
    }
    let err = last_errno();
    // SAFETY: `raw_fd` was moved out before failed `execve`; close it cleanly.
    unsafe {
        let _ = libc::close(raw_fd);
    }
    err
}

fn argv_bytes(argc: libc::c_int, argv: *const *const libc::c_char, idx: usize) -> Option<&'static [u8]> {
    if argv.is_null() || argc <= 0 || (idx as libc::c_int) >= argc {
        return None;
    }
    // SAFETY: `idx < argc` and caller guarantees `argv` has at least `argc` pointers.
    let p = unsafe { *argv.add(idx) };
    if p.is_null() {
        return None;
    }
    // SAFETY: valid NUL-terminated C string in `argv`.
    let len = unsafe { libc::strlen(p) };
    // SAFETY: readable slice of `len` bytes.
    Some(unsafe { core::slice::from_raw_parts(p.cast::<u8>(), len) })
}

#[cfg_attr(not(test), allow(dead_code))]
fn run_launcher(argc: libc::c_int, argv: *const *const libc::c_char) {
    check_unprivileged();

    let diag_mode = resolve_diag_mode();
    let start_ns = if diag_mode != DiagMode::Off {
        monotonic_ns()
    } else {
        0
    };

    let exe_fd = open_cstr_readonly(c"/proc/self/exe".as_ptr())
        .unwrap_or_else(|_| fatal("cannot open /proc/self/exe"));
    let st = fstat_fd(exe_fd.as_raw_fd()).unwrap_or_else(|| fatal("cannot stat /proc/self/exe"));
    if (st.st_mode & libc::S_IFMT) != libc::S_IFREG {
        fatal("/proc/self/exe is not a regular file");
    }
    let Ok(file_size) = u64::try_from(st.st_size) else {
        fatal("negative executable size");
    };
    if file_size < TRAILER_SIZE as u64 {
        fatal(format_err_msg(FormatError::TooSmall));
    }

    let mapped = MappedFile::try_map_fd(exe_fd.as_raw_fd(), file_size);
    let trailer_cow = read_range(
        exe_fd.as_raw_fd(),
        mapped.as_ref(),
        file_size - TRAILER_SIZE as u64,
        TRAILER_SIZE,
        "cannot read trailer",
    );
    let trailer_arr: &[u8; TRAILER_SIZE] = trailer_cow
        .as_slice()
        .try_into()
        .unwrap_or_else(|_| fatal("cannot read trailer"));
    let trailer = parse_trailer(file_size, trailer_arr).unwrap_or_else(|e| fatal(format_err_msg(e)));

    let index_cow = read_range(
        exe_fd.as_raw_fd(),
        mapped.as_ref(),
        trailer.index_offset,
        trailer.index_size,
        "cannot read index",
    );
    let index_slice = index_cow.as_slice();
    if index_slice.len() != trailer.index_size {
        fatal(format_err_msg(FormatError::InvalidIndexSize));
    }
    if FastSha256::digest(index_slice) != trailer.expected_index_sha256 {
        fatal(format_err_msg(FormatError::IndexChecksumMismatch));
    }

    let is_meta_cmd = argv_bytes(argc, argv, 1).is_some_and(|a| a.starts_with(b"--microfat:"));
    let host_caps = detect_host_caps();
    let (policy, policy_applied, override_reason) = resolve_selection_policy_full(
        host_caps,
        getenv_bytes(b"MICROFAT_FORCE_LEVEL\0"),
        getenv_bytes(b"MICROFAT_MAX_LEVEL\0"),
        getenv_bytes(b"MICROFAT_DISABLE_VARIANTS\0"),
        getenv_bytes(b"MICROFAT_POLICY\0"),
        getenv_bytes(b"MICROFAT_AVX512_DOWNCLOCK_PROTECTION\0"),
        is_skylake_x_or_cascade_lake(),
    );
    let validated = match parse_and_select_v2_index_with_policy(
        index_slice,
        trailer.index_offset,
        host_caps.expected_arch_bytes(),
        host_caps,
        &policy,
    ) {
        Ok(v) => v,
        Err(FormatError::NoCompatibleVariant) => {
            if is_meta_cmd {
                fatal("meta-commands are disabled in minimal launcher stub profile");
            }
            match policy.force_rank {
                Some(0) => fatal("selecting compatible CPU microarchitecture variant: unknown forced level"),
                Some(r) if !is_compatible_level(host_caps, r) => fatal(
                    "selecting compatible CPU microarchitecture variant: forced variant is incompatible with host CPU hardware",
                ),
                Some(_) => fatal(
                    "selecting compatible CPU microarchitecture variant: requested variant is not embedded in the fat binary",
                ),
                None => fatal(format_err_msg(FormatError::NoCompatibleVariant)),
            }
        }
        Err(e) => fatal(format_err_msg(e)),
    };

    if is_meta_cmd {
        fatal("meta-commands are disabled in minimal launcher stub profile");
    }

    let argv0_fallback = argv_bytes(argc, argv, 0).unwrap_or(b"microfat");
    let original_exe = readlink_proc_self_exe(argv0_fallback);
    let cgroup_limits = resolve_cgroup_limits_custom(b"/sys/fs/cgroup", b"/proc/self/cgroup\0");
    let exec_mode = resolve_exec_mode();

    if exec_mode == ExecMode::Cache {
        let hit_fd = match try_warm_cache(
            &validated.selected.sha256,
            validated.selected.uncompressed_size,
        ) {
            Ok(CacheOutcome::Hit(fd)) => Some(fd),
            Ok(CacheOutcome::Miss) => None,
            Err(CacheError::Unsafe(reason)) => fatal_pair("unsafe cache state", reason),
        };
        let mut decomp_ns: u64 = 0;
        let mut cold_extract = false;
        let target_fd = match hit_fd {
            Some(fd) => fd,
            None => {
                cold_extract = true;
                let zstd_max_win = match check_extraction_budget(
                    cgroup_limits,
                    exe_fd.as_raw_fd(),
                    mapped.as_ref(),
                    &validated.selected,
                    validated.dict,
                ) {
                    Ok(w) => w,
                    Err(msg) => fatal(msg),
                };
                let dict_bytes =
                    load_verified_dict(exe_fd.as_raw_fd(), mapped.as_ref(), validated.dict);
                // SAFETY: `libc::geteuid` takes no arguments and cannot fail.
                let euid = unsafe { libc::geteuid() };
                let dir_fd = ensure_secure_cache_dir(euid)
                    .unwrap_or_else(|CacheError::Unsafe(msg)| fatal_pair("cache init failed", msg));
                let ds = if diag_mode != DiagMode::Off {
                    monotonic_ns()
                } else {
                    0
                };
                let fd = materialize_and_open_in_dir_fd(
                    dir_fd.as_raw_fd(),
                    euid,
                    exe_fd.as_raw_fd(),
                    mapped.as_ref(),
                    &validated.selected,
                    dict_bytes.as_ref().map(ByteCow::as_slice),
                    zstd_max_win,
                )
                .unwrap_or_else(|msg| fatal_pair("cache materialization failed", msg));
                if diag_mode != DiagMode::Off {
                    decomp_ns = monotonic_ns().saturating_sub(ds);
                }
                drop(dict_bytes);
                fd
            }
        };
        let env_lines = build_runtime_exec_env(
            &original_exe,
            host_caps,
            &validated.selected,
            b"cache",
            policy_applied,
            override_reason.as_deref(),
            cgroup_limits,
        );
        let total_ns = if diag_mode != DiagMode::Off {
            monotonic_ns().saturating_sub(start_ns)
        } else {
            0
        };
        emit_dispatch_telemetry(
            diag_mode,
            host_caps,
            &validated.selected,
            b"cache",
            policy_applied,
            override_reason.as_deref(),
            cgroup_limits,
            &env_lines,
            cold_extract,
            decomp_ns,
            total_ns,
        );
        drop(index_cow);
        drop(trailer_cow);
        drop(mapped);
        drop(exe_fd);
        let _err = exec_verified_fd(target_fd, argc, argv, &env_lines);
        fatal("execve cached descriptor failed");
    }

    let mut warm_hit_exec_failed = false;
    if exec_mode == ExecMode::Auto {
        match try_warm_cache(
            &validated.selected.sha256,
            validated.selected.uncompressed_size,
        ) {
            Ok(CacheOutcome::Hit(cached_fd)) => {
                let env_lines = build_runtime_exec_env(
                    &original_exe,
                    host_caps,
                    &validated.selected,
                    b"cache",
                    policy_applied,
                    override_reason.as_deref(),
                    cgroup_limits,
                );
                let total_ns = if diag_mode != DiagMode::Off {
                    monotonic_ns().saturating_sub(start_ns)
                } else {
                    0
                };
                emit_dispatch_telemetry(
                    diag_mode,
                    host_caps,
                    &validated.selected,
                    b"cache",
                    policy_applied,
                    override_reason.as_deref(),
                    cgroup_limits,
                    &env_lines,
                    false,
                    0,
                    total_ns,
                );
                let _err = exec_verified_fd(cached_fd, argc, argv, &env_lines);
                warm_hit_exec_failed = true;
            }
            Ok(CacheOutcome::Miss) => {}
            Err(CacheError::Unsafe(reason)) => fatal_pair("unsafe warm cache state", reason),
        }
    }

    // Extraction budget & payload errors are fatal even in Auto mode (never mask corruption with cache).
    let zstd_max_win = match check_extraction_budget(
        cgroup_limits,
        exe_fd.as_raw_fd(),
        mapped.as_ref(),
        &validated.selected,
        validated.dict,
    ) {
        Ok(w) => w,
        Err(msg) => fatal(msg),
    };

    let dict_bytes = load_verified_dict(exe_fd.as_raw_fd(), mapped.as_ref(), validated.dict);
    let memfd_res = create_sealable_memfd(validated.selected.uncompressed_size);
    let memfd_failed = match memfd_res {
        Ok(memfd) => {
            let ds = if diag_mode != DiagMode::Off {
                monotonic_ns()
            } else {
                0
            };
            if let Err(msg) = extract_variant_to_fd(
                exe_fd.as_raw_fd(),
                mapped.as_ref(),
                &validated.selected,
                dict_bytes.as_ref().map(ByteCow::as_slice),
                zstd_max_win,
                memfd.as_raw_fd(),
            ) {
                fatal_pair("decompressing variant payload", msg);
            }
            let decomp_ns = if diag_mode != DiagMode::Off {
                monotonic_ns().saturating_sub(ds)
            } else {
                0
            };
            // SAFETY: `memfd` is an open memfd with MFD_ALLOW_SEALING and writable view unmapped.
            if unsafe { libc::fcntl(memfd.as_raw_fd(), libc::F_ADD_SEALS, SEAL_MASK) } != 0 {
                true
            } else {
                let env_lines = build_runtime_exec_env(
                    &original_exe,
                    host_caps,
                    &validated.selected,
                    b"memfd",
                    policy_applied,
                    override_reason.as_deref(),
                    cgroup_limits,
                );
                let total_ns = if diag_mode != DiagMode::Off {
                    monotonic_ns().saturating_sub(start_ns)
                } else {
                    0
                };
                emit_dispatch_telemetry(
                    diag_mode,
                    host_caps,
                    &validated.selected,
                    b"memfd",
                    policy_applied,
                    override_reason.as_deref(),
                    cgroup_limits,
                    &env_lines,
                    true,
                    decomp_ns,
                    total_ns,
                );
                let _exec_err = exec_verified_fd(memfd, argc, argv, &env_lines);
                true
            }
        }
        Err(_) => true,
    };

    if memfd_failed && exec_mode == ExecMode::Auto && !warm_hit_exec_failed {
        if diag_mode == DiagMode::Debug {
            write_stderr(
                b"[microfat:debug] memfd_create is unsupported or denied; falling back to cache\n",
            );
        }
        // SAFETY: `libc::geteuid` takes no arguments and cannot fail.
        let euid = unsafe { libc::geteuid() };
        let dir_fd = ensure_secure_cache_dir(euid)
            .unwrap_or_else(|CacheError::Unsafe(msg)| fatal_pair("cache fallback init failed", msg));
        let ds = if diag_mode != DiagMode::Off {
            monotonic_ns()
        } else {
            0
        };
        let cached_fd = materialize_and_open_in_dir_fd(
            dir_fd.as_raw_fd(),
            euid,
            exe_fd.as_raw_fd(),
            mapped.as_ref(),
            &validated.selected,
            dict_bytes.as_ref().map(ByteCow::as_slice),
            zstd_max_win,
        )
        .unwrap_or_else(|msg| fatal_pair("cache fallback materialization failed", msg));
        let decomp_ns = if diag_mode != DiagMode::Off {
            monotonic_ns().saturating_sub(ds)
        } else {
            0
        };
        let env_lines = build_runtime_exec_env(
            &original_exe,
            host_caps,
            &validated.selected,
            b"cache",
            policy_applied,
            override_reason.as_deref(),
            cgroup_limits,
        );
        let total_ns = if diag_mode != DiagMode::Off {
            monotonic_ns().saturating_sub(start_ns)
        } else {
            0
        };
        emit_dispatch_telemetry(
            diag_mode,
            host_caps,
            &validated.selected,
            b"cache",
            policy_applied,
            override_reason.as_deref(),
            cgroup_limits,
            &env_lines,
            true,
            decomp_ns,
            total_ns,
        );
        drop(dict_bytes);
        drop(index_cow);
        drop(trailer_cow);
        drop(mapped);
        drop(exe_fd);
        let _err = exec_verified_fd(cached_fd, argc, argv, &env_lines);
        fatal("cache fallback execve failed");
    }

    fatal("execve memfd failed");
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs::{self, Permissions};
    use std::os::unix::ffi::OsStrExt;
    use std::os::unix::fs::{symlink, PermissionsExt};
    use std::path::{Path, PathBuf};
    use std::process;
    use std::time::{SystemTime, UNIX_EPOCH};

    fn make_temp_dir(label: &str) -> PathBuf {
        let nanos = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let dir = std::env::temp_dir().join(format!(
            "microfat-rs-test-{label}-{}-{nanos}",
            process::id()
        ));
        fs::create_dir_all(&dir).unwrap();
        fs::set_permissions(&dir, Permissions::from_mode(0o700)).unwrap();
        dir
    }

    fn try_open_warm_cache_in_dir(
        dir: &Path,
        expected_sha256: &[u8; 32],
        expected_size: u64,
        euid: libc::uid_t,
    ) -> Result<CacheOutcome, CacheError> {
        let Some(dir_fd) = open_secure_cache_dir(dir.as_os_str().as_bytes(), euid, false)? else {
            return Ok(CacheOutcome::Miss);
        };
        match validate_cache_entry_at_fd(dir_fd.as_raw_fd(), expected_sha256, expected_size, euid)? {
            Some(fd) => Ok(CacheOutcome::Hit(fd)),
            None => Ok(CacheOutcome::Miss),
        }
    }

    fn build_lz4_test_frame(payload: &[u8], indep: bool, content_cksum: bool, block_cksum: bool) -> Vec<u8> {
        let mut out = Vec::new();
        out.extend_from_slice(&LZ4F_MAGIC.to_le_bytes());
        let mut flg = 0b0100_1000u8; // version 01 + content size
        if indep {
            flg |= 0b0010_0000;
        }
        if block_cksum {
            flg |= 0b0001_0000;
        }
        if content_cksum {
            flg |= 0b0000_0100;
        }
        let bd = 0x40u8; // 64 KiB max block
        out.push(flg);
        out.push(bd);
        out.extend_from_slice(&(payload.len() as u64).to_le_bytes());
        let h_ck = ((XxHash32::oneshot(0, &out[4..]) >> 8) & 0xff) as u8;
        out.push(h_ck);

        for chunk in payload.chunks(32 * 1024) {
            let hdr = (chunk.len() as u32) | 0x8000_0000; // uncompressed block in test helper
            out.extend_from_slice(&hdr.to_le_bytes());
            out.extend_from_slice(chunk);
            if block_cksum {
                out.extend_from_slice(&XxHash32::oneshot(0, chunk).to_le_bytes());
            }
        }
        out.extend_from_slice(&0u32.to_le_bytes());
        if content_cksum {
            out.extend_from_slice(&XxHash32::oneshot(0, payload).to_le_bytes());
        }
        out
    }

    #[test]
    fn resolves_exec_mode_precedence_and_defaults() {
        assert_eq!(resolve_exec_mode_from(None, None), ExecMode::Auto);
        assert_eq!(
            resolve_exec_mode_from(Some(b" "), Some(b"cache")),
            ExecMode::Cache
        );
        assert_eq!(
            resolve_exec_mode_from(Some(b"MEMFD"), Some(b"cache")),
            ExecMode::Memfd
        );
        assert_eq!(
            resolve_exec_mode_from(Some(b" Cache "), Some(b"memfd")),
            ExecMode::Cache
        );
        assert_eq!(
            resolve_exec_mode_from(Some(b"unknown"), Some(b"cache")),
            ExecMode::Auto
        );
    }

    #[test]
    fn capability_and_lz4_slice_verification() {
        assert!(!is_safe_capability_xattr_result(0, 0));
        assert!(!is_safe_capability_xattr_result(12, 0));
        assert!(!is_safe_capability_xattr_result(-1, libc::EACCES));
        assert!(is_safe_capability_xattr_result(-1, libc::ENODATA));
        assert!(is_safe_capability_xattr_result(-1, libc::ENOTSUP));

        let payload = b"hello microfat lz4 zero copy decoder!".repeat(2500);
        let frame = build_lz4_test_frame(&payload, true, true, true);
        let mut dst = vec![0u8; payload.len()];
        let mut h = FastSha256::new();
        let n = decompress_lz4_slice(&frame, &mut dst, &mut h).unwrap();
        assert_eq!(n, payload.len());
        assert_eq!(dst, payload);
        assert_eq!(h.finalize(), FastSha256::digest(&payload));

        // Corrupt content checksum fails closed.
        let mut bad = frame.clone();
        *bad.last_mut().unwrap() ^= 0x55;
        assert!(decompress_lz4_slice(&bad, &mut dst, &mut FastSha256::new()).is_err());

        // Oversized output triggers declared-size refusal.
        let mut small_dst = vec![0u8; payload.len() - 1];
        assert!(decompress_lz4_slice(&frame, &mut small_dst, &mut FastSha256::new()).is_err());
    }

    #[test]
    fn cold_cache_materializes_cleanly_and_blocks_chaos_targets() {
        let euid = unsafe { libc::geteuid() };
        let payload = b"#!/bin/sh\necho microfat-cache-ok\n";
        let sha = FastSha256::digest(payload);

        let dir = make_temp_dir("cold-cache");
        let dir_c = bytes_to_cstr(dir.as_os_str().as_bytes()).unwrap();
        let src_path = dir.join("source.bin");
        fs::write(&src_path, payload).unwrap();
        let src_c = bytes_to_cstr(src_path.as_os_str().as_bytes()).unwrap();
        let src_fd = open_cstr_readonly(src_c.as_ptr().cast()).unwrap();

        let dir_raw = unsafe {
            libc::open(
                dir_c.as_ptr().cast(),
                libc::O_RDONLY | libc::O_DIRECTORY | libc::O_NOFOLLOW | libc::O_CLOEXEC,
            )
        };
        assert!(dir_raw >= 0);
        let dir_fd = OwnedFd::from_raw(dir_raw);

        let variant = SelectedVariant {
            level: b"v3",
            offset: 0,
            compressed_size: payload.len() as u64,
            uncompressed_size: payload.len() as u64,
            sha256: sha,
            compression: Compression::None,
            rank: 3,
        };

        // Cold materialization writes executable 0700 cached file & verifies digest.
        let mat_fd = materialize_and_open_in_dir_fd(
            dir_fd.as_raw_fd(),
            euid,
            src_fd.as_raw_fd(),
            None,
            &variant,
            None,
            MAX_ZSTD_WINDOW,
        )
        .unwrap();
        let mut got = vec![0u8; payload.len()];
        pread_exact(mat_fd.as_raw_fd(), &mut got, 0).unwrap();
        assert_eq!(got.as_slice(), payload);
        drop(mat_fd);

        let hex_name = String::from_utf8(encode_hex_sha256(&sha).to_vec()).unwrap();
        let entry_path = dir.join(&hex_name);
        let meta = fs::symlink_metadata(&entry_path).unwrap();
        assert_eq!(meta.permissions().mode() & 0o777, 0o700);

        // Corrupt source payload leaves zero `.tmp` leftovers and never publishes.
        let mut bad_variant = variant;
        bad_variant.sha256[0] ^= 0xff;
        assert!(materialize_and_open_in_dir_fd(
            dir_fd.as_raw_fd(),
            euid,
            src_fd.as_raw_fd(),
            None,
            &bad_variant,
            None,
            MAX_ZSTD_WINDOW,
        )
        .is_err());
        let left_tmp = fs::read_dir(&dir)
            .unwrap()
            .filter_map(|e| e.ok())
            .any(|e| e.file_name().to_string_lossy().ends_with(".tmp"));
        assert!(!left_tmp, "no .tmp file must survive failed materialization");

        // Warm hit succeeds, FIFO replacement target fails closed.
        assert!(matches!(
            try_open_warm_cache_in_dir(&dir, &sha, payload.len() as u64, euid),
            Ok(CacheOutcome::Hit(_))
        ));
        fs::remove_file(&entry_path).unwrap();
        let entry_c = bytes_to_cstr(entry_path.as_os_str().as_bytes()).unwrap();
        assert_eq!(unsafe { libc::mkfifo(entry_c.as_ptr().cast(), 0o600) }, 0);
        assert!(matches!(
            try_open_warm_cache_in_dir(&dir, &sha, payload.len() as u64, euid),
            Err(CacheError::Unsafe(_))
        ));
        assert!(materialize_and_open_in_dir_fd(
            dir_fd.as_raw_fd(),
            euid,
            src_fd.as_raw_fd(),
            None,
            &variant,
            None,
            MAX_ZSTD_WINDOW,
        )
        .is_err());

        // Symlink & world-writable cache checks match Go security contract.
        fs::remove_file(&entry_path).unwrap();
        symlink(&src_path, &entry_path).unwrap();
        assert!(matches!(
            try_open_warm_cache_in_dir(&dir, &sha, payload.len() as u64, euid),
            Err(CacheError::Unsafe(_))
        ));
        fs::remove_file(&entry_path).unwrap();
        fs::set_permissions(&dir, Permissions::from_mode(0o777)).unwrap();
        assert!(matches!(
            try_open_warm_cache_in_dir(&dir, &sha, payload.len() as u64, euid),
            Err(CacheError::Unsafe(_))
        ));

        let _ = fs::remove_dir_all(&dir);
    }

    #[test]
    fn cgroup_v2_and_v1_fixtures_and_env_preservation() {
        let dir = make_temp_dir("cgv2");
        let root = dir.join("sys_fs_cgroup");
        let leaf = root.join("app.slice").join("svc");
        fs::create_dir_all(&leaf).unwrap();
        fs::write(root.join("cgroup.controllers"), b"memory cpu\n").unwrap();
        fs::write(root.join("memory.max"), b"1073741824\n").unwrap(); // 1 GiB
        fs::write(leaf.join("memory.max"), b"536870912\n").unwrap(); // 512 MiB
        fs::write(leaf.join("memory.high"), b"402653184\n").unwrap(); // 384 MiB tighter
        fs::write(leaf.join("cpu.max"), b"250000 100000\n").unwrap(); // 2 cores

        let proc_cg = dir.join("proc_self_cgroup");
        fs::write(&proc_cg, b"0::/app.slice/svc\n").unwrap();
        let proc_c = bytes_to_cstr(proc_cg.as_os_str().as_bytes()).unwrap();

        let lim = resolve_cgroup_limits_custom(root.as_os_str().as_bytes(), &proc_c).unwrap();
        assert_eq!(lim.version, 2);
        assert_eq!(lim.effective_memory_limit, Some(402_653_184));
        assert_eq!(compute_gomaxprocs(lim.quota_us, lim.period_us), Some(2));

        // Cgroup v1 direct fixture check.
        let v1_dir = make_temp_dir("cgv1");
        let v1_mem = v1_dir.join("memory").join("docker").join("c1");
        let v1_cpu = v1_dir.join("cpu").join("docker").join("c1");
        fs::create_dir_all(&v1_mem).unwrap();
        fs::create_dir_all(&v1_cpu).unwrap();
        fs::write(v1_mem.join("memory.limit_in_bytes"), b"268435456\n").unwrap();
        fs::write(v1_cpu.join("cpu.cfs_quota_us"), b"300000\n").unwrap();
        fs::write(v1_cpu.join("cpu.cfs_period_us"), b"100000\n").unwrap();
        let v1_proc = v1_dir.join("proc_cg");
        fs::write(&v1_proc, b"5:memory:/docker/c1\n3:cpu,cpuacct:/docker/c1\n").unwrap();
        let v1_proc_c = bytes_to_cstr(v1_proc.as_os_str().as_bytes()).unwrap();
        let v1_lim = resolve_cgroup_limits_custom(v1_dir.as_os_str().as_bytes(), &v1_proc_c).unwrap();
        assert_eq!(v1_lim.version, 1);
        assert_eq!(v1_lim.effective_memory_limit, Some(268_435_456));
        assert_eq!(compute_gomaxprocs(v1_lim.quota_us, v1_lim.period_us), Some(3));

        // Env update replaces managed keys and preserves unrelated user vars.
        let merged = build_exec_env_from(
            &[b"PATH=/usr/bin", b"GOMEMLIMIT=128MiB", b"MICROFAT_EXEC_MODE=auto"],
            &[(b"MICROFAT_EXEC_MODE", b"memfd"), (b"GOMAXPROCS", b"2")],
        );
        assert!(merged.iter().any(|x| x == b"PATH=/usr/bin\0"));
        assert!(merged.iter().any(|x| x == b"GOMEMLIMIT=128MiB\0"));
        assert!(merged.iter().any(|x| x == b"MICROFAT_EXEC_MODE=memfd\0"));
        assert!(!merged.iter().any(|x| x == b"MICROFAT_EXEC_MODE=auto\0"));

        let _ = fs::remove_dir_all(&dir);
        let _ = fs::remove_dir_all(&v1_dir);
    }

    #[test]
    fn parses_selection_policy_env_combinations() {
        let (p, reason) = resolve_selection_policy_from(
            TargetArch::Amd64,
            Some(b" v2 "),
            Some(b"v3"),
            Some(b"v3,v4"),
        );
        assert_eq!(p.force_rank, Some(2));
        assert_eq!(reason, Some("force_level"));

        let (p_bad, _) =
            resolve_selection_policy_from(TargetArch::Amd64, Some(b"invalid"), None, None);
        assert_eq!(p_bad.force_rank, Some(0));

        let (p_dis, r_dis) = resolve_selection_policy_from(
            TargetArch::Arm64,
            None,
            Some(b"arm64_v9.0"),
            Some(b" v8.2 , v8.1 "),
        );
        assert_eq!(p_dis.max_rank, Some(90));
        assert!(p_dis.is_disabled(82));
        assert!(p_dis.is_disabled(81));
        assert_eq!(r_dis, Some("max_level"));
    }

    #[test]
    fn diag_mode_zstd_window_and_env_scrubbing() {
        assert_eq!(resolve_diag_mode_from(None, None), DiagMode::Off);
        assert_eq!(resolve_diag_mode_from(Some(b"0"), None), DiagMode::Off);
        assert_eq!(resolve_diag_mode_from(Some(b"FaLsE"), None), DiagMode::Off);
        assert_eq!(resolve_diag_mode_from(Some(b" false "), None), DiagMode::Debug);
        assert_eq!(resolve_diag_mode_from(Some(b"0"), Some(b"0")), DiagMode::Debug);
        assert_eq!(resolve_diag_mode_from(Some(b"0"), Some(b"JsOn")), DiagMode::Json);

        // Internal env vars must be stripped before child exec.
        let scrubbed = build_exec_env_from(
            &[
                b"PATH=/usr/bin",
                b"MICROFAT_POLICY_APPLIED=spoofed",
                b"MICROFAT_CGROUP_GOMEMLIMIT=1B",
                b"MICROFAT_CGROUP_GOGC=999",
            ],
            &[(b"MICROFAT_EXEC_MODE", b"memfd")],
        );
        assert!(scrubbed.iter().any(|x| x == b"PATH=/usr/bin\0"));
        assert!(!scrubbed.iter().any(|x| x.starts_with(b"MICROFAT_POLICY_APPLIED=")));
        assert!(!scrubbed.iter().any(|x| x.starts_with(b"MICROFAT_CGROUP_GOMEMLIMIT=")));
        assert!(!scrubbed.iter().any(|x| x.starts_with(b"MICROFAT_CGROUP_GOGC=")));

        // Inspect Zstd frame window skips skippable frames and rejects > 128 MiB window.
        let dir = make_temp_dir("zstd-win");
        let mut crafted = Vec::new();
        crafted.extend_from_slice(&0x184D_2A50u32.to_le_bytes()); // skippable frame 0
        crafted.extend_from_slice(&4u32.to_le_bytes());
        crafted.extend_from_slice(b"skip");
        crafted.extend_from_slice(&[0x28, 0xB5, 0x2F, 0xFD, 0x00, 0x90, 0x00, 0x00, 0x00]); // 256 MiB
        let p = dir.join("z.bin");
        fs::write(&p, &crafted).unwrap();
        let pc = bytes_to_cstr(p.as_os_str().as_bytes()).unwrap();
        let pfd = open_cstr_readonly(pc.as_ptr().cast()).unwrap();
        let var = SelectedVariant {
            level: b"v1",
            offset: 0,
            compressed_size: crafted.len() as u64,
            uncompressed_size: 16,
            sha256: [0; 32],
            compression: Compression::Zstd,
            rank: 1,
        };
        assert!(inspect_zstd_frame_window(pfd.as_raw_fd(), None, &var).is_err());
        let _ = fs::remove_dir_all(&dir);
    }
}
