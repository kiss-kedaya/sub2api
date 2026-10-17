//go:build unit

package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateUsername(t *testing.T) {
	valid := []string{
		"john.doe",
		"user_1",
		"赵四",
		"foo bar",
		"a",
	}
	for _, username := range valid {
		t.Run("valid/"+username, func(t *testing.T) {
			require.NoError(t, ValidateUsername(username))
		})
	}

	invalid := []string{
		"",
		"   ",
		`<svg/onload=fetch("//evil.example")>`,
		"user<script>",
		`quote"name`,
		"amp&name",
		"user/name",
		"user\\name",
		"user;name",
		"tab\tname",
		"line\nname",
		"😀",
		strings.Repeat("字", MaxUsernameRunes+1),
	}
	for _, username := range invalid {
		t.Run("invalid/"+username, func(t *testing.T) {
			require.ErrorIs(t, ValidateUsername(username), ErrUsernameInvalid)
		})
	}
}

func TestSanitizeUsername(t *testing.T) {
	require.Equal(t, "", SanitizeUsername(""))
	require.Equal(t, "", SanitizeUsername("  "))
	require.Equal(t, "John Doe", SanitizeUsername(" John Doe "))
	require.Equal(t, "bTomb", SanitizeUsername("<b>Tom</b>"))
	require.Equal(t, "赵四", SanitizeUsername("赵四"))
	require.Equal(t, strings.Repeat("a", MaxUsernameRunes), SanitizeUsername(strings.Repeat("a", MaxUsernameRunes+10)))
	// 过滤后只剩空白/标点则视为空，调用方保持原用户名。
	require.Equal(t, "", SanitizeUsername("<>"))
	require.Equal(t, "", SanitizeUsername("😀😀"))
}
