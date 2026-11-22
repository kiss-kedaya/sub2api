package service

import (
	"strings"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/util/textsanitize"
)

// MaxUsernameRunes 用户名长度上限（按字符计）。users.username 列宽 100，
// 这里按展示字段收紧到 50，避免超长名称把管理面表格、邮件和通知撑爆。
const MaxUsernameRunes = 50

var ErrUsernameInvalid = infraerrors.BadRequest(
	"USERNAME_INVALID",
	"username may only contain letters, numbers, spaces and _-. (max 50 characters)",
)

// ValidateUsername 校验用户提交的用户名。用户名会进入管理面、邮件与通知等展示面，
// 必须在这里拒绝标记字符（首例存储型 XSS 就是通过 PUT /api/v1/user 写入的）。
// 允许各国文字、数字、空格与 _ - . ；空值由调用方决定是否允许（清空/未设置）。
func ValidateUsername(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" || utf8.RuneCountInString(value) > MaxUsernameRunes {
		return ErrUsernameInvalid
	}
	for _, r := range value {
		if !isUsernameRune(r) {
			return ErrUsernameInvalid
		}
	}
	return nil
}

// SanitizeUsername 处理第三方 OAuth 返回的显示名：先按统一规则清洗成纯文本
// （剥标签、解码实体、去控制字符与零宽字符），再过滤不允许的字符并截断；
// 过滤后为空返回空串，调用方保持原用户名不变。
func SanitizeUsername(raw string) string {
	// 第三方昵称可能带 HTML 实体或标签（如 "<img onerror>"、"\u200badmin"），
	// 先统一清洗，再走字符白名单——两层都过一遍，谁在调用链里漏了都不会落库。
	value := strings.TrimSpace(textsanitize.PlainText(raw))
	if value == "" {
		return ""
	}
	out := make([]rune, 0, MaxUsernameRunes)
	for _, r := range value {
		if !isUsernameRune(r) {
			continue
		}
		out = append(out, r)
		if len(out) == MaxUsernameRunes {
			break
		}
	}
	return strings.TrimSpace(string(out))
}

// sanitizeUsernameInput 把提交的用户名清洗成纯文本（剥标签、解码实体、去控制
// 字符与零宽字符），再交给 ValidateUsername。清洗后为空但原始输入非空，只有在
// 「回填存量同值」时才放行（历史脏数据要能被覆盖成空/干净值），其他情况按非法
// 用户名拒绝，避免 payload-only 输入静默清空用户名。
func sanitizeUsernameInput(raw, current string) (string, error) {
	value := textsanitize.PlainText(raw)
	if value == "" && strings.TrimSpace(raw) != "" && raw != current {
		return "", ErrUsernameInvalid
	}
	return value, nil
}

func isUsernameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' || r == ' '
}
