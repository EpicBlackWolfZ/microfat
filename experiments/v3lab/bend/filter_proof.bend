# Bend 2 formal proofs for reversible positional ELF stream filtering.
# Verified with: `bend experiments/v3lab/bend/filter_proof.bend --verdict`
# Note: All definitions are total, terminating, and contain zero `@unsafe` blocks.

type BNat is Data:
  Z{}
  S{p: BNat}

type Tag is Data:
  Lit{val: BNat}
  Rel{imm: BNat}

type Stream is Data:
  SNil{}
  SCons{head: Tag, tail: Stream}

def nadd(a: BNat, b: BNat) -> BNat:
  match a:
    case Z{}:
      b
    case S{p}:
      S{nadd(p, b)}

def npred(x: BNat) -> BNat:
  match x:
    case Z{}:
      Z{}
    case S{p}:
      p

def nsub_rev(b: BNat, a: BNat) -> BNat:
  match b:
    case Z{}:
      a
    case S{q}:
      nsub_rev(q, npred(a))

def nsub(a: BNat, b: BNat) -> BNat:
  nsub_rev(b, a)

def add5(pos: BNat) -> BNat:
  nadd(pos, S{S{S{S{S{Z{}}}}}})

law add_zero_r:
  for +x: BNat
  {nadd(x, Z{}) == x : BNat}

def add_zero_r(x):
  match x:
    case Z{}:
      {==}
    case S{p}:
      %add_zero_r(p) : {S{nadd(p, Z{})} == S{_} : BNat}
      {==}

law add_succ_r_sym:
  for a: BNat
  for +b: BNat
  {S{nadd(a, b)} == nadd(a, S{b}) : BNat}

def add_succ_r_sym(a, b):
  match a:
    case Z{}:
      {==}
    case S{p}:
      %add_succ_r_sym(p, b) : {S{S{nadd(p, b)}} == S{_} : BNat}
      {==}

law sub_add_cancel:
  for +x: BNat
  for +d: BNat
  {nsub(nadd(x, d), d) == x : BNat}

def sub_add_cancel(x, d):
  match d:
    case Z{}:
      %add_zero_r(x) : {nadd(x, Z{}) == _ : BNat}
      {==}
    case S{q}:
      %add_succ_r_sym(x, q) : {nsub(_, S{q}) == x : BNat}
      sub_add_cancel(x, q)

def filter_stream(s: Stream, +pos: BNat) -> Stream:
  match s:
    case SNil{}:
      SNil{}
    case SCons{head, tail}:
      match head:
        case Lit{val}:
          SCons{Lit{val}, filter_stream(tail, S{pos})}
        case Rel{imm}:
          SCons{Rel{nadd(imm, add5(pos))}, filter_stream(tail, add5(pos))}

def unfilter_stream(s: Stream, +pos: BNat) -> Stream:
  match s:
    case SNil{}:
      SNil{}
    case SCons{head, tail}:
      match head:
        case Lit{val}:
          SCons{Lit{val}, unfilter_stream(tail, S{pos})}
        case Rel{imm}:
          SCons{Rel{nsub(imm, add5(pos))}, unfilter_stream(tail, add5(pos))}

law filter_roundtrip:
  for +s: Stream
  for +pos: BNat
  {unfilter_stream(filter_stream(s, pos), pos) == s : Stream}

def filter_roundtrip(s, pos):
  match s:
    case SNil{}:
      {==}
    case SCons{head, tail}:
      match head:
        case Lit{val}:
          %filter_roundtrip(tail, S{pos}) : {SCons{Lit{val}, unfilter_stream(filter_stream(tail, S{pos}), S{pos})} == SCons{Lit{val}, _} : Stream}
          {==}
        case Rel{imm}:
          imm_lhs = nsub(nadd(imm, add5(pos)), add5(pos))
          %sub_add_cancel(imm, add5(pos)) : {SCons{Rel{imm_lhs}, unfilter_stream(filter_stream(tail, add5(pos)), add5(pos))} == SCons{Rel{_}, tail} : Stream}
          %filter_roundtrip(tail, add5(pos)) : {SCons{Rel{imm_lhs}, unfilter_stream(filter_stream(tail, add5(pos)), add5(pos))} == SCons{Rel{imm_lhs}, _} : Stream}
          {==}

def main() -> {unfilter_stream(filter_stream(SNil{}, Z{}), Z{}) == SNil{} : Stream}:
  {==}
