package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type TicketHandler struct{ tickets *service.TicketService }

func NewTicketHandler(tickets *service.TicketService) *TicketHandler {
	return &TicketHandler{tickets: tickets}
}

func ticketActor(c *gin.Context, admin bool) (service.TicketActor, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "authentication required")
		return service.TicketActor{}, false
	}
	if admin {
		role, ok := middleware.GetUserRoleFromContext(c)
		if !ok || role != service.RoleAdmin {
			response.Forbidden(c, "admin access required")
			return service.TicketActor{}, false
		}
	}
	return service.TicketActor{UserID: subject.UserID, Admin: admin}, true
}

func ticketID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid ticket ID")
		return 0, false
	}
	return id, true
}

func ticketBody(c *gin.Context, body any) bool {
	// 10,000 Unicode runes also fit when JSON encodes them as escape sequences.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		response.BadRequest(c, "invalid ticket request body")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		response.BadRequest(c, "invalid ticket request body")
		return false
	}
	return true
}

func ticketDetailResponse(c *gin.Context, detail *service.TicketDetail, err error, admin bool) {
	if response.ErrorFrom(c, err) {
		return
	}
	if !admin {
		detail.Ticket.UserEmail = ""
		detail.Ticket.UserName = ""
		detail.Requester = nil
	}
	response.Success(c, detail)
}

func (h *TicketHandler) List(c *gin.Context)          { h.list(c, false) }
func (h *TicketHandler) AdminList(c *gin.Context)     { h.list(c, true) }
func (h *TicketHandler) Stats(c *gin.Context)         { h.stats(c, false) }
func (h *TicketHandler) AdminStats(c *gin.Context)    { h.stats(c, true) }
func (h *TicketHandler) Detail(c *gin.Context)        { h.detail(c, false) }
func (h *TicketHandler) AdminDetail(c *gin.Context)   { h.detail(c, true) }
func (h *TicketHandler) Reply(c *gin.Context)         { h.reply(c, false) }
func (h *TicketHandler) AdminReply(c *gin.Context)    { h.reply(c, true) }
func (h *TicketHandler) Update(c *gin.Context)        { h.update(c, false) }
func (h *TicketHandler) AdminUpdate(c *gin.Context)   { h.update(c, true) }
func (h *TicketHandler) MarkRead(c *gin.Context)      { h.markRead(c, false) }
func (h *TicketHandler) AdminMarkRead(c *gin.Context) { h.markRead(c, true) }

func (h *TicketHandler) list(c *gin.Context, admin bool) {
	actor, ok := ticketActor(c, admin)
	if !ok {
		return
	}
	page, size := response.ParsePagination(c)
	for _, parameter := range []struct {
		name   string
		target *int
	}{{"page", &page}, {"page_size", &size}} {
		if raw, exists := c.GetQuery(parameter.name); exists {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 {
				response.BadRequest(c, "invalid pagination")
				return
			}
			*parameter.target = value
		}
	}
	filter := service.TicketFilter{PaginationParams: pagination.PaginationParams{Page: page, PageSize: size},
		Search: c.Query("search"), Status: c.Query("status"), Priority: c.Query("priority"), Category: c.Query("category"), AssignedTo: c.Query("assigned_to")}
	items, total, err := h.tickets.List(c.Request.Context(), actor, filter)
	if response.ErrorFrom(c, err) {
		return
	}
	if !admin {
		for i := range items {
			items[i].UserEmail = ""
			items[i].UserName = ""
		}
	}
	response.Paginated(c, items, total, page, size)
}

func (h *TicketHandler) stats(c *gin.Context, admin bool) {
	actor, ok := ticketActor(c, admin)
	if !ok {
		return
	}
	stats, err := h.tickets.Stats(c.Request.Context(), actor)
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, stats)
}

func (h *TicketHandler) detail(c *gin.Context, admin bool) {
	actor, ok := ticketActor(c, admin)
	if !ok {
		return
	}
	id, ok := ticketID(c)
	if !ok {
		return
	}
	var before int64
	if raw, exists := c.GetQuery("before_id"); exists {
		var err error
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			response.BadRequest(c, "invalid before_id")
			return
		}
	}
	detail, err := h.tickets.Detail(c.Request.Context(), actor, id, before)
	ticketDetailResponse(c, detail, err, admin)
}

func (h *TicketHandler) Create(c *gin.Context) {
	actor, ok := ticketActor(c, false)
	if !ok {
		return
	}
	var in service.CreateTicketInput
	if !ticketBody(c, &in) {
		return
	}
	detail, err := h.tickets.Create(c.Request.Context(), actor, in)
	ticketDetailResponse(c, detail, err, false)
}

func (h *TicketHandler) reply(c *gin.Context, admin bool) {
	actor, ok := ticketActor(c, admin)
	if !ok {
		return
	}
	id, ok := ticketID(c)
	if !ok {
		return
	}
	var in service.ReplyTicketInput
	if !ticketBody(c, &in) {
		return
	}
	detail, err := h.tickets.Reply(c.Request.Context(), actor, id, in)
	ticketDetailResponse(c, detail, err, admin)
}

func (h *TicketHandler) update(c *gin.Context, admin bool) {
	actor, ok := ticketActor(c, admin)
	if !ok {
		return
	}
	id, ok := ticketID(c)
	if !ok {
		return
	}
	var in service.UpdateTicketInput
	if !ticketBody(c, &in) {
		return
	}
	detail, err := h.tickets.Update(c.Request.Context(), actor, id, in)
	ticketDetailResponse(c, detail, err, admin)
}

func (h *TicketHandler) markRead(c *gin.Context, admin bool) {
	actor, ok := ticketActor(c, admin)
	if !ok {
		return
	}
	id, ok := ticketID(c)
	if !ok {
		return
	}
	var in struct {
		LastMessageID int64 `json:"last_message_id"`
	}
	if !ticketBody(c, &in) {
		return
	}
	if response.ErrorFrom(c, h.tickets.MarkRead(c.Request.Context(), actor, id, in.LastMessageID)) {
		return
	}
	response.Success(c, gin.H{"message": "ok"})
}
