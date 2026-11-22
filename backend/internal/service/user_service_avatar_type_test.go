//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 头像只收 PNG/JPEG/WebP：SVG 可以内嵌 <script>/onload，一旦被当图片内联渲染
// 或以后改成 v-html/object 展示，就是存储型 XSS。
func TestSetAvatarRejectsSVGDataURL(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`)
	payloads := []string{
		"data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(svg),
		"data:image/svg+xml;charset=utf-8;base64," + base64.StdEncoding.EncodeToString(svg),
		"data:Image/SVG+XML;base64," + base64.StdEncoding.EncodeToString(svg),
	}
	for _, payload := range payloads {
		repo := &mockUserRepo{}
		svc := NewUserService(repo, nil, nil, nil)

		_, err := svc.SetAvatar(context.Background(), 21, payload)
		require.ErrorIs(t, err, ErrAvatarUnsupportedType, "payload %q", payload)
		require.Empty(t, repo.upsertAvatarArgs, "被拒绝的头像不能落库")
	}
}

// 声明的 MIME 不能当证据：image/png 的 data URL 里放 SVG 字节也必须拒绝。
func TestSetAvatarRejectsSpoofedPNGContent(t *testing.T) {
	svg := []byte(`<svg onload="alert(1)"></svg>`)
	payload := "data:image/png;base64," + base64.StdEncoding.EncodeToString(svg)

	repo := &mockUserRepo{}
	svc := NewUserService(repo, nil, nil, nil)

	_, err := svc.SetAvatar(context.Background(), 22, payload)
	require.ErrorIs(t, err, ErrAvatarNotImage)
	require.Empty(t, repo.upsertAvatarArgs)
}

// GIF 不在白名单内，即使能解码也要明确拒绝。
func TestSetAvatarRejectsNonAllowlistedImageFormat(t *testing.T) {
	var buf bytes.Buffer
	pal := color.Palette{color.Black, color.White}
	require.NoError(t, gif.Encode(&buf, image.NewPaletted(image.Rect(0, 0, 1, 1), pal), nil))
	payload := "data:image/gif;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	repo := &mockUserRepo{}
	svc := NewUserService(repo, nil, nil, nil)

	_, err := svc.SetAvatar(context.Background(), 23, payload)
	require.ErrorIs(t, err, ErrAvatarUnsupportedType)
	require.Empty(t, repo.upsertAvatarArgs)
}

// 声明类型与真实字节不一致时以字节为准，落库 MIME 不能与内容矛盾。
func TestSetAvatarCanonicalizesMismatchedDeclaredType(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil))
	payload := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	repo := &mockUserRepo{}
	svc := NewUserService(repo, nil, nil, nil)

	avatar, err := svc.SetAvatar(context.Background(), 24, payload)
	require.NoError(t, err)
	require.Len(t, repo.upsertAvatarArgs, 1)
	require.Equal(t, "image/jpeg", repo.upsertAvatarArgs[0].ContentType)
	require.True(t, strings.HasPrefix(repo.upsertAvatarArgs[0].URL, "data:image/jpeg;base64,"))
	require.Equal(t, "image/jpeg", avatar.ContentType)
}

func TestSetAvatarAcceptsPNG(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	payload := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	repo := &mockUserRepo{}
	svc := NewUserService(repo, nil, nil, nil)

	avatar, err := svc.SetAvatar(context.Background(), 25, payload)
	require.NoError(t, err)
	require.Equal(t, "image/png", avatar.ContentType)
	require.Len(t, repo.upsertAvatarArgs, 1)
}

// 存量 SVG 头像在读取时降级为默认头像：不静默 500，也不把可执行内容回前端。
func TestGetProfileDegradesLegacySVGAvatar(t *testing.T) {
	repo := &mockUserRepo{
		getByIDUser: &User{ID: 26, Email: "legacy-svg@example.com"},
		getAvatarFn: func(context.Context, int64) (*UserAvatar, error) {
			return &UserAvatar{
				StorageProvider: "inline",
				URL:             "data:image/svg+xml;base64,PHN2ZyBvbmxvYWQ9YWxlcnQoMSk+PC9zdmc+",
				ContentType:     "image/svg+xml",
			}, nil
		},
	}
	svc := NewUserService(repo, nil, nil, nil)

	user, err := svc.GetProfile(context.Background(), 26)
	require.NoError(t, err)
	require.Empty(t, user.AvatarURL)
	require.Empty(t, user.AvatarMIME)
	require.Empty(t, user.AvatarSource)
}

// 旧行里 data URL 是 SVG、content_type 可能是空的；同样要按 URL 里的 MIME 拒绝。
func TestGetProfileDegradesLegacySVGAvatarWithEmptyContentType(t *testing.T) {
	repo := &mockUserRepo{
		getByIDUser: &User{ID: 27, Email: "legacy-svg2@example.com"},
		getAvatarFn: func(context.Context, int64) (*UserAvatar, error) {
			return &UserAvatar{
				StorageProvider: "inline",
				URL:             "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=",
			}, nil
		},
	}
	svc := NewUserService(repo, nil, nil, nil)

	user, err := svc.GetProfile(context.Background(), 27)
	require.NoError(t, err)
	require.Empty(t, user.AvatarURL)
}

// 非 http(s) 的远程头像 URL（例如历史遗留的 javascript:）也必须降级。
func TestGetProfileDegradesUnsafeRemoteAvatarURL(t *testing.T) {
	repo := &mockUserRepo{
		getByIDUser: &User{ID: 28, Email: "legacy-js@example.com"},
		getAvatarFn: func(context.Context, int64) (*UserAvatar, error) {
			return &UserAvatar{
				StorageProvider: "remote_url",
				URL:             "javascript:alert(1)",
			}, nil
		},
	}
	svc := NewUserService(repo, nil, nil, nil)

	user, err := svc.GetProfile(context.Background(), 28)
	require.NoError(t, err)
	require.Empty(t, user.AvatarURL)
}

// 白名单内的存量头像照常返回。
func TestGetProfileKeepsSupportedLegacyAvatar(t *testing.T) {
	repo := &mockUserRepo{
		getByIDUser: &User{ID: 29, Email: "ok-avatar@example.com"},
		getAvatarFn: func(context.Context, int64) (*UserAvatar, error) {
			return &UserAvatar{
				StorageProvider: "inline",
				URL:             "data:image/png;base64,iVBORw0KGgo=",
				ContentType:     "image/png",
			}, nil
		},
	}
	svc := NewUserService(repo, nil, nil, nil)

	user, err := svc.GetProfile(context.Background(), 29)
	require.NoError(t, err)
	require.Equal(t, "data:image/png;base64,iVBORw0KGgo=", user.AvatarURL)
	require.Equal(t, "image/png", user.AvatarMIME)
}

// 通过 PUT /api/v1/user 更新头像时也要被同一套类型策略拦住，且不能落库。
func TestUpdateProfileRejectsSVGAvatar(t *testing.T) {
	svgURL := "data:image/svg+xml;base64,PHN2ZyBvbmxvYWQ9YWxlcnQoMSk+PC9zdmc+"
	repo := &mockUserRepo{
		getByIDUser: &User{ID: 30, Email: "profile-svg@example.com"},
	}
	svc := NewUserService(repo, nil, nil, nil)

	_, err := svc.UpdateProfile(context.Background(), 30, UpdateProfileRequest{AvatarURL: &svgURL})
	require.ErrorIs(t, err, ErrAvatarUnsupportedType)
	require.Empty(t, repo.upsertAvatarArgs)
	require.Zero(t, repo.updateCalls)
}

// 远端 URL 无法预知 MIME，但 *.svg 明确是 SVG，写入/读取都要拒绝或降级。
func TestSetAvatarRejectsRemoteSVGURL(t *testing.T) {
	for _, raw := range []string{
		"https://cdn.example.com/avatar.svg",
		"https://cdn.example.com/path/AVATAR.SVG",
		"https://cdn.example.com/a.svgz?x=1",
	} {
		repo := &mockUserRepo{}
		svc := NewUserService(repo, nil, nil, nil)
		_, err := svc.SetAvatar(context.Background(), 31, raw)
		require.ErrorIs(t, err, ErrAvatarUnsupportedType, "raw=%q", raw)
		require.Empty(t, repo.upsertAvatarArgs)
	}
}

func TestGetProfileDegradesLegacyRemoteSVGAvatar(t *testing.T) {
	repo := &mockUserRepo{
		getByIDUser: &User{ID: 32, Email: "legacy-remote-svg@example.com"},
		getAvatarFn: func(context.Context, int64) (*UserAvatar, error) {
			return &UserAvatar{
				StorageProvider: "remote_url",
				URL:             "https://cdn.example.com/avatar.svg",
			}, nil
		},
	}
	svc := NewUserService(repo, nil, nil, nil)

	user, err := svc.GetProfile(context.Background(), 32)
	require.NoError(t, err)
	require.Empty(t, user.AvatarURL)
}
