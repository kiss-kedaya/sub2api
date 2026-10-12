package service

import (
	"strings"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
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

// SanitizeUsername 处理第三方 OAuth 返回的显示名：过滤不允许的字符并截断；
// 过滤后为空返回空串，调用方保持原用户名不变。
func SanitizeUsername(raw string) string {
	value := strings.TrimSpace(raw)
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

func isUsernameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' || r == ' '
}
