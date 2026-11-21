/**
 * GF(256) arithmetic for the shamir-gf256 share scheme.
 *
 * This must stay byte-identical to the Go implementation in
 * `backend/internal/cryptopay/wallet/shards/shards.go`:
 * reduction polynomial 0x11b, generator 0x03, log/exp tables built the same
 * way. Any change here breaks recovery of shares produced by the other side.
 */

const EXP = new Uint8Array(512)
const LOG = new Uint8Array(256)

function mulByThree(value: number): number {
  const doubled = (value << 1) ^ ((value & 0x80) !== 0 ? 0x1b : 0)
  return (doubled & 0xff) ^ value
}

function initTables(): void {
  let value = 1
  for (let i = 0; i < 255; i += 1) {
    EXP[i] = value
    LOG[value] = i
    value = mulByThree(value)
  }
  for (let i = 255; i < EXP.length; i += 1) {
    EXP[i] = EXP[i - 255]
  }
}

initTables()

/** Multiply two field elements. */
export function gfMul(a: number, b: number): number {
  if (a === 0 || b === 0) return 0
  return EXP[LOG[a] + LOG[b]]
}

/** Divide two field elements (b must be non-zero). */
export function gfDiv(a: number, b: number): number {
  if (a === 0) return 0
  if (b === 0) throw new Error('gf256: division by zero')
  return EXP[LOG[a] - LOG[b] + 255]
}

/** Evaluate a polynomial (coefficients lowest degree first) at x, Horner. */
export function gfEvaluate(coefficients: Uint8Array, x: number): number {
  let result = 0
  for (let i = coefficients.length - 1; i >= 0; i -= 1) {
    result = gfMul(result, x) ^ coefficients[i]
  }
  return result
}
