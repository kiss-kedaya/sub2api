package service

// Cline 只提供 Chat Completions。Responses / Messages 入站在网关里转成 Chat。
// 同一个 Key 有积分和 ClinePass 两个钱包，超限只冷却对应钱包，不停整号。

const (
	DefaultClineTestModel     = "deepseek/deepseek-v4-flash"
	DefaultClinePassTestModel = "cline-pass/glm-5.3-flash"
)

func (a *Account) IsCline() bool {
	return a != nil && a.Platform == PlatformCline
}
