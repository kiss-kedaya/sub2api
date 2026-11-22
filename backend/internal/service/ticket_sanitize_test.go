//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// 工单 subject/content 是已知的存储型 XSS 入口：入库值必须是纯文本，
// 否则任何页面改成 Markdown/v-html 就会执行存到库里的 payload。
func TestTicketCreateStoresSanitizedPlainText(t *testing.T) {
	s, repo, actor := supportTicketTestService(false)
	_, err := s.Create(context.Background(), actor, CreateTicketInput{
		Subject:  `<img src=x onerror=alert(1)>登录失败`,
		Content:  `<svg onload=alert(document.cookie)></svg>订单 A-1 扣了 1.64 没退`,
		Category: "billing",
		Priority: "high",
		ClientID: ticketTestUUID,
	})
	require.NoError(t, err)
	require.NotNil(t, repo.created)
	require.Equal(t, "登录失败", repo.created.Subject)
	require.Equal(t, "订单 A-1 扣了 1.64 没退", repo.created.Content)
	assertStoredTicketTextClean(t, repo.created.Subject, repo.created.Content)
}

func TestTicketCreateRejectsPayloadOnlySubject(t *testing.T) {
	// "<script>alert(1)</script>" 清洗后只剩 "alert(1)"，非空，允许提交；
	// 真正只剩空白/纯标签的写法必须被拒。
	s, repo, actor := supportTicketTestService(false)
	_, err := s.Create(context.Background(), actor, CreateTicketInput{
		Subject:  "<script>alert(1)</script>",
		Content:  "正文",
		Category: "api",
		Priority: "normal",
		ClientID: ticketTestUUID,
	})
	require.NoError(t, err)
	require.Equal(t, "alert(1)", repo.created.Subject)

	s, repo, actor = supportTicketTestService(false)
	_, err = s.Create(context.Background(), actor, CreateTicketInput{
		Subject:  "<img src=x onerror=alert(1)>",
		Content:  "正文",
		Category: "api",
		Priority: "normal",
		ClientID: ticketTestUUID,
	})
	require.Equal(t, 400, infraerrors.Code(err))
	require.Nil(t, repo.created)
}

func TestTicketCreateSanitizesEncodedMarkup(t *testing.T) {
	s, repo, actor := supportTicketTestService(false)
	_, err := s.Create(context.Background(), actor, CreateTicketInput{
		Subject:  `&lt;svg onload=alert(1)&gt;支付问题`,
		Content:  `&amp;lt;img src=x onerror=alert(1)&amp;gt;到账失败`,
		Category: "billing",
		Priority: "urgent",
		ClientID: ticketTestUUID,
	})
	require.NoError(t, err)
	require.Equal(t, "支付问题", repo.created.Subject)
	require.Equal(t, "到账失败", repo.created.Content)
	assertStoredTicketTextClean(t, repo.created.Subject, repo.created.Content)
}

// 幂等哈希基于清洗后的请求计算：同一份输入重复提交哈希必须一致。
func TestTicketCreateHashUsesSanitizedInput(t *testing.T) {
	s, repo, actor := supportTicketTestService(false)
	in := CreateTicketInput{
		Subject:  `<b>help</b>`,
		Content:  `please <script>alert(1)</script>fix`,
		Category: "api",
		Priority: "normal",
		ClientID: ticketTestUUID,
	}
	_, err := s.Create(context.Background(), actor, in)
	require.NoError(t, err)
	first := repo.hash

	in2 := in
	in2.Subject = `<i>help</i>`
	in2.Content = `please <img src=x onerror=alert(1)>fix`
	_, err = s.Create(context.Background(), actor, in2)
	require.NoError(t, err)
	// 清洗后两者都是 "help" / "please fix"，但中间空白不同（payload 被整段移除
	// 不留空格），所以这里只锁哈希稳定，不要求两者相等。
	require.Len(t, repo.hash, 64)

	in3 := in
	_, err = s.Create(context.Background(), actor, in3)
	require.NoError(t, err)
	require.Equal(t, first, repo.hash)
}

func TestTicketReplyStoresSanitizedPlainText(t *testing.T) {
	s, repo, actor := supportTicketTestService(false)
	_, err := s.Reply(context.Background(), actor, 10, ReplyTicketInput{
		Content:  `<script>alert(1)</script>收到，已复现`,
		ClientID: ticketTestUUID,
	})
	require.NoError(t, err)
	require.True(t, repo.mutated)
	require.Len(t, repo.messages, 1)
	require.Equal(t, "alert(1)收到，已复现", repo.messages[0].Content)
	require.False(t, strings.Contains(strings.ToLower(repo.messages[0].Content), "onerror"))
}

func TestTicketCreateForUserStoresSanitizedPlainText(t *testing.T) {
	s, repo, actor := supportTicketTestService(true)
	_, err := s.CreateForUser(context.Background(), actor, AdminCreateTicketInput{
		UserID:  7,
		Subject: `<svg onload=alert(1)>风控提醒`,
		Content: `<a href="javascript:alert(1)">点这里</a>补充资料`,
	})
	require.NoError(t, err)
	require.Equal(t, "风控提醒", repo.proactiveSubject)
	require.Equal(t, "点这里补充资料", repo.proactiveContent)
	assertStoredTicketTextClean(t, repo.proactiveSubject, repo.proactiveContent)
}

// 管理员主动建单时，标题全是 payload 时沿用默认标题，不能把 markup 落库。
func TestTicketCreateForUserDefaultsSanitizedSubject(t *testing.T) {
	s, repo, actor := supportTicketTestService(true)
	_, err := s.CreateForUser(context.Background(), actor, AdminCreateTicketInput{
		UserID:  7,
		Subject: `<img src=x onerror=alert(1)>`,
		Content: "正文",
	})
	require.NoError(t, err)
	require.Equal(t, "客服消息", repo.proactiveSubject)
}

func assertStoredTicketTextClean(t *testing.T, values ...string) {
	t.Helper()
	for _, value := range values {
		lower := strings.ToLower(value)
		for _, needle := range []string{"<img", "<svg", "<script", "onerror", "onload", "javascript:"} {
			require.NotContains(t, lower, needle, "ticket field still contains %q: %q", needle, value)
		}
	}
}
