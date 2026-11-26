package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/util/textsanitize"
	_ "golang.org/x/image/webp"
)

const (
	TicketMaxAttachmentBytes           = 5 << 20 // 5 MiB
	TicketMaxAttachmentsPerMessage     = 4
	TicketMaxImageDimension            = 8192
	TicketMaxImagePixels               = 25_000_000
	TicketMaxPendingAttachmentsPerUser = 30
	TicketImageOnlyContent             = "[图片]"
)

var (
	ErrTicketAttachmentNotFound    = infraerrors.NotFound("TICKET_ATTACHMENT_NOT_FOUND", "ticket attachment not found")
	ErrTicketAttachmentTooLarge    = infraerrors.BadRequest("TICKET_ATTACHMENT_TOO_LARGE", "image must be 5MB or smaller")
	ErrTicketAttachmentUnsupported = infraerrors.BadRequest("TICKET_ATTACHMENT_UNSUPPORTED", "only PNG, JPEG, WebP, or GIF images are supported")
	ErrTicketAttachmentInvalid     = infraerrors.BadRequest("TICKET_ATTACHMENT_INVALID", "invalid or unavailable image attachment")
)

type TicketAttachment struct {
	ID          int64     `json:"id"`
	TicketID    *int64    `json:"ticket_id,omitempty"`
	MessageID   *int64    `json:"message_id,omitempty"`
	FileName    string    `json:"file_name"`
	ContentType string    `json:"content_type"`
	SizeBytes   int       `json:"size_bytes"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	CreatedAt   time.Time `json:"created_at"`
}

type TicketAttachmentContent struct {
	TicketAttachment
	Data []byte `json:"-"`
}

type UploadTicketAttachmentInput struct {
	FileName string
	Data     []byte
}

type TicketAttachmentRecord struct {
	FileName    string
	ContentType string
	SizeBytes   int
	Width       int
	Height      int
	SHA256      string
	Data        []byte
}

func IsAllowedTicketImageContentType(value string) bool {
	switch value {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

func detectTicketImage(raw []byte) (string, int, int, error) {
	if len(raw) == 0 {
		return "", 0, 0, ErrTicketAttachmentInvalid
	}
	if len(raw) > TicketMaxAttachmentBytes {
		return "", 0, 0, ErrTicketAttachmentTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return "", 0, 0, ErrTicketAttachmentUnsupported
	}
	var contentType string
	switch format {
	case "png":
		contentType = "image/png"
	case "jpeg":
		contentType = "image/jpeg"
	case "webp":
		contentType = "image/webp"
	case "gif":
		contentType = "image/gif"
	default:
		return "", 0, 0, ErrTicketAttachmentUnsupported
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > TicketMaxImageDimension || cfg.Height > TicketMaxImageDimension ||
		int64(cfg.Width)*int64(cfg.Height) > TicketMaxImagePixels {
		return "", 0, 0, ErrTicketAttachmentInvalid
	}
	// Full decode after dimension/pixel guards rejects truncated images and
	// header-only polyglots (e.g., GIF89a header + HTML payload) before storage.
	img, decodedFormat, err := image.Decode(bytes.NewReader(raw))
	if err != nil || img == nil || decodedFormat != format || img.Bounds().Empty() {
		return "", 0, 0, ErrTicketAttachmentInvalid
	}
	return contentType, cfg.Width, cfg.Height, nil
}

func sanitizeTicketAttachmentFileName(raw, contentType string) string {
	cleanedInput := textsanitize.PlainText(raw)
	normalized := strings.ReplaceAll(cleanedInput, "\\", "/")
	base := path.Base(normalized)
	if base == "." || base == ".." || base == "/" {
		base = ""
	}
	var b strings.Builder
	for _, r := range base {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\'' || r == '/' || r == '\\' || r == '<' || r == '>' {
			continue
		}
		b.WriteRune(r)
	}
	cleaned := strings.TrimSpace(b.String())
	if runes := []rune(cleaned); len(runes) > 120 {
		cleaned = string(runes[:120])
	}
	if cleaned != "" && cleaned != "." && cleaned != ".." {
		return cleaned
	}
	switch contentType {
	case "image/jpeg":
		return "image.jpg"
	case "image/webp":
		return "image.webp"
	case "image/gif":
		return "image.gif"
	default:
		return "image.png"
	}
}

func ticketNormalizeAttachmentIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > TicketMaxAttachmentsPerMessage {
		return nil, ticketInvalid("at most 4 images per message")
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrTicketAttachmentInvalid
		}
		if _, dup := seen[id]; dup {
			return nil, ErrTicketAttachmentInvalid
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

func ticketNormalizeMessageContent(raw string, hasAttachments bool) (string, bool, error) {
	if !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
		return "", false, ticketInvalid("content must contain 1-10000 characters")
	}
	rawTrimmed := strings.TrimSpace(raw)
	sanitized := textsanitize.PlainText(raw)
	if rawTrimmed == "" {
		if hasAttachments {
			return TicketImageOnlyContent, true, nil
		}
		return "", false, ticketInvalid("content must contain 1-10000 characters")
	}
	if !ticketText(sanitized, 10000) {
		return "", false, ticketInvalid("content must contain 1-10000 characters")
	}
	return sanitized, false, nil
}

func ticketAttachmentEventData(hasAttachments, imageOnly bool) map[string]any {
	if !hasAttachments {
		return nil
	}
	data := map[string]any{"has_attachments": true}
	if imageOnly {
		data["image_only"] = true
	}
	return data
}

func (s *TicketService) UploadAttachment(ctx context.Context, actor TicketActor, in UploadTicketAttachmentInput) (*TicketAttachment, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	contentType, width, height, err := detectTicketImage(in.Data)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(in.Data)
	record := TicketAttachmentRecord{
		FileName:    sanitizeTicketAttachmentFileName(in.FileName, contentType),
		ContentType: contentType,
		SizeBytes:   len(in.Data),
		Width:       width,
		Height:      height,
		SHA256:      hex.EncodeToString(sum[:]),
		Data:        in.Data,
	}
	return s.repo.UploadAttachment(ctx, actor, record)
}

func (s *TicketService) GetAttachment(ctx context.Context, actor TicketActor, ticketID, attachmentID int64) (*TicketAttachmentContent, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	if ticketID <= 0 || attachmentID <= 0 {
		return nil, ticketInvalid("invalid ticket or attachment ID")
	}
	att, err := s.repo.GetAttachment(ctx, actor, ticketID, attachmentID)
	if err != nil {
		return nil, err
	}
	if att == nil || !IsAllowedTicketImageContentType(att.ContentType) {
		return nil, ErrTicketAttachmentNotFound
	}
	return att, nil
}

func (s *TicketService) DeletePendingAttachment(ctx context.Context, actor TicketActor, attachmentID int64) error {
	if err := s.authorize(ctx, actor); err != nil {
		return err
	}
	if attachmentID <= 0 {
		return ticketInvalid("invalid attachment ID")
	}
	return s.repo.DeletePendingAttachment(ctx, actor, attachmentID)
}
