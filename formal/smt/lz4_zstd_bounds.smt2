; SMT-LIB2 64-bit signed bitvector proof for Zstd frame window caps and LZ4/bounded-writer streaming limits.
; Matches internal/codec/zstd.go, internal/codec/lz4.go, and experiments/v3lab/rust-stub/src/main.rs.
;
; Expected output: sat, unsat, unsat, sat, unsat

(set-logic QF_BV)

(define-fun ZERO64 () (_ BitVec 64) #x0000000000000000)
(define-fun MIN_ZSTD_WIN () (_ BitVec 64) #x0000000000000400)     ; 1024 B
(define-fun MAX_ZSTD_WIN () (_ BitVec 64) #x0000000008000000)     ; 128 MiB
(define-fun ZSTD_OVERHEAD () (_ BitVec 64) #x0000000000400000)    ; 4 MiB
(define-fun MAX_DECODER_MEM () (_ BitVec 64) #x0000000020000000)  ; 512 MiB
(define-fun MAX_LZ4_BLK () (_ BitVec 64) #x0000000000400000)      ; 4 MiB
(define-fun MAX_PAYLOAD () (_ BitVec 64) #x0000000040000000)      ; 1 GiB
(define-fun INT64_MAX () (_ BitVec 64) #x7fffffffffffffff)

; --- Zstd skippable frame & window bounds ---
(declare-const win (_ BitVec 64))
(declare-const curOff (_ BitVec 64))
(declare-const endOff (_ BitVec 64))
(declare-const skipTotal (_ BitVec 64))
(declare-const decMem (_ BitVec 64))

(assert (bvsge curOff ZERO64))
(assert (bvslt curOff endOff))
(assert (bvsgt skipTotal ZERO64))
(assert (bvsle skipTotal (bvsub endOff curOff)))

(assert (bvsge win MIN_ZSTD_WIN))
(assert (bvsle win MAX_ZSTD_WIN))
(assert (= decMem (bvadd (bvadd win win) ZSTD_OVERHEAD)))

; 1. Satisfiability check: max Zstd window is strictly within decoder cap (2*128MiB + 4MiB <= 512MiB).
(push 1)
(assert (= win MAX_ZSTD_WIN))
(check-sat)
(pop 1)

; 2. Negated Property A: bounded skippable advance cannot wrap int64 or overshoot endOff.
(push 1)
(assert (not (and
  (bvslt curOff (bvadd curOff skipTotal))
  (bvsle (bvadd curOff skipTotal) endOff))))
(check-sat)
(pop 1)

; 3. Negated Property B: Zstd decoder workspace never exceeds MAX_DECODER_MEM or wraps int64.
(push 1)
(assert (not (and
  (bvsgt decMem ZERO64)
  (bvsle decMem MAX_DECODER_MEM)
  (bvsle decMem INT64_MAX))))
(check-sat)
(pop 1)

; --- LZ4 block header length & bounded decompression output guard ---
(declare-const blockHdr (_ BitVec 32))
(declare-const blkLen (_ BitVec 64))
(declare-const written (_ BitVec 64))
(declare-const chunk (_ BitVec 64))
(declare-const expectedSize (_ BitVec 64))

(assert (= blkLen ((_ zero_extend 32) (bvand blockHdr #x7fffffff))))
(assert (bvsle blkLen MAX_LZ4_BLK))

(assert (bvsge written ZERO64))
(assert (bvsge chunk ZERO64))
(assert (bvsle expectedSize MAX_PAYLOAD))
(assert (bvsle written expectedSize))
(assert (bvsle chunk (bvsub expectedSize written)))

; 4. Satisfiability check: full 4 MiB LZ4 block header + uncompressed chunk admitted cleanly.
(push 1)
(assert (= blkLen MAX_LZ4_BLK))
(assert (= chunk blkLen))
(check-sat)
(pop 1)

; 5. Negated Property C: boundedWriter addition cannot overflow signed int64 or exceed declared UncompressedSize.
(push 1)
(assert (not (and
  (bvsle ZERO64 (bvadd written chunk))
  (bvsle (bvadd written chunk) expectedSize)
  (bvsle (bvadd written chunk) MAX_PAYLOAD))))
(check-sat)
(pop 1)
