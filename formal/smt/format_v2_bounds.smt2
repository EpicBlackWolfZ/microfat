; SMT-LIB2 64-bit signed bitvector proof for internal/format/format.go:
; - ReadTrailerAndIndex trailer/index offset and size checks (lines 885-915)
; - ValidateBounds / validateDictionaryBounds / validateVariantBounds (lines 476-562)
;
; Expected output: sat (premises satisfiable), followed by unsat x 3 (negated safety properties).

(set-logic QF_BV)

(define-fun ZERO64 () (_ BitVec 64) #x0000000000000000)
(define-fun TRAILER_SIZE () (_ BitVec 64) #x0000000000000038) ; 56 bytes
(define-fun MAX_INDEX_SIZE () (_ BitVec 64) #x0000000000100000) ; 1 MiB
(define-fun MAX_DICT_SIZE () (_ BitVec 64) #x0000000000100000) ; 1 MiB
(define-fun MAX_PAYLOAD_SIZE () (_ BitVec 64) #x0000000040000000) ; 1 GiB

(declare-const totalSize (_ BitVec 64))
(declare-const trailerOffset (_ BitVec 64))
(declare-const indexOffset (_ BitVec 64))
(declare-const indexSize (_ BitVec 64))

(declare-const hasDict Bool)
(declare-const dictOff (_ BitVec 64))
(declare-const dictSize (_ BitVec 64))
(declare-const dictEnd (_ BitVec 64))

(declare-const off0 (_ BitVec 64))
(declare-const comp0 (_ BitVec 64))
(declare-const uncomp0 (_ BitVec 64))
(declare-const v0End (_ BitVec 64))

(declare-const off1 (_ BitVec 64))
(declare-const comp1 (_ BitVec 64))
(declare-const uncomp1 (_ BitVec 64))
(declare-const v1End (_ BitVec 64))

; --- Production ReadTrailerAndIndex guards ---
(assert (bvsge totalSize TRAILER_SIZE))
(assert (= trailerOffset (bvsub totalSize TRAILER_SIZE)))
(assert (bvsge indexOffset ZERO64))
(assert (bvslt indexOffset trailerOffset))
(assert (bvsgt indexSize ZERO64))
(assert (bvsle indexSize MAX_INDEX_SIZE))
(assert (= indexSize (bvsub trailerOffset indexOffset)))

; --- Production validateDictionaryBounds guards ---
(assert (ite hasDict
  (and
    (bvsgt dictSize ZERO64)
    (bvsle dictSize MAX_DICT_SIZE)
    (bvsge dictOff ZERO64)
    (bvsle dictOff indexOffset)
    (bvsle dictSize (bvsub indexOffset dictOff))
    (= dictEnd (bvadd dictOff dictSize)))
  (and
    (= dictSize ZERO64)
    (= dictEnd ZERO64))))

; --- Production validateVariantBounds guards (2 variants) ---
(assert (bvsge off0 ZERO64))
(assert (bvsgt comp0 ZERO64))
(assert (bvsgt uncomp0 ZERO64))
(assert (bvsle uncomp0 MAX_PAYLOAD_SIZE))
(assert (bvsle off0 indexOffset))
(assert (bvsle comp0 (bvsub indexOffset off0)))
(assert (=> hasDict (bvsge off0 dictEnd)))
(assert (= v0End (bvadd off0 comp0)))

(assert (bvsge off1 ZERO64))
(assert (bvsgt comp1 ZERO64))
(assert (bvsgt uncomp1 ZERO64))
(assert (bvsle uncomp1 MAX_PAYLOAD_SIZE))
(assert (bvsle off1 indexOffset))
(assert (bvsle comp1 (bvsub indexOffset off1)))
(assert (bvsge off1 v0End))
(assert (= v1End (bvadd off1 comp1)))

; 1. Non-vacuity check: valid binary exists with dictionary + two variants.
(push 1)
(assert hasDict)
(check-sat)
(pop 1)

; 2. Negated Property A: Trailer & index arithmetic stays in [0, totalSize] without int64 wrap.
(push 1)
(assert (not (and
  (bvsge trailerOffset ZERO64)
  (bvslt trailerOffset totalSize)
  (bvslt indexOffset trailerOffset)
  (= (bvadd indexOffset indexSize) trailerOffset)
  (= (bvadd trailerOffset TRAILER_SIZE) totalSize))))
(check-sat)
(pop 1)

; 3. Negated Property B: Dictionary and variant addition/subtraction never wrap signed int64.
(push 1)
(assert (not (and
  (bvsle ZERO64 dictEnd)
  (bvsle dictEnd indexOffset)
  (bvsgt v0End off0)
  (bvsle v0End indexOffset)
  (bvsgt v1End off1)
  (bvsle v1End indexOffset)
  (bvsge (bvsub indexOffset off0) ZERO64)
  (bvsge (bvsub indexOffset off1) ZERO64)
  (=> hasDict (bvsge (bvsub indexOffset dictOff) ZERO64)))))
(check-sat)
(pop 1)

; 4. Negated Property C: Strict pairwise segment ordering & disjointness inside file [0, totalSize].
(push 1)
(assert (not (and
  (=> hasDict (and (bvsle ZERO64 dictOff) (bvslt dictOff dictEnd) (bvsle dictEnd off0)))
  (bvsle ZERO64 off0)
  (bvslt off0 v0End)
  (bvsle v0End off1)
  (bvslt off1 v1End)
  (bvsle v1End indexOffset)
  (bvslt indexOffset trailerOffset)
  (bvslt trailerOffset totalSize))))
(check-sat)
(pop 1)
