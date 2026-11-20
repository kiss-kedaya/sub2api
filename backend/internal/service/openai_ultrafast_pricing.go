package service

import (
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

// GPT-6.2 Ultra Fast 档位计费倍率。
//
// 官方口径（v0.2.23 release notes）：
//   - 2027-03-01 00:00（太平洋时间）前按标准价 3 倍促销；
//   - 之后自动切到常规 6 倍；
//   - GPT-6 Astra 与 GPT-6.1 Sol 没有促销，固定 6 倍。
//
// 边界点用固定 UTC 瞬间表示：太平洋标准时间 UTC-8，因此 2027-03-01 00:00 PT
// 就是 2027-03-01T08:00:00Z；太平洋夏令时 2027-03-14 才开始，这个瞬间没有歧义，
// 也不需要加载 tzdata。
var gpt62UltrafastPromoEnd = time.Date(2027, time.March, 1, 8, 0, 0, 0, time.UTC)

const (
	gpt62UltrafastPromoMultiplier     = 3.0
	gpt62UltrafastRegularMultiplier   = 6.0
	openAIUltrafastFallbackMultiplier = gpt62UltrafastRegularMultiplier
)

// openAIUltrafastMultiplier 返回 model 在 at 时刻的 Ultra Fast 档位倍率。
//
// 倍率是时间敏感的业务规则，所以显式接收时刻（延迟结算也按请求时点计价），
// 由调用方注入时间源；at 为零值时按当前时刻处理，避免误判成促销价。
func openAIUltrafastMultiplier(model string, at time.Time) float64 {
	if at.IsZero() {
		at = timezone.Now()
	}
	if openai.GPT62ModelID(model) != "" && at.Before(gpt62UltrafastPromoEnd) {
		return gpt62UltrafastPromoMultiplier
	}
	return openAIUltrafastFallbackMultiplier
}

// openAIModelSupportsUltrafast 判断 OpenAI 是否为该模型提供 Ultra Fast 档位。
// 它是档位声明（Codex 模型目录）与计费倍率的唯一判定入口，避免出现
// 「目录声明了档位、计费却没按档位算」的不一致。
func openAIModelSupportsUltrafast(model string) bool {
	if openai.GPT62ModelID(model) != "" {
		return true
	}
	if openai.IsGPT61SolModelSpelling(model) {
		return true
	}
	return isOpenAIGPT6AstraModel(model)
}

// openAIUltrafastTierDescription 生成 Codex 模型目录里的档位描述，倍率按 at
// 时刻的规则渲染（GPT-6.2 促销期 3x、之后 6x）。
func openAIUltrafastTierDescription(model string, at time.Time) string {
	return fmt.Sprintf("Lowest latency; %gx Standard token pricing.", openAIUltrafastMultiplier(model, at))
}
