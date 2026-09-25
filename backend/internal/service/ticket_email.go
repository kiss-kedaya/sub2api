package service

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func ProvideTicketService(repo TicketRepository, users UserRepository, queue *EmailQueueService, notifications *NotificationEmailService) *TicketService {
	service := NewTicketService(repo, users)
	service.notificationEmails = notifications
	service.emailWake = make(chan struct{}, 1)
	if queue != nil && notifications != nil {
		queue.wg.Add(1)
		go func() {
			defer queue.wg.Done()
			service.runTicketEmailWorker(queue.stopChan)
		}()
	}
	return service
}

func (s *TicketService) notifyTicketReply() {
	select {
	case s.emailWake <- struct{}{}:
	default:
	}
}

func (s *TicketService) runTicketEmailWorker(stop <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		for count := 0; count < 10; count++ {
			select {
			case <-stop:
				return
			default:
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			processed, err := s.repo.ProcessPendingEmail(ctx, func(sendCtx context.Context, ticketID, messageID int64) error {
				err := s.sendTicketReplyEmail(sendCtx, ticketID, messageID)
				if err != nil {
					slog.Warn("ticket reply email deferred for retry", "ticket_id", ticketID, "message_id", messageID, "error_type", fmt.Sprintf("%T", err))
				}
				return err
			})
			cancel()
			if err != nil {
				slog.Error("ticket email outbox processing failed", "error_type", fmt.Sprintf("%T", err))
			}
			if err != nil || !processed {
				break
			}
		}
		select {
		case <-stop:
			return
		case <-s.emailWake:
		case <-ticker.C:
		}
	}
}

func (s *TicketService) sendTicketReplyEmail(ctx context.Context, ticketID, messageID int64) error {
	detail, err := s.repo.EmailConversation(ctx, ticketID, messageID)
	if err != nil {
		return err
	}
	if detail == nil || detail.Ticket == nil || detail.Ticket.ID != ticketID {
		return ErrTicketNotFound
	}
	recipient, err := s.users.GetByID(ctx, detail.Ticket.UserID)
	if err != nil {
		return err
	}
	if recipient == nil || recipient.DeletedAt != nil || recipient.ID != detail.Ticket.UserID {
		return ErrTicketNotFound
	}
	if _, err := parseSMTPAddress(recipient.Email, "recipient"); err != nil {
		return fmt.Errorf("ticket owner has no valid email address")
	}
	locale := s.notificationEmails.ResolveRecipientLocale(ctx, recipient.ID, recipient.Email)
	chinese := locale == notificationEmailLocaleChinese
	link := ticketEmailURL(ctx, s.notificationEmails.settingRepo, ticketID)
	display := "table"
	if link == "" {
		display = "none"
	}
	err = s.notificationEmails.Send(ctx, NotificationEmailSendInput{
		Event: NotificationEmailEventTicketReply, Locale: locale,
		RecipientEmail: recipient.Email, RecipientName: recipient.Username, UserID: recipient.ID,
		SourceType: "support_ticket_message", SourceID: strconv.FormatInt(messageID, 10),
		Variables:        map[string]string{"ticket_id": strconv.FormatInt(ticketID, 10), "ticket_subject": detail.Ticket.Subject, "ticket_url": link, "ticket_link_display": display},
		RawHTMLVariables: map[string]string{"conversation_html": ticketEmailConversation(detail.Messages, messageID, chinese)},
	})
	var receiptErr notificationEmailReceiptError
	if errors.As(err, &receiptErr) {
		slog.Warn("ticket email delivered without receipt; completing outbox job", "ticket_id", ticketID, "message_id", messageID)
		return nil
	}
	return err
}

func ticketEmailURL(ctx context.Context, settings SettingRepository, ticketID int64) string {
	if settings == nil {
		return ""
	}
	for _, key := range []string{SettingKeyFrontendURL, SettingKeyAPIBaseURL} {
		value, err := settings.GetValue(ctx, key)
		if err != nil {
			continue
		}
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			continue
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/tickets/" + strconv.FormatInt(ticketID, 10)
		parsed.RawPath, parsed.RawQuery, parsed.Fragment = "", "", ""
		return parsed.String()
	}
	return ""
}

func ticketEmailConversation(messages []TicketMessage, latestID int64, chinese bool) string {
	var body strings.Builder
	zone := time.FixedZone("UTC+8", 8*60*60)
	for _, message := range messages {
		if message.ID > latestID {
			continue
		}
		role, color, background := "You", "#475569", "#f8fafc"
		if chinese {
			role = "您"
		}
		if message.AuthorRole == "admin" {
			role, color, background = "Support", "#0f766e", "#f0fdfa"
			if chinese {
				role = "客服"
			}
		}
		if message.Kind == "event" {
			role, color, background = "Update", "#64748b", "#f8fafc"
			if chinese {
				role = "工单动态"
			}
		}
		label := html.EscapeString(role)
		if message.AuthorName != "" && message.Kind != "event" {
			label += " · " + html.EscapeString(message.AuthorName)
		}
		border := "#e2e8f0"
		if message.ID == latestID {
			border = "#5eead4"
			if chinese {
				label += " · 最新回复"
			} else {
				label += " · New reply"
			}
		}
		content := html.EscapeString(strings.ReplaceAll(message.Content, "\r\n", "\n"))
		content = strings.ReplaceAll(content, "\n", "<br>")
		_, _ = fmt.Fprintf(&body, `<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="width:100%%;table-layout:fixed;margin:0 0 16px;border:1px solid %s;border-radius:12px;background:%s"><tr><td style="padding:16px 18px;word-break:break-word;overflow-wrap:anywhere"><p style="margin:0 0 6px;font-size:13px;font-weight:700;color:%s">%s</p><p style="margin:0 0 12px;font-size:11px;color:#64748b">%s · UTC+8</p><div style="font-size:14px;line-height:1.8;color:#1e293b;white-space:pre-wrap;word-break:break-word;overflow-wrap:anywhere">%s</div></td></tr></table>`, border, background, color, label, message.CreatedAt.In(zone).Format("2006-01-02 15:04"), content)
	}
	return body.String()
}

func ticketReplyEmailTemplate(chinese bool) string {
	title, intro, history, action, footer := "Your ticket has a new reply", "Our support team has replied. The complete conversation up to this reply is included below.", "Conversation history", "Open your ticket center", "To continue this conversation, please reply in the ticket center rather than to this email."
	lang := "en"
	if chinese {
		lang = "zh-CN"
		title, intro, history, action, footer = "您的工单已有新的回复", "客服已回复您的工单。以下附上截至本次回复的完整对话，方便您随时查阅。", "完整对话记录", "前往工单中心", "如需继续沟通，请进入工单中心回复，无需直接回复此邮件。"
	}
	return fmt.Sprintf(`<!doctype html><html lang="%s"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%s</title></head><body style="margin:0;padding:0;background:#f1f5f9;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','Microsoft YaHei',sans-serif;color:#0f172a"><div style="display:none;max-height:0;overflow:hidden;opacity:0">%s · {{ticket_subject}}</div><table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background:#f1f5f9"><tr><td align="center" style="padding:28px 12px"><table role="presentation" width="640" cellpadding="0" cellspacing="0" style="width:100%%;max-width:640px;table-layout:fixed;background:#ffffff;border:1px solid #e2e8f0;border-radius:18px"><tr><td style="padding:28px 24px;background:#0f3d3a;border-radius:18px 18px 0 0;color:#ffffff;word-break:break-word"><p style="margin:0 0 22px;font-size:13px;letter-spacing:1px;color:#99f6e4">{{site_name}} · SUPPORT</p><h1 style="margin:0 0 12px;font-size:24px;line-height:1.4;color:#ffffff">%s</h1><p style="margin:0;font-size:14px;line-height:1.7;color:#ccfbf1">%s</p></td></tr><tr><td style="padding:24px;word-break:break-word;overflow-wrap:anywhere"><p style="margin:0 0 8px;font-size:12px;font-weight:700;color:#0f766e">TICKET #{{ticket_id}}</p><h2 style="margin:0 0 20px;font-size:19px;line-height:1.5">{{ticket_subject}}</h2><table role="presentation" cellpadding="0" cellspacing="0" style="display:{{ticket_link_display}};margin:0 0 28px"><tr><td style="border-radius:8px;background:#0f766e"><a href="{{ticket_url}}" style="display:inline-block;padding:12px 20px;color:#ffffff;text-decoration:none;font-size:14px;font-weight:700">%s →</a></td></tr></table><h3 style="margin:0 0 18px;padding-top:20px;border-top:1px solid #e2e8f0;font-size:14px;color:#475569">%s</h3>{{conversation_html}}</td></tr><tr><td style="padding:20px 24px;background:#f8fafc;border-top:1px solid #e2e8f0;border-radius:0 0 18px 18px"><p style="margin:0;font-size:12px;line-height:1.8;color:#64748b">%s</p><p style="margin:8px 0 0;font-size:11px;color:#94a3b8">{{site_name}}</p></td></tr></table></td></tr></table></body></html>`, lang, title, title, title, intro, action, history, footer)
}

func ticketEmailPreviewConversation(chinese bool) string {
	question, answer := "Could you help me check this API request?", "We have reviewed your request. Please retry and let us know if you need anything else."
	if chinese {
		question, answer = "你好，我的 API 请求遇到了问题，可以帮忙看一下吗？", "您好，我们已完成检查。请重新发起请求试试，如有其他问题，欢迎在本工单继续回复。"
	}
	created := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	return ticketEmailConversation([]TicketMessage{
		{ID: 1, AuthorRole: "user", Kind: "reply", Content: question, CreatedAt: created},
		{ID: 2, AuthorRole: "admin", Kind: "reply", Content: answer, CreatedAt: created.Add(5 * time.Minute)},
	}, 2, chinese)
}
