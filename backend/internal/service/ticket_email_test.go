package service

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type ticketEmailRepoStub struct {
	*ticketRepoStub
	replayed          bool
	commitErr         error
	detailErr         error
	conversation      *TicketDetail
	conversationCalls int
}

func (repo *ticketEmailRepoStub) Mutate(_ context.Context, _ TicketActor, _ int64, _, _ string, change func(*Ticket) (*TicketChange, error)) error {
	if repo.replayed {
		return nil
	}
	plan, err := change(&repo.ticket)
	if err != nil {
		return err
	}
	for index := range plan.Messages {
		plan.Messages[index].ID = int64(index + 100)
	}
	return repo.commitErr
}

func (repo *ticketEmailRepoStub) Detail(ctx context.Context, actor TicketActor, id, before int64) (*TicketDetail, error) {
	if repo.detailErr != nil {
		return nil, repo.detailErr
	}
	return repo.ticketRepoStub.Detail(ctx, actor, id, before)
}

func (repo *ticketEmailRepoStub) EmailConversation(context.Context, int64, int64) (*TicketDetail, error) {
	repo.conversationCalls++
	return repo.conversation, nil
}

func TestTicketReplyWakesOnlyCommittedAdminReply(t *testing.T) {
	for _, test := range []struct {
		name                                      string
		admin, replay, failedCommit, failedDetail bool
		queued                                    int
	}{
		{"admin reply", true, false, false, false, 1},
		{"user follow up", false, false, false, false, 0},
		{"idempotent replay", true, true, false, false, 0},
		{"rolled back reply", true, false, true, false, 0},
		{"detail read fails after commit", true, false, false, true, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, original, actor := supportTicketTestService(test.admin)
			repo := &ticketEmailRepoStub{ticketRepoStub: original, replayed: test.replay}
			if test.failedCommit {
				repo.commitErr = errors.New("commit failed")
			}
			if test.failedDetail {
				repo.detailErr = errors.New("detail failed")
			}
			service.repo, service.emailWake, service.notificationEmails = repo, make(chan struct{}, 1), &NotificationEmailService{}
			_, err := service.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "客服回复", ClientID: ticketTestUUID})
			if test.failedCommit || test.failedDetail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if test.queued > 0 {
				require.Len(t, service.emailWake, 1)
			} else {
				require.Len(t, service.emailWake, 0)
			}
			require.Zero(t, repo.conversationCalls)
		})
	}
}

func TestTicketReplyEmailDeliversFullEscapedConversation(t *testing.T) {
	ctx := context.Background()
	settings := newNotificationEmailMemorySettingRepo()
	smtp := startNotificationEmailTestSMTPServer(t)
	require.NoError(t, settings.SetMultiple(ctx, smtp.settings()))
	require.NoError(t, settings.Set(ctx, SettingKeyFrontendURL, "https://support.example.com/"))
	require.NoError(t, settings.Set(ctx, SettingKeySiteName, "可达鸭"))
	notifications := NewNotificationEmailService(settings, NewEmailService(settings, nil))
	notifications.RememberRecipientLocale(ctx, 7, "owner@example.com", "zh-CN")
	ticket := &Ticket{ID: 10, UserID: 7, Subject: "API 请求排查 <script>bad()</script>", Contact: "attacker@example.com", UserEmail: "stale@example.com"}
	detail := &TicketDetail{Ticket: ticket}
	for index := int64(1); index <= 60; index++ {
		role := "user"
		if index%2 == 0 {
			role = "admin"
		}
		detail.Messages = append(detail.Messages, TicketMessage{ID: index, TicketID: 10, AuthorRole: role, AuthorName: "演示成员", Kind: "reply", Content: fmt.Sprintf("完整对话第 %d 条\n第二行 <img src=x onerror=alert(1)> {{site_name}}", index), CreatedAt: time.Date(2026, 9, 25, 8, int(index), 0, 0, time.UTC)})
	}
	repo := &ticketEmailRepoStub{conversation: detail}
	service := ProvideTicketService(repo, &ticketUsersStub{user: &User{ID: 7, Email: "owner@example.com", Username: "小鸭"}}, nil, notifications)
	require.NoError(t, service.sendTicketReplyEmail(ctx, 10, 60))
	require.Equal(t, int64(1), smtp.messageCount())
	message, err := mail.ReadMessage(strings.NewReader(smtp.lastMessage()))
	require.NoError(t, err)
	require.Equal(t, "<owner@example.com>", message.Header.Get("To"))
	subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	require.NoError(t, err)
	require.Contains(t, subject, "您的工单已有新的回复")
	body := smtp.lastMessageBody(t)
	require.Contains(t, body, "完整对话第 1 条")
	require.Contains(t, body, "完整对话第 60 条")
	require.Contains(t, body, "最新回复")
	require.Contains(t, body, "https://support.example.com/tickets/10")
	require.Contains(t, body, "&lt;img src=x onerror=alert(1)&gt;")
	require.Contains(t, body, "{{site_name}}")
	require.NotContains(t, body, "<script>")
	require.NotContains(t, body, "<img src=x")
	require.NotContains(t, body, "attacker@example.com")
	require.NotContains(t, body, "stale@example.com")
	require.NoError(t, service.sendTicketReplyEmail(ctx, 10, 60))
	require.Equal(t, int64(1), smtp.messageCount())
	if output := os.Getenv("TICKET_EMAIL_PREVIEW_DIR"); output != "" {
		require.NoError(t, os.MkdirAll(output, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(output, "ticket-reply-email.html"), []byte(body), 0o600))
	}
}

func TestTicketReplyEmailDoesNotNotifyDeletedOrWrongOwner(t *testing.T) {
	now := time.Now()
	for _, owner := range []*User{nil, {ID: 8, Email: "other@example.com"}, {ID: 7, Email: "owner@example.com", DeletedAt: &now}, {ID: 7, Email: "invalid\r\nBcc: injected@example.com"}} {
		repo := &ticketEmailRepoStub{conversation: &TicketDetail{Ticket: &Ticket{ID: 10, UserID: 7}}}
		service := ProvideTicketService(repo, &ticketUsersStub{user: owner}, nil, NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil))
		require.Error(t, service.sendTicketReplyEmail(context.Background(), 10, 60))
	}
}

func TestTicketEmailURLPrefersFrontendAndRejectsUnsafeURL(t *testing.T) {
	ctx := context.Background()
	settings := newNotificationEmailMemorySettingRepo()
	require.NoError(t, settings.Set(ctx, SettingKeyFrontendURL, "https://frontend.example.com/console/?tracking=1#old"))
	require.NoError(t, settings.Set(ctx, SettingKeyAPIBaseURL, "https://api.example.com"))
	require.Equal(t, "https://frontend.example.com/console/tickets/10", ticketEmailURL(ctx, settings, 10))
	require.NoError(t, settings.Set(ctx, SettingKeyFrontendURL, "javascript:alert(1)"))
	require.Equal(t, "https://api.example.com/tickets/10", ticketEmailURL(ctx, settings, 10))
	require.NoError(t, settings.Set(ctx, SettingKeyAPIBaseURL, "https://user:password@example.com"))
	require.Empty(t, ticketEmailURL(ctx, settings, 10))
}

func TestTicketEmailConversationKeepsBoundaryAndEscapesNames(t *testing.T) {
	body := ticketEmailConversation([]TicketMessage{{ID: 1, AuthorName: "<svg/onload=alert(1)>", Content: "第一行\n第二行", Kind: "reply"}, {ID: 2, Content: "newest", AuthorRole: "admin", Kind: "reply"}, {ID: 3, Content: "future reply", Kind: "reply"}}, 2, true)
	require.Contains(t, body, "第一行<br>第二行")
	require.Contains(t, body, "&lt;svg/onload=alert(1)&gt;")
	require.Contains(t, body, "最新回复")
	require.NotContains(t, body, "future reply")
	require.NotContains(t, body, "<svg")
}

func TestTicketEmailTemplatePreviewAndCustomValues(t *testing.T) {
	service := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)
	for _, locale := range []string{"zh", "en"} {
		preview, err := service.PreviewTemplate(context.Background(), NotificationEmailPreviewInput{Event: NotificationEmailEventTicketReply, Locale: locale})
		require.NoError(t, err)
		require.Contains(t, preview.HTML, "https://example.com/tickets/1024")
		require.Contains(t, preview.HTML, "UTC+8")
		require.NotContains(t, preview.Subject, "{{")
	}
	preview, err := service.PreviewTemplate(context.Background(), NotificationEmailPreviewInput{Event: NotificationEmailEventTicketReply, Locale: "zh", Variables: map[string]string{"conversation_html": "<script>alert(1)</script>", "ticket_url": "javascript:alert(1)"}})
	require.NoError(t, err)
	require.Contains(t, preview.HTML, "&lt;script&gt;")
	require.NotContains(t, preview.HTML, "<script>")
	require.NotContains(t, preview.HTML, "javascript:")
	variables := service.runtimeVariables(context.Background(), NotificationEmailEventTicketReply, "zh", NotificationEmailSendInput{})
	require.Empty(t, variables["conversation_html"])
	require.Empty(t, variables["ticket_url"])
	require.Equal(t, "none", variables["ticket_link_display"])
}
