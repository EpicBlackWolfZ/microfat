import Base

def bool_to_u32(b: Bool) -> U32:
  match b:
    case True{}:
      1
    case False{}:
      0

def score_step(s1: U32, s2: U32) -> U32:
  op1 = U32.and(s1, 15)
  op2 = U32.and(U32.shrn(s2, 4n), 15)
  bool_to_u32(U32.is_eq(op1, op2))

def next_state(s: U32) -> U32:
  U32.add(U32.mul(s, 1664525), 1013904223)

def score_inner(rem: Nat, +s1: U32, +s2: U32, acc: U32) -> U32:
  match rem:
    case 0n:
      acc
    case 1n+p:
      score_inner(p, next_state(s1), next_state(s2), U32.add(acc, score_step(s1, s2)))

def score_outer(rem: Nat, +idx: U32, acc: U32) -> U32:
  match rem:
    case 0n:
      acc
    case 1n+p:
      seed1 = U32.add(U32.mul(idx, 2654435761), 1)
      seed2 = U32.add(U32.mul(idx, 2246822519), 17)
      chunk = score_inner(64n, seed1, seed2, 0)
      score_outer(p, U32.add(idx, 1), U32.add(acc, chunk))

# Balanced binary fork tree: depth 14n => 2^14 = 16,384 leaves (256 * 64 = 16,384 checks/leaf = 268,435,456 checks).
def scan_tree(+depth: Nat, +idx: U32) -> U32:
  match depth:
    case 0n:
      score_outer(256n, U32.mul(idx, 256), 0)
    case 1n+p:
      left right = scan_tree(p, U32.mul(idx, 2)) scan_tree(p, U32.add(U32.mul(idx, 2), 1))
      U32.add(left, right)

def main() -> IO(Unit):
  do IO<Unit>:
    t0 : Nat <- IO.now()
    total : U32 = scan_tree!(14n, 0)
    t1 : Nat <- IO.now()
    threads : U32 <- IO.thread_count()
    elapsed : Nat = Nat.sub(t1, t0)
    IO.print("leaves=16384 checks_per_leaf=16384 threads=")
    IO.print(U32.show(threads))
    IO.print(" hits=")
    IO.print(U32.show(total))
    IO.print(" elapsed_ms=")
    IO.print(Nat.show(elapsed))
    IO.print("\n")
