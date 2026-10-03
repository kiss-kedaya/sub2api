package claude

import (
	"strings"
	"unicode"
)

var (
	effortLowMediumHigh         = []string{"low", "medium", "high"}
	effortLowMediumHighMax      = []string{"low", "medium", "high", "max"}
	effortLowMediumHighXHighMax = []string{"low", "medium", "high", "xhigh", "max"}
)

var effortFamilies = []struct {
	family string
	levels []string
}{
	{family: "claude-mythos-preview", levels: effortLowMediumHighMax},
	{family: "claude-mythos-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-fable-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-sonnet-4-6", levels: effortLowMediumHighMax},
	{family: "claude-sonnet-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-4-8", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-4-7", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-4-6", levels: effortLowMediumHighMax},
	{family: "claude-opus-4-5", levels: effortLowMediumHigh},
	{family: "claude-opus-5-5", levels: effortLowMediumHighXHighMax},
	{family: "claude-opus-5", levels: effortLowMediumHighXHighMax},
}

// EffortLevelsForModel returns the output_config.effort values accepted by a
// Claude model, ordered from the lightest to the deepest reasoning level.
func EffortLevelsForModel(model string) []string {
	id := normalizeEffortModelID(model)
	for _, entry := range effortFamilies {
		if id == entry.family || strings.HasPrefix(id, entry.family+"-") {
			return append([]string(nil), entry.levels...)
		}
	}
	return nil
}

// IsOpus55 identifies the fixed Opus 5.5 ID after provider/local suffix normalization.
func IsOpus55(model string) bool {
	return normalizeEffortModelID(model) == "claude-opus-5-5"
}

// IsSonnet55 identifies the fixed Sonnet 5.5 ID after provider/local suffix
// normalization. Sonnet 5.5 has no entry in the bundled pricing catalog, so
// callers must intercept it explicitly — otherwise "claude-sonnet-5-5" falls
// into the sonnet-4.5 family ($3/$15) and overcharges by 50%.
func IsSonnet55(model string) bool {
	return normalizeEffortModelID(model) == "claude-sonnet-5-5"
}

func normalizeEffortModelID(model string) string {
	id := strings.ToLower(strings.TrimSpace(model))
	id = strings.TrimPrefix(id, "models/")
	if slash := strings.IndexByte(id, '/'); slash >= 0 {
		id = strings.TrimPrefix(strings.TrimSpace(id[slash+1:]), "models/")
	}
	// 区域前缀写法：us.anthropic.claude-... / eu.anthropic.claude-...
	if idx := strings.Index(id, "anthropic."); idx >= 0 {
		id = id[idx+len("anthropic."):]
	}
	id = strings.TrimSuffix(id, "-thinking")
	// 版本号点号写法归一：claude-opus-5.5 -> claude-opus-5-5。只处理夹在数字之间的
	// 点，避免影响 gpt-5.6 之类由各自别名表处理的写法。
	id = DottedVersionToHyphen(id)
	if mapped, ok := ModelIDReverseOverrides[id]; ok {
		id = mapped
	}
	if len(id) >= 9 {
		suffix := id[len(id)-9:]
		if suffix[0] == '-' {
			digits := true
			for _, r := range suffix[1:] {
				if !unicode.IsDigit(r) {
					digits = false
					break
				}
			}
			if digits {
				id = id[:len(id)-9]
			}
		}
	}
	return id
}

// DottedVersionToHyphen 把夹在数字之间的点替换成连字符（5.5 -> 5-5）。
// 供计费侧复用：目录键用连字符，用户会打点号写法。
func DottedVersionToHyphen(id string) string {
	if !strings.Contains(id, ".") {
		return id
	}
	b := []byte(id)
	for i := 1; i < len(b)-1; i++ {
		if b[i] == '.' && b[i-1] >= '0' && b[i-1] <= '9' && b[i+1] >= '0' && b[i+1] <= '9' {
			b[i] = '-'
		}
	}
	return string(b)
}
