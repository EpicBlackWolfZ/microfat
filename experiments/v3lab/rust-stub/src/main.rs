//! Ultra-lean verified Format v2 launcher stub for static Linux x86-64 and ARM64 binaries.

mod cpuid;
mod sha256_hw;

use std::ffi::CString;
use std::fs::File;
use std::io::{self, Read, Write};
use std::os::fd::{AsRawFd, FromRawFd, OwnedFd, RawFd};
use std::os::unix::ffi::OsStrExt;
use std::os::unix::fs::FileExt;
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{self, Command};

use cpuid::detect_host_caps;
use lz4_flex::frame::FrameDecoder as Lz4FrameDecoder;
use microfat_stub_core::format::{
    parse_and_select_v2_index, parse_trailer, Compression, DictMeta, FormatError, SelectedVariant,
    TRAILER_SIZE,
};
use ruzstd::decoding::{Dictionary, FrameDecoder, StreamingDecoder};
use sha256_hw::FastSha256;

const AT_SECURE_KEY: usize = 23;
const IO_BUF_SIZE: usize = 256 * 1024;
const MAX_ZSTD_WINDOW: u64 = 128 * 1024 * 1024;
const SEAL_MASK: libc::c_int =
    libc::F_SEAL_SEAL | libc::F_SEAL_SHRINK | libc::F_SEAL_GROW | libc::F_SEAL_WRITE;
const UNSAFE_CACHE_MODE_BITS: libc::mode_t =
    libc::S_IWGRP | libc::S_IWOTH | libc::S_ISUID | libc::S_ISGID | libc::S_ISVTX;

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

fn fatal(msg: &str) -> ! {
    let _ = writeln!(io::stderr(), "microfat-stub-rs: {msg}");
    process::exit(1)
}

fn clear_errno() {
    // SAFETY: `__errno_location` returns a valid thread-local errno pointer on Linux.
    unsafe {
        *libc::__errno_location() = 0;
    }
}

fn at_secure_nonzero() -> bool {
    clear_errno();
    // SAFETY: `libc::getauxval` takes a kernel auxiliary vector key with no pointer preconditions.
    let val = unsafe { libc::getauxval(libc::AT_SECURE) };
    if val != 0 {
        return true;
    }
    if io::Error::last_os_error().raw_os_error() != Some(libc::ENOENT) {
        return false;
    }

    let auxv = match std::fs::read("/proc/self/auxv") {
        Ok(b) => b,
        Err(_) => fatal("cannot read /proc/self/auxv to verify AT_SECURE"),
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
            fatal("refusing privileged execution (uid/gid mismatch)");
        }
    }
    if at_secure_nonzero() {
        fatal("refusing privileged execution (AT_SECURE != 0)");
    }
}

fn resolve_exec_mode_from(exec_val: Option<&str>, dispatch_val: Option<&str>) -> ExecMode {
    let raw = exec_val
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .or_else(|| dispatch_val.map(str::trim).filter(|s| !s.is_empty()))
        .unwrap_or("");
    if raw.eq_ignore_ascii_case("cache") {
        ExecMode::Cache
    } else if raw.eq_ignore_ascii_case("memfd") {
        ExecMode::Memfd
    } else {
        ExecMode::Auto
    }
}

fn resolve_exec_mode() -> ExecMode {
    let exec_val = std::env::var("MICROFAT_EXEC_MODE").ok();
    let dispatch_val = std::env::var("MICROFAT_DISPATCH_MODE").ok();
    resolve_exec_mode_from(exec_val.as_deref(), dispatch_val.as_deref())
}

struct MappedFile {
    ptr: *mut libc::c_void,
    len: usize,
}

impl MappedFile {
    fn try_map_fd(fd: RawFd, len: u64) -> Option<Self> {
        let len_usize = usize::try_from(len).ok()?;
        if len_usize == 0 || len_usize > isize::MAX as usize {
            return None;
        }
        // SAFETY: `fd` is a valid open regular file of at least `len_usize` bytes;
        // `PROT_READ | MAP_PRIVATE` creates an immutable private mapping unmapped on Drop.
        let ptr = unsafe {
            libc::mmap(
                core::ptr::null_mut(),
                len_usize,
                libc::PROT_READ,
                libc::MAP_PRIVATE,
                fd,
                0,
            )
        };
        if ptr == libc::MAP_FAILED {
            return None;
        }
        Some(Self {
            ptr,
            len: len_usize,
        })
    }

    fn as_slice(&self) -> &[u8] {
        // SAFETY: `self.ptr` points to a live read-only mapping of `self.len` bytes
        // kept immutable for `&self`'s lifetime and released in `Drop`.
        unsafe { core::slice::from_raw_parts(self.ptr.cast::<u8>(), self.len) }
    }
}

impl Drop for MappedFile {
    fn drop(&mut self) {
        if self.len > 0 {
            // SAFETY: `self.ptr` and `self.len` originated from a successful `libc::mmap`.
            unsafe {
                let _ = libc::munmap(self.ptr, self.len);
            }
        }
    }
}

struct FileSectionReader<'a> {
    file: &'a File,
    pos: u64,
    end: u64,
}

impl<'a> FileSectionReader<'a> {
    fn new(file: &'a File, offset: u64, len: u64) -> Self {
        Self {
            file,
            pos: offset,
            end: offset.saturating_add(len),
        }
    }

    fn remaining(&self) -> u64 {
        self.end.saturating_sub(self.pos)
    }
}

impl Read for FileSectionReader<'_> {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        let rem = self.remaining();
        if rem == 0 || buf.is_empty() {
            return Ok(0);
        }
        let max_read = core::cmp::min(buf.len() as u64, rem) as usize;
        let n = self.file.read_at(&mut buf[..max_read], self.pos)?;
        self.pos = self.pos.saturating_add(n as u64);
        Ok(n)
    }
}

enum SectionCursor<'a> {
    Mapped(&'a [u8]),
    File(FileSectionReader<'a>),
}

impl SectionCursor<'_> {
    fn remaining(&self) -> u64 {
        match self {
            Self::Mapped(s) => s.len() as u64,
            Self::File(r) => r.remaining(),
        }
    }
}

impl Read for SectionCursor<'_> {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        match self {
            Self::Mapped(s) => s.read(buf),
            Self::File(r) => r.read(buf),
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
    file: &File,
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
    if file.read_exact_at(&mut buf, offset).is_err() {
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

fn hash_fd_stream(fd: RawFd, expected_size: u64) -> io::Result<[u8; 32]> {
    if let Some(m) = MappedFile::try_map_fd(fd, expected_size) {
        return Ok(FastSha256::digest(m.as_slice()));
    }
    let mut hasher = FastSha256::new();
    let mut buf = vec![0u8; IO_BUF_SIZE];
    let mut total = 0u64;
    loop {
        // SAFETY: `fd` is a valid open descriptor and `buf` is a live mutable slice.
        let n = unsafe { libc::read(fd, buf.as_mut_ptr().cast(), buf.len()) };
        if n < 0 {
            let err = io::Error::last_os_error();
            if err.kind() == io::ErrorKind::Interrupted {
                continue;
            }
            return Err(err);
        }
        if n == 0 {
            break;
        }
        total = total.saturating_add(n as u64);
        if total > expected_size {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "cached entry exceeds expected size",
            ));
        }
        hasher.update(&buf[..n as usize]);
    }
    if total != expected_size {
        return Err(io::Error::new(
            io::ErrorKind::UnexpectedEof,
            "cached entry truncated",
        ));
    }
    Ok(hasher.finalize())
}

fn path_to_cstring(path: &Path) -> Result<CString, CacheError> {
    CString::new(path.as_os_str().as_bytes())
        .map_err(|_| CacheError::Unsafe("cache path contains interior NUL byte"))
}

fn validate_stat_metadata(st: &libc::stat, euid: libc::uid_t, must_be_dir: bool) -> Result<(), CacheError> {
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

fn fstat_fd(fd: RawFd) -> Option<libc::stat> {
    // SAFETY: `libc::stat` is plain-old-data initialized by `libc::fstat` on success.
    let mut st: libc::stat = unsafe { core::mem::zeroed() };
    // SAFETY: `fd` is open and `st` points to writable stack storage.
    if unsafe { libc::fstat(fd, &mut st) } != 0 {
        return None;
    }
    Some(st)
}

fn open_secure_cache_dir(dir: &Path, euid: libc::uid_t) -> Result<Option<OwnedFd>, CacheError> {
    let c_dir = path_to_cstring(dir)?;
    let flags = libc::O_RDONLY | libc::O_DIRECTORY | libc::O_NOFOLLOW | libc::O_CLOEXEC;
    // SAFETY: `c_dir` is a valid NUL-terminated C string.
    let raw_fd = unsafe { libc::open(c_dir.as_ptr(), flags) };
    if raw_fd < 0 {
        let errno = io::Error::last_os_error().raw_os_error().unwrap_or(0);
        return match errno {
            libc::ENOENT => Ok(None),
            libc::ELOOP => Err(CacheError::Unsafe("unsafe cache directory symlink")),
            libc::ENOTDIR => Err(CacheError::Unsafe("cache directory path is not a directory")),
            _ => Ok(None),
        };
    }
    // SAFETY: `raw_fd` was just returned open by `libc::open`.
    let fd = unsafe { OwnedFd::from_raw_fd(raw_fd) };
    let st = fstat_fd(fd.as_raw_fd())
        .ok_or(CacheError::Unsafe("unable to fstat cache directory"))?;
    validate_stat_metadata(&st, euid, true)?;
    Ok(Some(fd))
}

#[cfg(test)]
fn try_open_warm_cache_in_dir(
    dir: &Path,
    expected_sha256: &[u8; 32],
    expected_size: u64,
    euid: libc::uid_t,
) -> Result<CacheOutcome, CacheError> {
    let Some(dir_fd) = open_secure_cache_dir(dir, euid)? else {
        return Ok(CacheOutcome::Miss);
    };
    let Some(outcome) =
        validate_cache_entry_at_fd(dir_fd.as_raw_fd(), expected_sha256, expected_size, euid)?
    else {
        return Ok(CacheOutcome::Miss);
    };
    Ok(CacheOutcome::Hit(outcome))
}

fn validate_cache_entry_at_fd(
    dir_fd: RawFd,
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
    // SAFETY: `dir_fd` is a validated open directory fd and `name_ptr` is 65-byte NUL-terminated ASCII hex.
    let raw_fd = unsafe { libc::openat(dir_fd, name_ptr, flags) };
    if raw_fd < 0 {
        let errno = io::Error::last_os_error().raw_os_error().unwrap_or(0);
        if errno == libc::ENOENT {
            return Ok(None);
        }
        if errno == libc::ELOOP {
            return Err(CacheError::Unsafe("unsafe cache entry symlink"));
        }
        // SAFETY: `libc::stat` is plain-old-data populated on success by `libc::fstatat`.
        let mut st_link: libc::stat = unsafe { core::mem::zeroed() };
        // SAFETY: `dir_fd` is valid and `name_ptr` is NUL-terminated.
        if unsafe { libc::fstatat(dir_fd, name_ptr, &mut st_link, libc::AT_SYMLINK_NOFOLLOW) }
            == 0
        {
            validate_stat_metadata(&st_link, euid, false)?;
        }
        return Ok(None);
    }

    // SAFETY: `raw_fd` was just opened by `libc::openat`.
    let entry_fd = unsafe { OwnedFd::from_raw_fd(raw_fd) };
    let st = fstat_fd(entry_fd.as_raw_fd())
        .ok_or(CacheError::Unsafe("unable to fstat cache entry"))?;
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

fn cache_dir_candidates(euid: libc::uid_t) -> Result<Vec<PathBuf>, CacheError> {
    if let Ok(explicit) = std::env::var("MICROFAT_CACHE_DIR") {
        let trimmed = explicit.trim();
        if !trimmed.is_empty() {
            let p = PathBuf::from(trimmed);
            if !p.is_absolute() {
                return Err(CacheError::Unsafe("MICROFAT_CACHE_DIR must be absolute"));
            }
            return Ok(vec![p]);
        }
    }

    let mut candidates = Vec::with_capacity(2);
    if let Ok(xdg) = std::env::var("XDG_CACHE_HOME") {
        let trimmed = xdg.trim();
        if !trimmed.is_empty() && Path::new(trimmed).is_absolute() {
            candidates.push(Path::new(trimmed).join("microfat"));
        }
    } else if let Ok(home) = std::env::var("HOME") {
        let trimmed = home.trim();
        if !trimmed.is_empty() && Path::new(trimmed).is_absolute() {
            candidates.push(Path::new(trimmed).join(".cache").join("microfat"));
        }
    }

    let tmp_root = std::env::var("TMPDIR")
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty() && Path::new(s).is_absolute())
        .unwrap_or_else(|| "/tmp".to_string());
    candidates.push(Path::new(&tmp_root).join(format!(".microfat-{euid}")));
    Ok(candidates)
}

fn try_warm_cache(expected_sha256: &[u8; 32], expected_size: u64) -> Result<CacheOutcome, CacheError> {
    // SAFETY: `libc::geteuid` takes no pointers and cannot fail.
    let euid = unsafe { libc::geteuid() };
    for dir in cache_dir_candidates(euid)? {
        let Some(dir_fd) = open_secure_cache_dir(&dir, euid)? else {
            continue;
        };
        return match validate_cache_entry_at_fd(dir_fd.as_raw_fd(), expected_sha256, expected_size, euid)? {
            Some(entry_fd) => Ok(CacheOutcome::Hit(entry_fd)),
            None => Ok(CacheOutcome::Miss),
        };
    }
    Ok(CacheOutcome::Miss)
}

struct HashingMemfdSink {
    fd: RawFd,
    hasher: FastSha256,
    written: u64,
    expected_size: u64,
}

impl HashingMemfdSink {
    fn write_all_fd(&self, mut data: &[u8]) -> io::Result<()> {
        while !data.is_empty() {
            // SAFETY: `self.fd` is an open memfd descriptor owned until close or exec.
            let n = unsafe { libc::write(self.fd, data.as_ptr().cast(), data.len()) };
            if n < 0 {
                let err = io::Error::last_os_error();
                if err.kind() == io::ErrorKind::Interrupted {
                    continue;
                }
                return Err(err);
            }
            if n == 0 {
                return Err(io::Error::new(
                    io::ErrorKind::WriteZero,
                    "short write to memfd",
                ));
            }
            data = &data[n as usize..];
        }
        Ok(())
    }
}

impl Write for HashingMemfdSink {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        let next = self
            .written
            .checked_add(buf.len() as u64)
            .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "payload size overflow"))?;
        if next > self.expected_size {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "decompressed payload exceeds declared UncompressedSize",
            ));
        }
        self.write_all_fd(buf)?;
        self.hasher.update(buf);
        self.written = next;
        Ok(buf.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

fn create_sealable_memfd(expected_size: u64) -> OwnedFd {
    // SAFETY: NUL-terminated static string passed cleanly to `memfd_create`.
    let raw_fd = unsafe {
        libc::memfd_create(
            c"microfat-rs".as_ptr(),
            libc::MFD_CLOEXEC | libc::MFD_ALLOW_SEALING,
        )
    };
    if raw_fd < 0 {
        fatal("memfd_create failed");
    }
    // SAFETY: `raw_fd` was just returned open by `libc::memfd_create`.
    let memfd = unsafe { OwnedFd::from_raw_fd(raw_fd) };
    let Ok(size_off) = libc::off_t::try_from(expected_size) else {
        fatal("uncompressed payload size overflows off_t");
    };
    // SAFETY: `memfd` is an open writable memfd descriptor.
    if unsafe { libc::ftruncate(memfd.as_raw_fd(), size_off) } != 0 {
        fatal("ftruncate memfd failed");
    }
    memfd
}

fn format_err_msg(err: FormatError) -> &'static str {
    match err {
        FormatError::TooSmall => "binary smaller than 56-byte trailer",
        FormatError::InvalidMagic => "invalid trailer or index magic",
        FormatError::InvalidIndexOffset => "invalid index offset",
        FormatError::InvalidIndexSize => "invalid index size",
        FormatError::IndexChecksumMismatch => "index SHA-256 mismatch",
        FormatError::TruncatedIndex => "truncated binary index",
        FormatError::UnsupportedVersion => "unsupported format version",
        FormatError::TrailingBytes => "unexpected trailing bytes in index",
        FormatError::InvalidDict => "invalid dictionary metadata",
        FormatError::InvalidChecksum => "invalid SHA-256 digest string",
        FormatError::InvalidVariantBounds => "invalid variant segment bounds",
        FormatError::OverlappingSegments => "overlapping segments in binary",
        FormatError::UnsupportedCompression => "unsupported compression codec",
        FormatError::ZeroVariants => "index contains zero variants",
        FormatError::ArchMismatch => "target architecture mismatch",
        FormatError::NoCompatibleVariant => "no compatible variant for host CPU",
    }
}

fn load_verified_dict<'a>(
    file: &File,
    mapped: Option<&'a MappedFile>,
    dict: Option<DictMeta>,
) -> Option<ByteCow<'a>> {
    let d = dict?;
    let dict_len = usize::try_from(d.size).unwrap_or_else(|_| fatal("dictionary size overflow"));
    let data = read_range(file, mapped, d.offset, dict_len, "failed reading shared dictionary");
    if FastSha256::digest(data.as_slice()) != d.sha256 {
        fatal("shared dictionary SHA-256 mismatch");
    }
    Some(data)
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

fn decompress_zstd_section(
    section: &mut SectionCursor<'_>,
    dict_bytes: Option<&[u8]>,
    sink: &mut HashingMemfdSink,
    buf: &mut [u8],
) -> io::Result<()> {
    let mut frame_dec = FrameDecoder::new();
    frame_dec.set_max_window_size(MAX_ZSTD_WINDOW);
    if let Some(raw_dict) = dict_bytes {
        let parsed = Dictionary::decode_dict(raw_dict)
            .unwrap_or_else(|_| fatal("invalid zstd dictionary"));
        if frame_dec.add_dict(parsed).is_err() {
            fatal("failed registering zstd dictionary");
        }
    }
    loop {
        let mut stream = match StreamingDecoder::new_with_decoder(&mut *section, &mut frame_dec) {
            Ok(s) => s,
            Err(e) => {
                return Err(io::Error::new(io::ErrorKind::InvalidData, e.to_string()));
            }
        };
        stream_copy(&mut stream, sink, buf)?;
        if section.remaining() == 0 {
            return Ok(());
        }
    }
}

fn decompress_variant(
    file: &File,
    mapped: Option<&MappedFile>,
    variant: &SelectedVariant<'_>,
    dict_bytes: Option<&[u8]>,
    sink: &mut HashingMemfdSink,
) {
    let mapped_slice = mapped_variant_slice(mapped, variant);
    if matches!(variant.compression, Compression::None) {
        if let Some(src) = mapped_slice {
            if let Err(err) = sink.write_all(src) {
                fatal(&format!("payload copy failed: {err}"));
            }
            return;
        }
    }

    let mut section = match mapped_slice {
        Some(src) => SectionCursor::Mapped(src),
        None => SectionCursor::File(FileSectionReader::new(
            file,
            variant.offset,
            variant.compressed_size,
        )),
    };
    let mut buf = vec![0u8; IO_BUF_SIZE];

    let copy_res: io::Result<()> = match variant.compression {
        Compression::None => stream_copy(&mut section, sink, &mut buf),
        Compression::Lz4 => {
            let mut dec = Lz4FrameDecoder::new(section);
            stream_copy(&mut dec, sink, &mut buf)
        }
        Compression::Zstd => decompress_zstd_section(&mut section, dict_bytes, sink, &mut buf),
    };

    if let Err(err) = copy_res {
        fatal(&format!("payload decompression failed: {err}"));
    }
}

fn stream_copy(mut r: impl Read, w: &mut impl Write, buf: &mut [u8]) -> io::Result<()> {
    loop {
        let n = r.read(buf)?;
        if n == 0 {
            return Ok(());
        }
        w.write_all(&buf[..n])?;
    }
}

fn exec_verified_fd(fd: OwnedFd, err_prefix: &str) -> ! {
    let raw_fd = fd.as_raw_fd();
    let mut args = std::env::args_os();
    let argv0 = args.next().unwrap_or_default();
    let exec_err = Command::new(format!("/proc/self/fd/{raw_fd}"))
        .arg0(argv0)
        .args(args)
        .exec();
    drop(fd);
    fatal(&format!("{err_prefix}: {exec_err}"));
}

fn main() {
    check_unprivileged();

    let file = File::open("/proc/self/exe").unwrap_or_else(|_| fatal("cannot open /proc/self/exe"));
    let meta = file
        .metadata()
        .unwrap_or_else(|_| fatal("cannot stat /proc/self/exe"));
    if !meta.file_type().is_file() {
        fatal("/proc/self/exe is not a regular file");
    }
    let file_size = meta.len();
    if file_size < TRAILER_SIZE as u64 {
        fatal("binary smaller than 56-byte trailer");
    }

    let mapped = MappedFile::try_map_fd(file.as_raw_fd(), file_size);
    let trailer_cow = read_range(
        &file,
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
        &file,
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

    let host_caps = detect_host_caps();
    let validated = parse_and_select_v2_index(
        index_slice,
        trailer.index_offset,
        host_caps.expected_arch_bytes(),
        host_caps,
    )
    .unwrap_or_else(|e| fatal(format_err_msg(e)));

    let exec_mode = resolve_exec_mode();
    if exec_mode != ExecMode::Memfd {
        match try_warm_cache(&validated.selected.sha256, validated.selected.uncompressed_size) {
            Ok(CacheOutcome::Hit(cached_fd)) => {
                drop(index_cow);
                drop(trailer_cow);
                drop(mapped);
                drop(file);
                exec_verified_fd(cached_fd, "execve cached descriptor failed");
            }
            Ok(CacheOutcome::Miss) => {
                if exec_mode == ExecMode::Cache {
                    fatal("no valid warm cache entry found (read-only cache mode)");
                }
            }
            Err(CacheError::Unsafe(reason)) => {
                fatal(&format!("unsafe warm cache state: {reason}"));
            }
        }
    }

    let dict_bytes = load_verified_dict(&file, mapped.as_ref(), validated.dict);
    let memfd = create_sealable_memfd(validated.selected.uncompressed_size);
    let mut sink = HashingMemfdSink {
        fd: memfd.as_raw_fd(),
        hasher: FastSha256::new(),
        written: 0,
        expected_size: validated.selected.uncompressed_size,
    };

    decompress_variant(
        &file,
        mapped.as_ref(),
        &validated.selected,
        dict_bytes.as_ref().map(ByteCow::as_slice),
        &mut sink,
    );

    if sink.written != validated.selected.uncompressed_size {
        fatal("decompressed payload byte count mismatch");
    }
    if sink.hasher.finalize() != validated.selected.sha256 {
        fatal("decompressed payload SHA-256 verification failed");
    }

    // SAFETY: `memfd` is an open memfd created with MFD_ALLOW_SEALING.
    if unsafe { libc::fcntl(memfd.as_raw_fd(), libc::F_ADD_SEALS, SEAL_MASK) } != 0 {
        fatal("failed applying memfd write/grow/shrink/seal protections");
    }

    drop(dict_bytes);
    drop(index_cow);
    drop(trailer_cow);
    drop(mapped);
    drop(file);
    exec_verified_fd(memfd, "execve memfd failed");
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::fs::{self, Permissions};
    use std::os::unix::fs::{symlink, PermissionsExt};
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

    #[test]
    fn resolves_exec_mode_precedence_and_defaults() {
        assert_eq!(resolve_exec_mode_from(None, None), ExecMode::Auto);
        assert_eq!(resolve_exec_mode_from(Some(" "), Some("cache")), ExecMode::Cache);
        assert_eq!(resolve_exec_mode_from(Some("MEMFD"), Some("cache")), ExecMode::Memfd);
        assert_eq!(resolve_exec_mode_from(Some(" Cache "), Some("memfd")), ExecMode::Cache);
        assert_eq!(resolve_exec_mode_from(Some("unknown"), Some("cache")), ExecMode::Auto);
    }

    #[test]
    fn warm_cache_validates_hits_misses_and_fails_closed_on_unsafe_entries() {
        let euid = unsafe { libc::geteuid() };
        let payload = b"#!/bin/sh\necho ok\n";
        let sha = FastSha256::digest(payload);
        let hex_name = String::from_utf8(encode_hex_sha256(&sha).to_vec()).unwrap();

        let dir = make_temp_dir("cache");
        assert!(matches!(
            try_open_warm_cache_in_dir(&dir, &sha, payload.len() as u64, euid),
            Ok(CacheOutcome::Miss)
        ));

        let entry_path = dir.join(&hex_name);
        fs::write(&entry_path, payload).unwrap();
        fs::set_permissions(&entry_path, Permissions::from_mode(0o600)).unwrap();
        // Missing executable bit triggers clean fallback miss.
        assert!(matches!(
            try_open_warm_cache_in_dir(&dir, &sha, payload.len() as u64, euid),
            Ok(CacheOutcome::Miss)
        ));

        // Valid 0o700 regular file yields verified Hit fd with exact contents.
        fs::set_permissions(&entry_path, Permissions::from_mode(0o700)).unwrap();
        let hit = try_open_warm_cache_in_dir(&dir, &sha, payload.len() as u64, euid).unwrap();
        match hit {
            CacheOutcome::Hit(fd) => {
                let mut file = File::from(fd);
                let mut got = Vec::new();
                file.read_to_end(&mut got).unwrap();
                assert_eq!(got.as_slice(), payload);
            }
            CacheOutcome::Miss => panic!("expected warm cache hit"),
        }

        // Corrupt content (wrong digest) yields clean Miss for memfd fallback.
        let mut corrupt = payload.to_vec();
        corrupt[0] ^= 0xff;
        fs::write(&entry_path, &corrupt).unwrap();
        fs::set_permissions(&entry_path, Permissions::from_mode(0o700)).unwrap();
        assert!(matches!(
            try_open_warm_cache_in_dir(&dir, &sha, payload.len() as u64, euid),
            Ok(CacheOutcome::Miss)
        ));

        // Group-writable regular entry fails closed.
        fs::write(&entry_path, payload).unwrap();
        fs::set_permissions(&entry_path, Permissions::from_mode(0o720)).unwrap();
        assert!(matches!(
            try_open_warm_cache_in_dir(&dir, &sha, payload.len() as u64, euid),
            Err(CacheError::Unsafe(_))
        ));

        // Symlink entry fails closed.
        fs::remove_file(&entry_path).unwrap();
        let target = dir.join("target");
        fs::write(&target, payload).unwrap();
        fs::set_permissions(&target, Permissions::from_mode(0o700)).unwrap();
        symlink(&target, &entry_path).unwrap();
        assert!(matches!(
            try_open_warm_cache_in_dir(&dir, &sha, payload.len() as u64, euid),
            Err(CacheError::Unsafe(_))
        ));

        // Unsafe cache dir permissions fail closed even before entry open.
        fs::remove_file(&entry_path).unwrap();
        fs::set_permissions(&dir, Permissions::from_mode(0o777)).unwrap();
        assert!(matches!(
            try_open_warm_cache_in_dir(&dir, &sha, payload.len() as u64, euid),
            Err(CacheError::Unsafe(_))
        ));

        fs::set_permissions(&dir, Permissions::from_mode(0o700)).unwrap();
        let link_dir = make_temp_dir("linkparent").join("symcache");
        symlink(&dir, &link_dir).unwrap();
        assert!(matches!(
            try_open_warm_cache_in_dir(&link_dir, &sha, payload.len() as u64, euid),
            Err(CacheError::Unsafe(_))
        ));

        let _ = fs::remove_dir_all(link_dir.parent().unwrap());
        let _ = fs::remove_dir_all(&dir);
    }
}
