import { describe, expect, it } from "vitest";

import {
  OPENAI_TIER_DEFAULT_MULTIPLIER,
  buildOpenAITierMultiplierPayload,
  findInvalidOpenAITierMultiplier,
  normalizeGroupOpenAIFast,
  openAITierMultiplierInputId,
  openAITierMultiplierToForm,
  parseOpenAITierMultiplier,
  supportsGroupOpenAIFast,
} from "../groupsOpenAIFast";
import en from "@/i18n/locales/en/admin/overview";
import zh from "@/i18n/locales/zh/admin/overview";

describe("groupsOpenAIFast", () => {
  it("supports OpenAI and composite groups", () => {
    expect(supportsGroupOpenAIFast("openai")).toBe(true);
    expect(supportsGroupOpenAIFast("composite")).toBe(true);
    expect(supportsGroupOpenAIFast("anthropic")).toBe(false);
  });

  it("clears stale enabled state on unsupported platforms", () => {
    expect(normalizeGroupOpenAIFast("openai", true)).toBe(true);
    expect(normalizeGroupOpenAIFast("composite", true)).toBe(true);
    expect(normalizeGroupOpenAIFast("anthropic", true)).toBe(false);
    expect(normalizeGroupOpenAIFast("openai", false)).toBe(false);
  });

  it("provides localized switch labels", () => {
    expect(zh.groups.openaiFast).toMatchObject({
      title: expect.any(String),
      force: expect.any(String),
      hint: expect.stringContaining("service_tier=priority"),
      free: expect.any(String),
      freeHint: expect.stringContaining("Standard"),
    });
    expect(en.groups.openaiFast).toMatchObject({
      title: expect.any(String),
      force: expect.any(String),
      hint: expect.stringContaining("service_tier=priority"),
      free: expect.any(String),
      freeHint: expect.stringContaining("Standard"),
    });
  });
});

describe("parseOpenAITierMultiplier", () => {
  it.each([undefined, null, "", "   ", "abc", "1,5x", NaN, true, {}])(
    "treats %j as clear (official default) without an error",
    (raw) => {
      expect(parseOpenAITierMultiplier(raw)).toEqual({
        value: null,
        issue: null,
      });
    },
  );

  it.each([
    [2, 2],
    ["2", 2],
    [" 2.5 ", 2.5],
    [0.0001, 0.0001],
    ["0.16", 0.16],
    [1, 1],
    [100, 100],
    [99.9999, 99.9999],
  ])("accepts %j", (raw, value) => {
    expect(parseOpenAITierMultiplier(raw)).toEqual({ value, issue: null });
  });

  it.each([0, "0", -1, "-0.5", -Infinity])(
    "rejects %j: must be greater than 0 (0 is not free, 1 is)",
    (raw) => {
      expect(parseOpenAITierMultiplier(raw)).toEqual({
        value: null,
        issue: "nonPositive",
      });
    },
  );

  it.each([100.0001, "101", 1000, "1e999", Infinity])(
    "rejects %j: above 100",
    (raw) => {
      expect(parseOpenAITierMultiplier(raw)).toEqual({
        value: null,
        issue: "tooLarge",
      });
    },
  );

  it.each([1.23456, "0.00001", 0.1 + 0.2, "2.00001"])(
    "rejects %j: more than 4 decimal places",
    (raw) => {
      expect(parseOpenAITierMultiplier(raw)).toEqual({
        value: null,
        issue: "tooManyDecimals",
      });
    },
  );
});

describe("openAITierMultiplierToForm", () => {
  it("maps null/missing/non-positive to an empty input (official default)", () => {
    for (const value of [null, undefined, 0, -1, NaN, Infinity]) {
      expect(openAITierMultiplierToForm(value)).toBe("");
    }
  });

  it("maps a stored multiplier to the text shown in the input", () => {
    expect(openAITierMultiplierToForm(2)).toBe("2");
    expect(openAITierMultiplierToForm(2.5)).toBe("2.5");
    expect(openAITierMultiplierToForm(0.0001)).toBe("0.0001");
  });
});

describe("buildOpenAITierMultiplierPayload", () => {
  const form = (patch: Record<string, unknown> = {}) => ({
    free_openai_fast: false,
    openai_fast_multiplier: "",
    openai_ultrafast_multiplier: "",
    ...patch,
  });

  it("sends 0 (clear) for empty inputs and the number otherwise", () => {
    expect(buildOpenAITierMultiplierPayload("openai", form())).toEqual({
      openai_fast_multiplier: 0,
      openai_ultrafast_multiplier: 0,
    });
    expect(
      buildOpenAITierMultiplierPayload(
        "openai",
        form({ openai_fast_multiplier: 2.5, openai_ultrafast_multiplier: "1" }),
      ),
    ).toEqual({ openai_fast_multiplier: 2.5, openai_ultrafast_multiplier: 1 });
    // 非数字和空一样按「清除」处理
    expect(
      buildOpenAITierMultiplierPayload(
        "composite",
        form({ openai_fast_multiplier: "abc", openai_ultrafast_multiplier: "  " }),
      ),
    ).toEqual({ openai_fast_multiplier: 0, openai_ultrafast_multiplier: 0 });
  });

  it("omits both fields on platforms without Fast support", () => {
    expect(
      buildOpenAITierMultiplierPayload(
        "anthropic",
        form({ openai_fast_multiplier: 3, openai_ultrafast_multiplier: 3 }),
      ),
    ).toEqual({
      openai_fast_multiplier: undefined,
      openai_ultrafast_multiplier: undefined,
    });
  });

  it("omits Fast (locked at 1x) while free Fast is on, but still sends UltraFast", () => {
    expect(
      buildOpenAITierMultiplierPayload(
        "openai",
        form({
          free_openai_fast: true,
          openai_fast_multiplier: 3,
          openai_ultrafast_multiplier: 6,
        }),
      ),
    ).toEqual({
      openai_fast_multiplier: undefined,
      openai_ultrafast_multiplier: 6,
    });
  });

  it("never turns an invalid value into a clear: the field is omitted instead", () => {
    expect(
      buildOpenAITierMultiplierPayload(
        "openai",
        form({ openai_fast_multiplier: 101, openai_ultrafast_multiplier: 0 }),
      ),
    ).toEqual({
      openai_fast_multiplier: undefined,
      openai_ultrafast_multiplier: undefined,
    });
  });
});

describe("findInvalidOpenAITierMultiplier", () => {
  const form = (patch: Record<string, unknown> = {}) => ({
    free_openai_fast: false,
    openai_fast_multiplier: "",
    openai_ultrafast_multiplier: "",
    ...patch,
  });

  it("returns null for blank and valid values", () => {
    expect(findInvalidOpenAITierMultiplier("openai", form())).toBeNull();
    expect(
      findInvalidOpenAITierMultiplier(
        "openai",
        form({ openai_fast_multiplier: 2, openai_ultrafast_multiplier: 100 }),
      ),
    ).toBeNull();
  });

  it("reports the first invalid field, Fast before UltraFast", () => {
    expect(
      findInvalidOpenAITierMultiplier(
        "openai",
        form({ openai_fast_multiplier: 0, openai_ultrafast_multiplier: 101 }),
      ),
    ).toEqual({ tier: "fast", issue: "nonPositive" });
    expect(
      findInvalidOpenAITierMultiplier(
        "composite",
        form({ openai_ultrafast_multiplier: 1.23456 }),
      ),
    ).toEqual({ tier: "ultrafast", issue: "tooManyDecimals" });
  });

  it("ignores a locked Fast value and unsupported platforms", () => {
    expect(
      findInvalidOpenAITierMultiplier(
        "openai",
        form({ free_openai_fast: true, openai_fast_multiplier: 101 }),
      ),
    ).toBeNull();
    expect(
      findInvalidOpenAITierMultiplier(
        "anthropic",
        form({ openai_fast_multiplier: 101 }),
      ),
    ).toBeNull();
  });
});

describe("tier multiplier helpers", () => {
  it("builds the input id used by both the field and the submit-time focus", () => {
    expect(openAITierMultiplierInputId("create", "fast")).toBe(
      "create-openai-fast-multiplier",
    );
    expect(openAITierMultiplierInputId("edit", "ultrafast")).toBe(
      "edit-openai-ultrafast-multiplier",
    );
  });

  it("documents the official defaults shown as placeholders", () => {
    expect(OPENAI_TIER_DEFAULT_MULTIPLIER).toEqual({ fast: 2, ultrafast: 6 });
  });
});

describe("tier multiplier copy", () => {
  const locales = { zh: zh.groups.openaiFast, en: en.groups.openaiFast };

  it.each(Object.entries(locales))(
    "%s explains stacking, blank = official and 1 = no surcharge",
    (_name, copy) => {
      const text = copy.tierMultiplier;
      for (const value of [text.fastHint, text.ultrafastHint]) {
        expect(value).toContain("1");
      }
      expect(text.fastHint).toContain("2.5");
      expect(text.ultrafastHint).toContain("6");
      expect(text.placeholder).toContain("{value}");
      expect(text.previewValue).toContain("{total}");
      expect(text.previewOfficial).toContain("{rate}");
      expect(text.lockedFree).toContain("1×");
      for (const key of ["nonPositive", "tooLarge", "tooManyDecimals"] as const) {
        expect(text.errors[key]).toBeTruthy();
      }
    },
  );
});
