; SMT-LIB2 64-bit signed bitvector proof for:
; - internal/cgroup/memory.go: ExtractionMemory, RetainedMemoryLimit, ZstdDecoderMemory
; - internal/cgroup/cgroup.go: CalculateGOMEMLIMIT integer headroom calculation
;
; Expected output: sat / unsat x 4 pairs (8 lines total).

(set-logic QF_BV)

(define-fun ZERO64 () (_ BitVec 64) #x0000000000000000)
(define-fun ONE64 () (_ BitVec 64) #x0000000000000001)
(define-fun TWO64 () (_ BitVec 64) #x0000000000000002)
(define-fun MAX_INT64 () (_ BitVec 64) #x7fffffffffffffff)
(define-fun ONE_PIB () (_ BitVec 64) #x0004000000000000) ; UnlimitedCgroupV1MemoryThreshold

; --- 1. ExtractionMemory(payload, dictionary, decoder, reserve) ---
(declare-const p (_ BitVec 64))
(declare-const d (_ BitVec 64))
(declare-const dec (_ BitVec 64))
(declare-const res (_ BitVec 64))
(declare-const t1 (_ BitVec 64))
(declare-const t2 (_ BitVec 64))
(declare-const t3 (_ BitVec 64))
(declare-const t4 (_ BitVec 64))

; Exact guards matching Go loop in ExtractionMemory:
(define-fun ext_guards () Bool
  (and
    (bvsge p ZERO64) (bvsle p MAX_INT64) (= t1 p)
    (bvsge d ZERO64) (bvsle d (bvsub MAX_INT64 t1)) (= t2 (bvadd t1 d))
    (bvsge dec ZERO64) (bvsle dec (bvsub MAX_INT64 t2)) (= t3 (bvadd t2 dec))
    (bvsge res ZERO64) (bvsle res (bvsub MAX_INT64 t3)) (= t4 (bvadd t3 res))))

(push 1)
(assert ext_guards)
(assert (bvsgt p ZERO64))
(assert (bvsgt dec ZERO64))
(check-sat)
(pop 1)

(push 1)
(assert ext_guards)
; Exact 66-bit unsigned sum (zero-extended) equals 66-bit zero-extended t4 (zero signed overflow anywhere):
(define-fun sum66 () (_ BitVec 66)
  (bvadd
    (bvadd ((_ zero_extend 2) p) ((_ zero_extend 2) d))
    (bvadd ((_ zero_extend 2) dec) ((_ zero_extend 2) res))))
(assert (not (and
  (bvsge t1 ZERO64)
  (bvsge t2 ZERO64)
  (bvsge t3 ZERO64)
  (bvsge t4 ZERO64)
  (= sum66 ((_ zero_extend 2) t4)))))
(check-sat)
(pop 1)

; --- 2. RetainedMemoryLimit(limit, retained) for ALL arbitrary 64-bit signed inputs ---
(declare-const lim (_ BitVec 64))
(declare-const ret (_ BitVec 64))
(define-fun ret_limit () (_ BitVec 64)
  (ite (or (bvslt ret ZERO64) (bvsge ret lim)) ZERO64 (bvsub lim ret)))

(push 1)
(assert (and (bvsgt lim ZERO64) (bvsge ret ZERO64) (bvslt ret lim)))
(check-sat)
(pop 1)

(push 1)
(assert (not (and
  (bvsge ret_limit ZERO64)
  (or (= ret_limit ZERO64)
      (and (bvsgt lim ZERO64) (bvsle ret_limit lim))))))
(check-sat)
(pop 1)

; --- 3. CalculateGOMEMLIMIT integer headroom reserve calculation ---
(declare-const cgLim (_ BitVec 64))
(declare-const minHeadroom (_ BitVec 64))
(define-fun cg_half () (_ BitVec 64) (bvsdiv cgLim TWO64))
(define-fun cg_half_compl () (_ BitVec 64) (bvsub cgLim cg_half))
(define-fun cg_reserved () (_ BitVec 64)
  (ite (bvslt minHeadroom cg_half_compl) minHeadroom cg_half_compl))
(define-fun cg_headroom_budget () (_ BitVec 64) (bvsub cgLim cg_reserved))

(define-fun cg_guards () Bool
  (and
    (bvsgt cgLim ZERO64)
    (bvslt cgLim ONE_PIB)
    (bvsgt minHeadroom ZERO64)))

(push 1)
(assert cg_guards)
(assert (bvsge cgLim TWO64))
(check-sat)
(pop 1)

(push 1)
(assert cg_guards)
(assert (not (and
  (bvsge cg_half ZERO64)
  (bvsge cg_half_compl ZERO64)
  (bvsgt cg_reserved ZERO64)
  (bvsle cg_reserved cg_half_compl)
  (bvsge cg_headroom_budget cg_half)
  (bvsle cg_headroom_budget cgLim)
  (=> (bvsge cgLim TWO64) (bvsge cg_headroom_budget ONE64)))))
(check-sat)
(pop 1)

; --- 4. ZstdDecoderMemory(window, maxWindow, overhead, maxDecoder) ---
(declare-const zWin (_ BitVec 64))
(declare-const zMaxWin (_ BitVec 64))
(declare-const zOvh (_ BitVec 64))
(declare-const zMaxDec (_ BitVec 64))
(define-fun z_doubled () (_ BitVec 64) (bvadd zWin zWin))
(define-fun z_total () (_ BitVec 64) (bvadd z_doubled zOvh))

(define-fun z_guards () Bool
  (and
    (bvsgt zWin ZERO64)
    (bvsgt zMaxWin ZERO64)
    (bvsge zOvh ZERO64)
    (bvsgt zMaxDec ZERO64)
    (bvsle zWin zMaxWin)
    (bvsle zWin (bvsub MAX_INT64 zWin))
    (bvsle zOvh (bvsub MAX_INT64 z_doubled))
    (bvsle z_total zMaxDec)))

(push 1)
(assert z_guards)
(check-sat)
(pop 1)

(push 1)
(assert z_guards)
(define-fun z_expected66 () (_ BitVec 66)
  (bvadd
    (bvadd ((_ zero_extend 2) zWin) ((_ zero_extend 2) zWin))
    ((_ zero_extend 2) zOvh)))
(assert (not (and
  (bvsgt z_doubled ZERO64)
  (bvsgt z_total ZERO64)
  (bvsle z_total MAX_INT64)
  (bvsle z_total zMaxDec)
  (= z_expected66 ((_ zero_extend 2) z_total)))))
(check-sat)
(pop 1)
