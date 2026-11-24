import type { ApiKeyVerificationFactors } from '@/types'

/**
 * 创建 / 轮换 Key 前是否必须先过一次身份校验。
 *
 * 以服务端的 create_verification_required 为准：只有开了 TOTP 或绑了 Passkey 的账号才强制；
 * 只有登录密码的账号（含用 Google / GitHub 登录的用户）不再被卡在输密码上。
 * 老服务端没有这个字段，按它当时的真实校验口径处理（有任何可用因子就强制），
 * 这样发版期间新页面碰到老实例也不会少带校验材料。
 */
export function needsKeyVerification(factors: ApiKeyVerificationFactors | null | undefined): boolean {
  if (!factors) return false
  return factors.create_verification_required ?? factors.available_methods.length > 0
}
