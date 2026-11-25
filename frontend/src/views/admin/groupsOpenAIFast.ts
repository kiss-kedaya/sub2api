export function supportsGroupOpenAIFast(platform: string): boolean {
  return platform === "openai" || platform === "composite";
}

export function normalizeGroupOpenAIFast(
  platform: string,
  enabled: boolean,
): boolean {
  return supportsGroupOpenAIFast(platform) && enabled;
}

// ---------------------------------------------------------------------------
// Fast / UltraFast 档位倍率：叠在分组倍率上的倍率，不是独立倍率。
// 例：分组倍率 0.16、Fast 档位倍率 2 → Fast 请求按 0.16 × 2 = 0.32 倍计费。
// 表单值（输入框字符串/数字）↔ 请求体（空 → 0 清除，数字 → 数字）的转换都在这里，便于单测。
// ---------------------------------------------------------------------------

export type OpenAITier = "fast" | "ultrafast";

/** 输入框 id：组件渲染和提交失败后聚焦用同一个，避免靠 testid 取元素。 */
export function openAITierMultiplierInputId(
  idPrefix: string,
  tier: OpenAITier,
): string {
  return `${idPrefix}-openai-${tier}-multiplier`;
}

/** 官方默认档位倍率，只用于 placeholder 和说明；真实默认以后端为准（GPT-5.5 的 Fast 是 2.5×）。 */
export const OPENAI_TIER_DEFAULT_MULTIPLIER: Record<OpenAITier, number> = {
  fast: 2,
  ultrafast: 6,
};

/** 与后端契约一致：>0、≤100、最多 4 位小数。 */
export const OPENAI_TIER_MULTIPLIER_MAX = 100;
const OPENAI_TIER_MULTIPLIER_DECIMALS = 4;

export type OpenAITierMultiplierIssue =
  | "nonPositive"
  | "tooLarge"
  | "tooManyDecimals";

export interface ParsedOpenAITierMultiplier {
  /** null = 留空/非数字（清除，回官方默认），或数值不合法。 */
  value: number | null;
  issue: OpenAITierMultiplierIssue | null;
}

/**
 * 解析并校验输入框的值。空字符串、纯空白、非数字一律按「清除」处理（无错误）；
 * 其余必须是 >0、≤100、最多 4 位小数的数字。0 不算清除：它是错误，
 * 免费请填 1（0 在请求体里才表示清除，不该让人手输 0 误以为免费）。
 */
export function parseOpenAITierMultiplier(
  raw: unknown,
): ParsedOpenAITierMultiplier {
  const value =
    typeof raw === "number"
      ? raw
      : typeof raw === "string" && raw.trim() !== ""
        ? Number(raw)
        : NaN;
  if (Number.isNaN(value)) return { value: null, issue: null };
  if (value <= 0) return { value: null, issue: "nonPositive" };
  if (value > OPENAI_TIER_MULTIPLIER_MAX) {
    return { value: null, issue: "tooLarge" };
  }
  // 四舍五入到 4 位后仍等于自己，才说明最多 4 位小数（输入的 2.5、0.1234 这类十进制数都能正确通过）。
  if (Number(value.toFixed(OPENAI_TIER_MULTIPLIER_DECIMALS)) !== value) {
    return { value: null, issue: "tooManyDecimals" };
  }
  return { value, issue: null };
}

/** 接口值 → 输入框：null/缺省/非正数 → 空字符串（官方默认）。 */
export function openAITierMultiplierToForm(
  value: number | null | undefined,
): string {
  return typeof value === "number" && Number.isFinite(value) && value > 0
    ? String(value)
    : "";
}

interface OpenAITierMultiplierForm {
  free_openai_fast: boolean;
  openai_fast_multiplier: unknown;
  openai_ultrafast_multiplier: unknown;
}

function tierMultiplierToRequest(raw: unknown): number | undefined {
  const { value, issue } = parseOpenAITierMultiplier(raw);
  // 不合法的值不该走到这里（提交前已校验）；兜底省略字段 = 不改，也不会误清除。
  return issue ? undefined : (value ?? 0);
}

/**
 * 表单值 → 请求体字段（创建/更新通用）。
 * - 空 → 0（清除回官方默认），数字 → 数字。
 * - 不支持 Fast 的平台：两个字段都省略（更新 = 不改，创建 = 默认）。
 * - 免费 Fast 开启时 Fast 输入框是禁用的（固定显示 1×），和原生禁用输入框一样不提交：
 *   不顺手清掉已存的值，也不在新建时落一个看不见的值。UltraFast 不受影响。
 * 总是返回两个键（值可能是 undefined），这样展开在 `...form` 之后能盖掉表单里的字符串。
 */
export function buildOpenAITierMultiplierPayload(
  platform: string,
  form: OpenAITierMultiplierForm,
): {
  openai_fast_multiplier: number | undefined;
  openai_ultrafast_multiplier: number | undefined;
} {
  if (!supportsGroupOpenAIFast(platform)) {
    return {
      openai_fast_multiplier: undefined,
      openai_ultrafast_multiplier: undefined,
    };
  }
  return {
    openai_fast_multiplier: form.free_openai_fast
      ? undefined
      : tierMultiplierToRequest(form.openai_fast_multiplier),
    openai_ultrafast_multiplier: tierMultiplierToRequest(
      form.openai_ultrafast_multiplier,
    ),
  };
}

/** 提交前校验：返回第一个不合法的字段，全部合法返回 null。 */
export function findInvalidOpenAITierMultiplier(
  platform: string,
  form: OpenAITierMultiplierForm,
): { tier: OpenAITier; issue: OpenAITierMultiplierIssue } | null {
  if (!supportsGroupOpenAIFast(platform)) return null;
  const fast = form.free_openai_fast
    ? null
    : parseOpenAITierMultiplier(form.openai_fast_multiplier).issue;
  if (fast) return { tier: "fast", issue: fast };
  const ultrafast = parseOpenAITierMultiplier(
    form.openai_ultrafast_multiplier,
  ).issue;
  return ultrafast ? { tier: "ultrafast", issue: ultrafast } : null;
}

/** 倍率显示：最多 4 位小数、去掉多余的 0（0.30000000000000004 → 0.3，2 → 2）。 */
export function formatTierMultiplier(value: number): string {
  const rounded = Number(value.toFixed(OPENAI_TIER_MULTIPLIER_DECIMALS));
  // 极小的积四舍五入会变成 0，显示 0 会误导
  if (rounded === 0 && value !== 0) return String(Number(value.toPrecision(2)));
  return String(rounded);
}
