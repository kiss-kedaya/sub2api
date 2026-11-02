package dto

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type Announcement struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	Status     string `json:"status"`
	NotifyMode string `json:"notify_mode"`

	Targeting service.AnnouncementTargeting `json:"targeting"`

	StartsAt *time.Time `json:"starts_at,omitempty"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`

	CreatedBy *int64 `json:"created_by,omitempty"`
	UpdatedBy *int64 `json:"updated_by,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UserAnnouncement struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content,omitempty"`
	// Excerpt is the one-line preview used by the timeline; the body is only
	// sent by the detail endpoint.
	Excerpt    string `json:"excerpt,omitempty"`
	NotifyMode string `json:"notify_mode"`

	StartsAt *time.Time `json:"starts_at,omitempty"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`

	ReadAt *time.Time `json:"read_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func AnnouncementFromService(a *service.Announcement) *Announcement {
	if a == nil {
		return nil
	}
	return &Announcement{
		ID:         a.ID,
		Title:      a.Title,
		Content:    a.Content,
		Status:     a.Status,
		NotifyMode: a.NotifyMode,
		Targeting:  a.Targeting,
		StartsAt:   a.StartsAt,
		EndsAt:     a.EndsAt,
		CreatedBy:  a.CreatedBy,
		UpdatedBy:  a.UpdatedBy,
		CreatedAt:  a.CreatedAt,
		UpdatedAt:  a.UpdatedAt,
	}
}

// UserAnnouncementListFromService drops the body so the timeline stays small.
func UserAnnouncementListFromService(a *service.UserAnnouncement) *UserAnnouncement {
	out := UserAnnouncementFromService(a)
	if out == nil {
		return nil
	}
	out.Excerpt = announcementExcerpt(a.Announcement.Content)
	out.Content = ""
	return out
}

const announcementExcerptRunes = 140

func announcementExcerpt(content string) string {
	text := strings.Join(strings.Fields(content), " ")
	if utf8.RuneCountInString(text) <= announcementExcerptRunes {
		return text
	}
	return string([]rune(text)[:announcementExcerptRunes])
}

func UserAnnouncementFromService(a *service.UserAnnouncement) *UserAnnouncement {
	if a == nil {
		return nil
	}
	return &UserAnnouncement{
		ID:         a.Announcement.ID,
		Title:      a.Announcement.Title,
		Content:    a.Announcement.Content,
		NotifyMode: a.Announcement.NotifyMode,
		StartsAt:   a.Announcement.StartsAt,
		EndsAt:     a.Announcement.EndsAt,
		ReadAt:     a.ReadAt,
		CreatedAt:  a.Announcement.CreatedAt,
		UpdatedAt:  a.Announcement.UpdatedAt,
	}
}
