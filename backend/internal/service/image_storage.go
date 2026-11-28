package service

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

const defaultImageMaxDownloadBytes int64 = 32 << 20 // 32 MiB

// ImageStorage 把图片字节写入对象存储并返回可访问 URL。
//
// 这是对象存储的可插拔抽象：适配一个新的对象存储厂商，只需实现本接口
// （例如包一个厂商 SDK），无需改动任务/网关逻辑。仓库内自带一个 S3 兼容实现
// （repository.S3ImageStorage），适用于 AWS S3 / Cloudflare R2 / 阿里云 OSS / MinIO 等。
type ImageStorage interface {
	// Save 把 data 以 key 存入对象存储，返回可下载的 URL（公开直链或 presigned 临时链接）。
	// contentType 为图片 MIME 类型，如 "image/png"。
	Save(ctx context.Context, key, contentType string, data []byte) (url string, err error)
}

// ImageResultUploader 是 ImageStorage 的上层编排器（与具体厂商无关）：
// 把上游生图响应里的每张图片（b64_json 解码 / url 下载）转存到对象存储，
// 并把响应结果改写为只含短链接的紧凑 JSON，从而避免大 base64 落 Redis。
type ImageResultUploader struct {
	storage          ImageStorage
	httpClient       *http.Client
	customHTTPClient bool
	prefix           string
	maxDownloadBytes int64
}

// NewImageResultUploader 构造一个 uploader；storage 为 nil 时 Rewrite 直接透传。
func NewImageResultUploader(storage ImageStorage, prefix string, maxDownloadBytes int64, httpClient *http.Client) *ImageResultUploader {
	customHTTPClient := httpClient != nil
	if httpClient == nil {
		httpClient = defaultImageDownloadHTTPClient()
	}
	if maxDownloadBytes <= 0 {
		maxDownloadBytes = defaultImageMaxDownloadBytes
	}
	return &ImageResultUploader{
		storage:          storage,
		httpClient:       httpClient,
		customHTTPClient: customHTTPClient,
		prefix:           prefix,
		maxDownloadBytes: maxDownloadBytes,
	}
}

func defaultImageDownloadHTTPClient() *http.Client {
	policy := urlvalidator.OutboundPolicy{AllowInsecureHTTP: true}
	redirectGuard := urlvalidator.NewRedirectGuard(policy)
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           urlvalidator.NewSafeDialContext(policy),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &http.Client{
		Timeout:   60 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req == nil || req.URL == nil {
				return urlvalidator.ErrOutboundTargetNotAllowed
			}
			scheme := strings.ToLower(req.URL.Scheme)
			if scheme != "http" && scheme != "https" {
				return urlvalidator.ErrOutboundTargetNotAllowed
			}
			return redirectGuard(req, via)
		},
	}
}

// Rewrite 将 result（上游生图响应 JSON）里的每张图片转存到对象存储，
// 返回改写后的紧凑结果（data[i].url 指向对象存储，b64_json 被移除）。
// 任一图片转存失败即返回 error（调用方据此将任务标记为失败，绝不把大 blob 落 Redis）。
func (u *ImageResultUploader) Rewrite(ctx context.Context, taskID string, result json.RawMessage) (json.RawMessage, error) {
	if u == nil || u.storage == nil {
		return result, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(result, &top); err != nil {
		return nil, fmt.Errorf("parse image response: %w", err)
	}
	rawData, ok := top["data"]
	if !ok {
		// 没有 data 数组（结构不符合预期），保持原样返回，交由上层决定。
		return result, nil
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(rawData, &items); err != nil {
		return nil, fmt.Errorf("parse image response data: %w", err)
	}
	if len(items) == 0 {
		return result, nil
	}
	for i, item := range items {
		data, contentType, err := u.fetchImageBytes(ctx, item)
		if err != nil {
			return nil, fmt.Errorf("image %d: %w", i, err)
		}
		key := u.buildKey(taskID, i, contentType)
		url, err := u.storage.Save(ctx, key, contentType, data)
		if err != nil {
			return nil, fmt.Errorf("image %d: upload to object storage: %w", i, err)
		}
		urlRaw, err := json.Marshal(url)
		if err != nil {
			return nil, fmt.Errorf("image %d: encode url: %w", i, err)
		}
		item["url"] = urlRaw
		delete(item, "b64_json")
		items[i] = item
	}
	newData, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("encode image response data: %w", err)
	}
	top["data"] = newData
	out, err := json.Marshal(top)
	if err != nil {
		return nil, fmt.Errorf("encode image response: %w", err)
	}
	return out, nil
}

func (u *ImageResultUploader) fetchImageBytes(ctx context.Context, item map[string]json.RawMessage) ([]byte, string, error) {
	if raw, ok := item["b64_json"]; ok {
		var b64 string
		if err := json.Unmarshal(raw, &b64); err == nil {
			if b64 = strings.TrimSpace(b64); b64 != "" {
				limit := u.maxDownloadBytes
				if limit <= 0 {
					limit = defaultImageMaxDownloadBytes
				}
				decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(b64))
				data, err := io.ReadAll(io.LimitReader(decoder, limit+1))
				if err != nil {
					return nil, "", fmt.Errorf("decode b64_json: %w", err)
				}
				if int64(len(data)) > limit {
					return nil, "", fmt.Errorf("decoded b64_json exceeds %d bytes", limit)
				}
				contentType, err := validateImageBytes(data)
				if err != nil {
					return nil, "", fmt.Errorf("validate b64_json image: %w", err)
				}
				return data, contentType, nil
			}
		}
	}
	if raw, ok := item["url"]; ok {
		var rawURL string
		if err := json.Unmarshal(raw, &rawURL); err == nil {
			if rawURL = strings.TrimSpace(rawURL); rawURL != "" {
				if len(rawURL) >= len("data:") && strings.EqualFold(rawURL[:len("data:")], "data:") {
					return u.decodeImageDataURL(rawURL)
				}
				return u.download(ctx, rawURL)
			}
		}
	}
	return nil, "", errors.New("image item has neither b64_json nor url")
}

func (u *ImageResultUploader) decodeImageDataURL(rawURL string) ([]byte, string, error) {
	header, payload, ok := strings.Cut(rawURL[len("data:"):], ",")
	if !ok {
		return nil, "", errors.New("decode image data URL: missing comma separator")
	}

	parts := strings.Split(header, ";")
	if strings.TrimSpace(parts[0]) == "" {
		return nil, "", errors.New("decode image data URL: missing media type")
	}
	base64Index := len(parts) - 1
	if base64Index < 1 || !strings.EqualFold(strings.TrimSpace(parts[base64Index]), "base64") {
		for i := 1; i < base64Index; i++ {
			if strings.EqualFold(strings.TrimSpace(parts[i]), "base64") {
				return nil, "", errors.New("decode image data URL: base64 marker must be the final header token")
			}
		}
		return nil, "", errors.New("decode image data URL: payload is not base64 encoded")
	}
	for i := 1; i < base64Index; i++ {
		if strings.EqualFold(strings.TrimSpace(parts[i]), "base64") {
			return nil, "", errors.New("decode image data URL: duplicate base64 marker")
		}
	}
	mediaTypeHeader := strings.Join(parts[:base64Index], ";")
	declaredType, _, err := mime.ParseMediaType(mediaTypeHeader)
	if err != nil {
		return nil, "", fmt.Errorf("decode image data URL: invalid media type: %w", err)
	}
	declaredType = strings.ToLower(declaredType)
	if !strings.HasPrefix(declaredType, "image/") {
		return nil, "", fmt.Errorf("decode image data URL: media type %q is not an image", declaredType)
	}
	if !isAllowedDeclaredImageType(declaredType) {
		return nil, "", fmt.Errorf("decode image data URL: unsupported image media type %q", declaredType)
	}

	limit := u.maxDownloadBytes
	if limit <= 0 {
		limit = defaultImageMaxDownloadBytes
	}
	decoder := base64.NewDecoder(base64.StdEncoding, strings.NewReader(payload))
	data, err := io.ReadAll(io.LimitReader(decoder, limit+1))
	if err != nil {
		return nil, "", fmt.Errorf("decode image data URL base64 payload: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, "", fmt.Errorf("decoded image data URL exceeds %d bytes", limit)
	}

	contentType, err := validateImageBytes(data)
	if err != nil {
		return nil, "", fmt.Errorf("decode image data URL: %w", err)
	}
	return data, contentType, nil
}

func (u *ImageResultUploader) download(ctx context.Context, rawURL string) ([]byte, string, error) {
	if !u.customHTTPClient {
		if _, err := urlvalidator.ValidateOutboundURL(ctx, rawURL, urlvalidator.OutboundPolicy{AllowInsecureHTTP: true}); err != nil {
			return nil, "", fmt.Errorf("download image: %w", err)
		}
	} else {
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Host == "" || (strings.ToLower(parsed.Scheme) != "http" && strings.ToLower(parsed.Scheme) != "https") {
			return nil, "", fmt.Errorf("download image: %w", urlvalidator.ErrOutboundTargetNotAllowed)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("build download request: %w", err)
	}
	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("download image: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, "", fmt.Errorf("download image: unexpected status %d", resp.StatusCode)
	}
	limit := u.maxDownloadBytes
	if limit <= 0 {
		limit = defaultImageMaxDownloadBytes
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", fmt.Errorf("read image body: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, "", fmt.Errorf("downloaded image exceeds %d bytes", limit)
	}
	contentType, err := validateImageBytes(data)
	if err != nil {
		return nil, "", fmt.Errorf("download image: %w", err)
	}
	return data, contentType, nil
}

func (u *ImageResultUploader) buildKey(taskID string, index int, contentType string) string {
	return u.prefix + taskID + "-" + strconv.Itoa(index) + extensionForContentType(contentType)
}

func isAllowedDeclaredImageType(mediaType string) bool {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/png", "image/jpeg", "image/jpg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

// detectedImageContentType sniffs magic bytes and returns an allowlisted
// bitmap MIME type ("image/png", "image/jpeg", "image/webp", "image/gif"),
// or "" for anything else (never falls back to "image/png" and never accepts SVG).
func detectedImageContentType(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	ct := strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0]))
	switch ct {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return ct
	default:
		return ""
	}
}

// validateImageBytes verifies real bitmap bytes using both magic-byte sniffing
// and image.DecodeConfig. Only PNG, JPEG, WebP, and GIF are permitted; SVG,
// HTML, JSON, and corrupted/polyglot payloads are rejected.
func validateImageBytes(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("image payload is empty")
	}
	sniffed := detectedImageContentType(data)
	if sniffed == "" {
		return "", fmt.Errorf("unsupported or non-image content %q", strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0]))
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return "", errors.New("invalid or undecodable image payload")
	}
	var decodedMIME string
	switch strings.ToLower(format) {
	case "png":
		decodedMIME = "image/png"
	case "jpeg":
		decodedMIME = "image/jpeg"
	case "webp":
		decodedMIME = "image/webp"
	case "gif":
		decodedMIME = "image/gif"
	default:
		return "", fmt.Errorf("unsupported image format %q", format)
	}
	if decodedMIME != sniffed {
		return "", fmt.Errorf("image format mismatch: sniffed %q, decoded %q", sniffed, decodedMIME)
	}
	return decodedMIME, nil
}

func extensionForContentType(ct string) string {
	switch ct {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}
