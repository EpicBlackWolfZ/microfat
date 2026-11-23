//! Ultra-lean verified Format v2 launcher stub prototype for Linux x86-64.

mod cpuid;

use std::fs::File;
use std::io::{self, Read, Write};
use std::os::fd::RawFd;
use std::os::unix::fs::FileExt;
use std::os::unix::process::CommandExt;
use std::process::{self, Command};

use lz4_flex::frame::FrameDecoder as Lz4FrameDecoder;
use microfat_stub_core::format::{
    parse_trailer, verify_and_select_v2, Compression, DictMeta, FormatError, SelectedVariant,
    TRAILER_SIZE,
};
use microfat_stub_core::sha256::Sha256;
use ruzstd::decoding::{Dictionary, FrameDecoder, StreamingDecoder};

const AT_SECURE: usize = 23;
const IO_BUF_SIZE: usize = 64 * 1024;
const MAX_ZSTD_WINDOW: u64 = 128 * 1024 * 1024;
const SEAL_MASK: libc::c_int =
    libc::F_SEAL_SEAL | libc::F_SEAL_SHRINK | libc::F_SEAL_GROW | libc::F_SEAL_WRITE;

fn fatal(msg: &str) -> ! {
    let _ = writeln!(io::stderr(), "microfat-stub-rs: {msg}");
    process::exit(1)
}

fn check_unprivileged() {
    // SAFETY: `getuid`/`geteuid`/`getgid`/`getegid` take no pointers and cannot fail.
    unsafe {
        if libc::getuid() != libc::geteuid() || libc::getgid() != libc::getegid() {
            fatal("refusing privileged execution (uid/gid mismatch)");
        }
    }
    let auxv = match std::fs::read("/proc/self/auxv") {
        Ok(b) => b,
        Err(_) => fatal("cannot read /proc/self/auxv to verify AT_SECURE"),
    };
    let word = core::mem::size_of::<usize>();
    let stride = word * 2;
    let mut off = 0usize;
    while off + stride <= auxv.len() {
        let key = usize::from_ne_bytes(auxv[off..off + word].try_into().unwrap());
        let val = usize::from_ne_bytes(auxv[off + word..off + stride].try_into().unwrap());
        if key == 0 && val == 0 {
            break;
        }
        if key == AT_SECURE && val != 0 {
            fatal("refusing privileged execution (AT_SECURE != 0)");
        }
        off += stride;
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

struct HashingMemfdSink {
    fd: RawFd,
    hasher: Sha256,
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

fn create_sealable_memfd() -> RawFd {
    // SAFETY: NUL-terminated static string passed cleanly to `memfd_create`.
    let fd = unsafe {
        libc::memfd_create(
            b"microfat-rs\0".as_ptr().cast(),
            libc::MFD_CLOEXEC | libc::MFD_ALLOW_SEALING,
        )
    };
    if fd < 0 {
        fatal("memfd_create failed");
    }
    fd
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

fn load_verified_dict(file: &File, dict: Option<DictMeta>) -> Option<Vec<u8>> {
    let d = dict?;
    let mut buf = vec![0u8; d.size as usize];
    if file.read_exact_at(&mut buf, d.offset).is_err() {
        fatal("failed reading shared dictionary");
    }
    if Sha256::digest(&buf) != d.sha256 {
        fatal("shared dictionary SHA-256 mismatch");
    }
    Some(buf)
}

fn decompress_variant(
    file: &File,
    variant: &SelectedVariant<'_>,
    dict_bytes: Option<&[u8]>,
    sink: &mut HashingMemfdSink,
) {
    let mut section = FileSectionReader::new(file, variant.offset, variant.compressed_size);
    let mut buf = vec![0u8; IO_BUF_SIZE];

    let copy_res: io::Result<()> = match variant.compression {
        Compression::None => stream_copy(&mut section, sink, &mut buf),
        Compression::Lz4 => {
            let mut dec = Lz4FrameDecoder::new(section);
            stream_copy(&mut dec, sink, &mut buf)
        }
        Compression::Zstd => {
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
                let mut stream = match StreamingDecoder::new_with_decoder(&mut section, &mut frame_dec)
                {
                    Ok(s) => s,
                    Err(e) => break Err(io::Error::new(io::ErrorKind::InvalidData, e.to_string())),
                };
                if let Err(e) = stream_copy(&mut stream, sink, &mut buf) {
                    break Err(e);
                }
                if section.remaining() == 0 {
                    break Ok(());
                }
            }
        }
    };

    if let Err(err) = copy_res {
        // SAFETY: Best-effort cleanup of unsealed fd on error exit.
        unsafe { libc::close(sink.fd) };
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

fn main() {
    check_unprivileged();

    let file = File::open("/proc/self/exe").unwrap_or_else(|_| fatal("cannot open /proc/self/exe"));
    let file_size = file
        .metadata()
        .unwrap_or_else(|_| fatal("cannot stat /proc/self/exe"))
        .len();
    if file_size < TRAILER_SIZE as u64 {
        fatal("binary smaller than 56-byte trailer");
    }

    let mut trailer_buf = [0u8; TRAILER_SIZE];
    file.read_exact_at(&mut trailer_buf, file_size - TRAILER_SIZE as u64)
        .unwrap_or_else(|_| fatal("cannot read trailer"));
    let trailer = parse_trailer(file_size, &trailer_buf).unwrap_or_else(|e| fatal(format_err_msg(e)));

    let mut index_buf = vec![0u8; trailer.index_size];
    file.read_exact_at(&mut index_buf, trailer.index_offset)
        .unwrap_or_else(|_| fatal("cannot read index"));

    let host_rank = cpuid::detect_host_amd64_rank();
    let validated = verify_and_select_v2(&trailer, &index_buf, b"amd64", host_rank)
        .unwrap_or_else(|e| fatal(format_err_msg(e)));

    let dict_bytes = load_verified_dict(&file, validated.dict);
    let mfd = create_sealable_memfd();
    let mut sink = HashingMemfdSink {
        fd: mfd,
        hasher: Sha256::new(),
        written: 0,
        expected_size: validated.selected.uncompressed_size,
    };

    decompress_variant(&file, &validated.selected, dict_bytes.as_deref(), &mut sink);

    if sink.written != validated.selected.uncompressed_size {
        unsafe { libc::close(mfd) };
        fatal("decompressed payload byte count mismatch");
    }
    if sink.hasher.finalize() != validated.selected.sha256 {
        unsafe { libc::close(mfd) };
        fatal("decompressed payload SHA-256 verification failed");
    }

    // SAFETY: `mfd` is an open memfd created with MFD_ALLOW_SEALING.
    if unsafe { libc::fcntl(mfd, libc::F_ADD_SEALS, SEAL_MASK) } != 0 {
        unsafe { libc::close(mfd) };
        fatal("failed applying memfd write/grow/shrink/seal protections");
    }

    drop(file);
    let mut args = std::env::args_os();
    let argv0 = args.next().unwrap_or_default();
    let exec_err = Command::new(format!("/proc/self/fd/{mfd}"))
        .arg0(argv0)
        .args(args)
        .exec();
    unsafe { libc::close(mfd) };
    fatal(&format!("execve memfd failed: {exec_err}"));
}
