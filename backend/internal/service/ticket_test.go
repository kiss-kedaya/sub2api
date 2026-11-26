package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

const ticketTestUUID = "6b7a9b04-1952-491c-ae6d-a2da990d8a8b"

type ticketUsersStub struct {
	UserRepository
	user *User
	err  error
}

func (s *ticketUsersStub) GetByID(context.Context, int64) (*User, error) { return s.user, s.err }

type ticketRepoStub struct {
	TicketRepository
	ticket               Ticket
	messages             []TicketMessage
	created              *CreateTicketInput
	hash                 string
	listed               bool
	lastFilter           TicketFilter
	mutated              bool
	proactiveUserID      int64
	proactiveSubject     string
	proactiveContent     string
	proactiveAttachments []int64
	proactiveImageOnly   bool
	uploadedRecord       *TicketAttachmentRecord
	attachmentContent    *TicketAttachmentContent
	attachmentErr        error
	deletedAttachmentID  int64
}

func (r *ticketRepoStub) Create(_ context.Context, _ TicketActor, in CreateTicketInput, hash string) (int64, error) {
	r.created, r.hash = &in, hash
	return 10, nil
}
func (r *ticketRepoStub) CreateForUser(_ context.Context, _ TicketActor, in AdminCreateTicketInput) (int64, error) {
	r.proactiveUserID, r.proactiveSubject, r.proactiveContent = in.UserID, in.Subject, in.Content
	r.proactiveAttachments, r.proactiveImageOnly = in.AttachmentIDs, in.ImageOnly
	return 10, nil
}
func (r *ticketRepoStub) Detail(context.Context, TicketActor, int64, int64) (*TicketDetail, error) {
	ticket := r.ticket
	return &TicketDetail{Ticket: &ticket, Messages: r.messages}, nil
}
func (r *ticketRepoStub) Mutate(_ context.Context, _ TicketActor, _ int64, _, hash string, fn func(*Ticket) (*TicketChange, error)) error {
	ticket := r.ticket
	plan, err := fn(&ticket)
	if err != nil {
		return err
	}
	r.ticket, r.messages, r.hash, r.mutated = *plan.Ticket, plan.Messages, hash, true
	return nil
}
func (r *ticketRepoStub) List(_ context.Context, _ TicketActor, f TicketFilter) ([]Ticket, int64, error) {
	r.listed = true
	r.lastFilter = f
	return []Ticket{r.ticket}, 1, nil
}
func (r *ticketRepoStub) UploadAttachment(_ context.Context, _ TicketActor, rec TicketAttachmentRecord) (*TicketAttachment, error) {
	r.uploadedRecord = &rec
	return &TicketAttachment{
		ID:          55,
		FileName:    rec.FileName,
		ContentType: rec.ContentType,
		SizeBytes:   rec.SizeBytes,
		Width:       rec.Width,
		Height:      rec.Height,
	}, nil
}
func (r *ticketRepoStub) GetAttachment(_ context.Context, _ TicketActor, _, _ int64) (*TicketAttachmentContent, error) {
	if r.attachmentErr != nil {
		return nil, r.attachmentErr
	}
	return r.attachmentContent, nil
}
func (r *ticketRepoStub) DeletePendingAttachment(_ context.Context, _ TicketActor, attachmentID int64) error {
	r.deletedAttachmentID = attachmentID
	return nil
}

func supportTicketTestService(admin bool) (*TicketService, *ticketRepoStub, TicketActor) {
	role := RoleUser
	if admin {
		role = RoleAdmin
	}
	repo := &ticketRepoStub{ticket: Ticket{ID: 10, UserID: 7, Status: "open", Priority: "normal"}}
	s := NewTicketService(repo, &ticketUsersStub{user: &User{ID: 7, Status: StatusActive, Role: role}})
	return s, repo, TicketActor{UserID: 7, Admin: admin}
}

func TestCreateForUserRequiresAdminAndDefaultsSubject(t *testing.T) {
	service, repo, actor := supportTicketTestService(true)
	detail, err := service.CreateForUser(context.Background(), actor, AdminCreateTicketInput{UserID: 7, Content: "我们检测到你的请求异常，请联系客服"})
	require.NoError(t, err)
	require.Equal(t, int64(10), detail.Ticket.ID)
	require.Equal(t, int64(7), repo.proactiveUserID)
	require.Equal(t, "客服消息", repo.proactiveSubject)
	require.Equal(t, "我们检测到你的请求异常，请联系客服", repo.proactiveContent)

	_, err = service.CreateForUser(context.Background(), TicketActor{UserID: 7}, AdminCreateTicketInput{UserID: 7, Content: "hello"})
	require.Error(t, err)
}

func TestTicketCreateValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*CreateTicketInput)
		valid  bool
	}{
		{"valid", func(*CreateTicketInput) {}, true},
		{"unicode boundaries", func(in *CreateTicketInput) {
			in.Subject = strings.Repeat("界", 160)
			in.Content = strings.Repeat("界", 10000)
			in.Contact = strings.Repeat("界", 200)
		}, true},
		{"blank subject", func(in *CreateTicketInput) { in.Subject = "  \n" }, false},
		{"long subject", func(in *CreateTicketInput) { in.Subject = strings.Repeat("界", 161) }, false},
		{"blank content", func(in *CreateTicketInput) { in.Content = " \n\t" }, false},
		{"long content", func(in *CreateTicketInput) { in.Content = strings.Repeat("界", 10001) }, false},
		{"long contact", func(in *CreateTicketInput) { in.Contact = strings.Repeat("界", 201) }, false},
		{"nul contact", func(in *CreateTicketInput) { in.Contact = "a\x00b" }, false},
		{"invalid utf8", func(in *CreateTicketInput) { in.Content = string([]byte{0xff}) }, false},
		{"invalid category", func(in *CreateTicketInput) { in.Category = "unknown" }, false},
		{"invalid priority", func(in *CreateTicketInput) { in.Priority = "low" }, false},
		{"missing UUID", func(in *CreateTicketInput) { in.ClientID = "" }, false},
		{"nil UUID", func(in *CreateTicketInput) { in.ClientID = "00000000-0000-0000-0000-000000000000" }, false},
		{"compact UUID", func(in *CreateTicketInput) { in.ClientID = strings.ReplaceAll(ticketTestUUID, "-", "") }, false},
		{"uppercase UUID", func(in *CreateTicketInput) { in.ClientID = strings.ToUpper(ticketTestUUID) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, actor := supportTicketTestService(false)
			in := CreateTicketInput{Subject: "help", Content: "request", Category: "api", Priority: "normal", ClientID: ticketTestUUID}
			tc.change(&in)
			_, err := s.Create(context.Background(), actor, in)
			if !tc.valid {
				require.Equal(t, 400, infraerrors.Code(err))
				require.Nil(t, repo.created)
				return
			}
			require.NoError(t, err)
			require.Equal(t, ticketTestUUID, repo.created.ClientID)
			require.Len(t, repo.hash, 64)
		})
	}
}

func TestTicketServiceRejectsAnonymousAndNonAdmin(t *testing.T) {
	for _, action := range []struct {
		name string
		run  func(*TicketService, TicketActor) error
	}{
		{"list", func(s *TicketService, a TicketActor) error {
			_, _, err := s.List(context.Background(), a, TicketFilter{})
			return err
		}},
		{"stats", func(s *TicketService, a TicketActor) error { _, err := s.Stats(context.Background(), a); return err }},
		{"detail", func(s *TicketService, a TicketActor) error {
			_, err := s.Detail(context.Background(), a, 10, 0)
			return err
		}},
		{"create", func(s *TicketService, a TicketActor) error {
			_, err := s.Create(context.Background(), a, CreateTicketInput{})
			return err
		}},
		{"reply", func(s *TicketService, a TicketActor) error {
			_, err := s.Reply(context.Background(), a, 10, ReplyTicketInput{})
			return err
		}},
		{"update", func(s *TicketService, a TicketActor) error {
			_, err := s.Update(context.Background(), a, 10, UpdateTicketInput{})
			return err
		}},
		{"read", func(s *TicketService, a TicketActor) error { return s.MarkRead(context.Background(), a, 10, 2) }},
	} {
		t.Run(action.name, func(t *testing.T) {
			s, _, _ := supportTicketTestService(false)
			require.Equal(t, 401, infraerrors.Code(action.run(s, TicketActor{})))
			require.Equal(t, 403, infraerrors.Code(action.run(s, TicketActor{UserID: 7, Admin: true})))
			s.users = &ticketUsersStub{user: &User{ID: 7, Status: "disabled", Role: RoleAdmin}}
			require.Equal(t, 401, infraerrors.Code(action.run(s, TicketActor{UserID: 7, Admin: true})))
		})
	}
}

func TestTicketReplyStateMachine(t *testing.T) {
	for _, admin := range []bool{false, true} {
		for _, status := range []string{"open", "in_progress", "waiting_user", "resolved", "closed"} {
			t.Run(TicketActor{Admin: admin}.Role()+"/"+status, func(t *testing.T) {
				s, repo, actor := supportTicketTestService(admin)
				repo.ticket.Status = status
				detail, err := s.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID})
				if status == "closed" {
					require.ErrorIs(t, err, ErrTicketClosed)
					require.False(t, repo.mutated)
					return
				}
				require.NoError(t, err)
				expected := "open"
				if admin {
					expected = "waiting_user"
				}
				require.Equal(t, expected, detail.Ticket.Status)
				require.Equal(t, "reply", detail.Messages[len(detail.Messages)-1].Kind)
				if status != expected {
					require.Equal(t, "status_changed", detail.Messages[0].EventType)
					require.Equal(t, status, detail.Messages[0].EventData["from"])
				}
			})
		}
	}
}

func TestTicketReplyPermissionsAndDefaults(t *testing.T) {
	s, repo, actor := supportTicketTestService(false)
	_, err := s.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID, Status: ticketTestString("resolved")})
	require.Equal(t, 400, infraerrors.Code(err))
	require.False(t, repo.mutated)
	repo.ticket.UserID = 8
	_, err = s.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID})
	require.ErrorIs(t, err, ErrTicketNotFound)
	s, repo, actor = supportTicketTestService(true)
	for _, status := range []string{"waiting_user", "in_progress", "resolved", "open", "closed", "bad"} {
		_, err = s.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID, Status: &status})
		if status == "open" || status == "closed" || status == "bad" {
			require.Equal(t, 400, infraerrors.Code(err))
		} else {
			require.NoError(t, err)
			require.Equal(t, status, repo.ticket.Status)
		}
	}
	_, err = s.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID})
	require.NoError(t, err)
	defaultHash := repo.hash
	_, err = s.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID, Status: ticketTestString("waiting_user")})
	require.NoError(t, err)
	require.Equal(t, defaultHash, repo.hash)
}

func TestTicketUpdatePermissionsAndEvents(t *testing.T) {
	for _, in := range []UpdateTicketInput{{}, {Status: ticketTestString("resolved")}, {Priority: ticketTestString("urgent")}, {AssignedTo: ticketTestString("me")}} {
		s, repo, a := supportTicketTestService(false)
		_, err := s.Update(context.Background(), a, 10, in)
		require.Equal(t, 400, infraerrors.Code(err))
		require.False(t, repo.mutated)
	}
	s, repo, a := supportTicketTestService(false)
	_, err := s.Update(context.Background(), a, 10, UpdateTicketInput{Status: ticketTestString("closed")})
	require.NoError(t, err)
	require.Equal(t, "closed", repo.ticket.Status)
	_, err = s.Update(context.Background(), a, 10, UpdateTicketInput{Status: ticketTestString("open")})
	require.NoError(t, err)
	require.Equal(t, "closed", repo.messages[0].EventData["from"])
	repo.ticket.UserID = 8
	_, err = s.Update(context.Background(), a, 10, UpdateTicketInput{Status: ticketTestString("closed")})
	require.ErrorIs(t, err, ErrTicketNotFound)
	s, repo, a = supportTicketTestService(true)
	_, err = s.Update(context.Background(), a, 10, UpdateTicketInput{Status: ticketTestString("closed"), Priority: ticketTestString("urgent"), AssignedTo: ticketTestString("me")})
	require.NoError(t, err)
	require.Equal(t, "closed", repo.ticket.Status)
	require.Equal(t, int64(7), *repo.ticket.AssigneeID)
	require.Len(t, repo.messages, 3)
	for _, m := range repo.messages {
		require.Equal(t, "event", m.Kind)
		require.NotEmpty(t, m.Content)
		require.Equal(t, "admin", m.AuthorRole)
	}
	_, err = s.Update(context.Background(), a, 10, UpdateTicketInput{AssignedTo: ticketTestString("unassigned")})
	require.NoError(t, err)
	require.Nil(t, repo.ticket.AssigneeID)
	_, err = s.Update(context.Background(), a, 10, UpdateTicketInput{AssignedTo: ticketTestString("123")})
	require.Equal(t, 400, infraerrors.Code(err))
}

func TestTicketFiltersAndCursorValidation(t *testing.T) {
	for _, admin := range []bool{false, true} {
		s, r, a := supportTicketTestService(admin)
		_, _, err := s.List(context.Background(), a, TicketFilter{PaginationParams: pagination.DefaultPagination(), Status: "active"})
		require.NoError(t, err)
		require.True(t, r.listed)
		_, err = s.Update(context.Background(), a, 10, UpdateTicketInput{Status: ticketTestString("active")})
		require.Equal(t, 400, infraerrors.Code(err))
	}
	for _, change := range []func(*TicketFilter){
		func(f *TicketFilter) { f.Page = 0 }, func(f *TicketFilter) { f.PageSize = 101 }, func(f *TicketFilter) { f.Page = int(^uint(0) >> 1) },
		func(f *TicketFilter) { f.Status = "bad" }, func(f *TicketFilter) { f.Priority = "bad" }, func(f *TicketFilter) { f.Category = "bad" },
		func(f *TicketFilter) { f.AssignedTo = "mine" }, func(f *TicketFilter) { f.Search = strings.Repeat("界", 161) },
		func(f *TicketFilter) { f.Search = string([]byte{0xff}) },
	} {
		s, r, a := supportTicketTestService(false)
		f := TicketFilter{PaginationParams: pagination.DefaultPagination()}
		change(&f)
		_, _, err := s.List(context.Background(), a, f)
		require.Equal(t, 400, infraerrors.Code(err))
		require.False(t, r.listed)
	}
	s, _, a := supportTicketTestService(false)
	_, err := s.Detail(context.Background(), a, 10, -1)
	require.Equal(t, 400, infraerrors.Code(err))
	require.ErrorIs(t, s.MarkRead(context.Background(), a, 10, 0), ErrTicketReadCursor)
	s.users = &ticketUsersStub{err: errors.New("lookup failed")}
	_, err = s.Detail(context.Background(), a, 10, 0)
	require.EqualError(t, err, "lookup failed")
}

func ticketTestString(value string) *string { return &value }

func ticketTestImageBytes(t *testing.T, format string, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 32, G: 112, B: 240, A: 255})
	var buf bytes.Buffer
	switch format {
	case "png":
		require.NoError(t, png.Encode(&buf, img))
	case "jpeg":
		require.NoError(t, jpeg.Encode(&buf, img, nil))
	case "gif":
		require.NoError(t, gif.Encode(&buf, img, nil))
	default:
		t.Fatalf("unsupported format %s", format)
	}
	return buf.Bytes()
}

func TestTicketUnreadFilterAcceptedInListAndRejectedInUpdate(t *testing.T) {
	for _, admin := range []bool{false, true} {
		s, r, a := supportTicketTestService(admin)
		_, _, err := s.List(context.Background(), a, TicketFilter{PaginationParams: pagination.DefaultPagination(), Status: "unread"})
		require.NoError(t, err)
		require.True(t, r.listed)
		require.Equal(t, "unread", r.lastFilter.Status)
		_, err = s.Update(context.Background(), a, 10, UpdateTicketInput{Status: ticketTestString("unread")})
		require.Equal(t, 400, infraerrors.Code(err))
	}
}

func TestTicketUploadAttachmentValidationAndSanitization(t *testing.T) {
	for _, format := range []struct {
		name, mime, ext string
	}{
		{"png", "image/png", "png"},
		{"jpeg", "image/jpeg", "jpg"},
		{"gif", "image/gif", "gif"},
	} {
		t.Run(format.name, func(t *testing.T) {
			s, repo, actor := supportTicketTestService(false)
			raw := ticketTestImageBytes(t, format.name, 16, 12)
			att, err := s.UploadAttachment(context.Background(), actor, UploadTicketAttachmentInput{
				FileName: `..\..\<b>shot</b>.` + format.ext,
				Data:     raw,
			})
			require.NoError(t, err)
			require.Equal(t, int64(55), att.ID)
			require.Equal(t, format.mime, att.ContentType)
			require.Equal(t, 16, att.Width)
			require.Equal(t, 12, att.Height)
			require.Equal(t, "shot."+format.ext, att.FileName)
			require.Equal(t, len(raw), repo.uploadedRecord.SizeBytes)
			require.Len(t, repo.uploadedRecord.SHA256, 64)
		})
	}

	s, _, actor := supportTicketTestService(false)
	// Rejects empty, SVG, HTML spoof, truncated PNG, GIF+HTML polyglot, pixel bomb, oversized, and unauthorized callers.
	_, err := s.UploadAttachment(context.Background(), actor, UploadTicketAttachmentInput{FileName: "empty.png", Data: nil})
	require.ErrorIs(t, err, ErrTicketAttachmentInvalid)

	_, err = s.UploadAttachment(context.Background(), actor, UploadTicketAttachmentInput{
		FileName: "xss.svg",
		Data:     []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`),
	})
	require.ErrorIs(t, err, ErrTicketAttachmentUnsupported)

	_, err = s.UploadAttachment(context.Background(), actor, UploadTicketAttachmentInput{
		FileName: "fake.png",
		Data:     []byte("<html><script>alert(1)</script></html>"),
	})
	require.ErrorIs(t, err, ErrTicketAttachmentUnsupported)

	// Truncated PNG (IHDR header only + HTML payload, no IDAT/IEND) must be rejected.
	truncatedPNG := append([]byte{}, ticketTestImageBytes(t, "png", 8, 8)[:33]...)
	truncatedPNG = append(truncatedPNG, []byte("<script>alert(1)</script>")...)
	_, err = s.UploadAttachment(context.Background(), actor, UploadTicketAttachmentInput{
		FileName: "truncated.png",
		Data:     truncatedPNG,
	})
	require.ErrorIs(t, err, ErrTicketAttachmentInvalid)

	// Header-only GIF89a + HTML polyglot must be rejected by full decode.
	gifPolyglot := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00<script>alert(1)</script>")
	_, err = s.UploadAttachment(context.Background(), actor, UploadTicketAttachmentInput{
		FileName: "polyglot.gif",
		Data:     gifPolyglot,
	})
	require.ErrorIs(t, err, ErrTicketAttachmentInvalid)

	// Valid minimal 1x1 lossless WebP succeeds and sanitizes dot-traversal filename edge cases.
	webp1x1 := []byte{
		'R', 'I', 'F', 'F', 0x1a, 0x00, 0x00, 0x00, 'W', 'E', 'B', 'P',
		'V', 'P', '8', 'L', 0x0e, 0x00, 0x00, 0x00, 0x2f, 0x00, 0x00, 0x00,
		0x10, 0x07, 0x10, 0x11, 0x11, 0x88, 0x88, 0xfe, 0x07, 0x00,
	}
	sWebp, _, webpActor := supportTicketTestService(false)
	attWebp, err := sWebp.UploadAttachment(context.Background(), webpActor, UploadTicketAttachmentInput{
		FileName: `.."`,
		Data:     webp1x1,
	})
	require.NoError(t, err)
	require.Equal(t, "image/webp", attWebp.ContentType)
	require.Equal(t, 1, attWebp.Width)
	require.Equal(t, 1, attWebp.Height)
	require.Equal(t, "image.webp", attWebp.FileName)

	// GIF claiming 8000x8000 (64M pixels > TicketMaxImagePixels) is rejected before pixel decode.
	gifPixelBomb := []byte{'G', 'I', 'F', '8', '9', 'a', 0x40, 0x1f, 0x40, 0x1f, 0x00, 0x00, 0x00}
	_, err = s.UploadAttachment(context.Background(), actor, UploadTicketAttachmentInput{
		FileName: "bomb.gif",
		Data:     gifPixelBomb,
	})
	require.ErrorIs(t, err, ErrTicketAttachmentInvalid)

	_, err = s.UploadAttachment(context.Background(), actor, UploadTicketAttachmentInput{
		FileName: "huge.png",
		Data:     make([]byte, TicketMaxAttachmentBytes+1),
	})
	require.ErrorIs(t, err, ErrTicketAttachmentTooLarge)

	_, err = s.UploadAttachment(context.Background(), TicketActor{}, UploadTicketAttachmentInput{
		FileName: "a.png",
		Data:     ticketTestImageBytes(t, "png", 4, 4),
	})
	require.Equal(t, 401, infraerrors.Code(err))

	// DeletePendingAttachment validates auth and ID before calling repository.
	sDel, repoDel, actorDel := supportTicketTestService(false)
	require.Equal(t, 401, infraerrors.Code(sDel.DeletePendingAttachment(context.Background(), TicketActor{}, 55)))
	require.Equal(t, 400, infraerrors.Code(sDel.DeletePendingAttachment(context.Background(), actorDel, 0)))
	require.NoError(t, sDel.DeletePendingAttachment(context.Background(), actorDel, 55))
	require.Equal(t, int64(55), repoDel.deletedAttachmentID)
}

func TestTicketAttachmentsCreateReplyAndProactive(t *testing.T) {
	// Image-only ticket creation works and marks ImageOnly=true.
	s, repo, actor := supportTicketTestService(false)
	_, err := s.Create(context.Background(), actor, CreateTicketInput{
		Subject:       "Screenshot issue",
		Content:       "   ",
		Category:      "api",
		Priority:      "normal",
		ClientID:      ticketTestUUID,
		AttachmentIDs: []int64{11, 12},
	})
	require.NoError(t, err)
	require.Equal(t, TicketImageOnlyContent, repo.created.Content)
	require.True(t, repo.created.ImageOnly)
	require.Equal(t, []int64{11, 12}, repo.created.AttachmentIDs)

	// Payload-only XSS text with attachments is still rejected.
	s, repo, actor = supportTicketTestService(false)
	_, err = s.Create(context.Background(), actor, CreateTicketInput{
		Subject:       "XSS attempt",
		Content:       `<img src=x onerror=alert(1)>`,
		Category:      "api",
		Priority:      "normal",
		ClientID:      ticketTestUUID,
		AttachmentIDs: []int64{11},
	})
	require.Equal(t, 400, infraerrors.Code(err))
	require.Nil(t, repo.created)

	// Image-only reply works and sets has_attachments + image_only in event_data.
	s, repo, actor = supportTicketTestService(false)
	_, err = s.Reply(context.Background(), actor, 10, ReplyTicketInput{
		Content:       "",
		ClientID:      ticketTestUUID,
		AttachmentIDs: []int64{21},
	})
	require.NoError(t, err)
	require.True(t, repo.mutated)
	require.Len(t, repo.messages, 1)
	require.Equal(t, TicketImageOnlyContent, repo.messages[0].Content)
	require.Equal(t, []int64{21}, repo.messages[0].AttachmentIDs)
	require.Equal(t, true, repo.messages[0].EventData["has_attachments"])
	require.Equal(t, true, repo.messages[0].EventData["image_only"])

	// Text + image reply preserves literal text without image_only.
	s, repo, actor = supportTicketTestService(false)
	_, err = s.Reply(context.Background(), actor, 10, ReplyTicketInput{
		Content:       TicketImageOnlyContent,
		ClientID:      ticketTestUUID,
		AttachmentIDs: []int64{22},
	})
	require.NoError(t, err)
	require.Equal(t, true, repo.messages[0].EventData["has_attachments"])
	require.Nil(t, repo.messages[0].EventData["image_only"])

	// Rejects >4 images, duplicate IDs, and non-positive IDs.
	for _, ids := range [][]int64{{1, 2, 3, 4, 5}, {7, 7}, {0}, {-1}} {
		s, repo, actor = supportTicketTestService(false)
		_, err = s.Reply(context.Background(), actor, 10, ReplyTicketInput{
			Content:       "check",
			ClientID:      ticketTestUUID,
			AttachmentIDs: ids,
		})
		require.Equal(t, 400, infraerrors.Code(err), "ids=%v", ids)
		require.False(t, repo.mutated)
	}

	// Admin proactive image-only message works.
	s, repo, actor = supportTicketTestService(true)
	_, err = s.CreateForUser(context.Background(), actor, AdminCreateTicketInput{
		UserID:        7,
		Content:       "",
		AttachmentIDs: []int64{31},
	})
	require.NoError(t, err)
	require.Equal(t, TicketImageOnlyContent, repo.proactiveContent)
	require.Equal(t, []int64{31}, repo.proactiveAttachments)
	require.True(t, repo.proactiveImageOnly)

	// GetAttachment rejects invalid IDs and unsafe stored MIME types.
	s, repo, actor = supportTicketTestService(false)
	_, err = s.GetAttachment(context.Background(), actor, 0, 1)
	require.Equal(t, 400, infraerrors.Code(err))
	repo.attachmentContent = &TicketAttachmentContent{
		TicketAttachment: TicketAttachment{ID: 1, ContentType: "image/svg+xml"},
		Data:             []byte("<svg/>"),
	}
	_, err = s.GetAttachment(context.Background(), actor, 10, 1)
	require.ErrorIs(t, err, ErrTicketAttachmentNotFound)
}
