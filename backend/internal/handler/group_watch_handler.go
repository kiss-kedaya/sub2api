package handler

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GroupWatchHandler exposes user group-watch subscription and in-app notification endpoints.
type GroupWatchHandler struct {
	service *service.GroupWatchService
}

func NewGroupWatchHandler(svc *service.GroupWatchService) *GroupWatchHandler {
	return &GroupWatchHandler{service: svc}
}

// ListSubscriptions GET /api/v1/group-watch/subscriptions
func (h *GroupWatchHandler) ListSubscriptions(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return
	}
	ids, err := h.service.ListSubscribedGroupIDs(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"group_ids": ids})
}

// Subscribe PUT /api/v1/group-watch/subscriptions/:group_id
func (h *GroupWatchHandler) Subscribe(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return
	}
	groupID, err := strconv.ParseInt(strings.TrimSpace(c.Param("group_id")), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "invalid group_id")
		return
	}
	isAdmin := middleware.IsPrimaryAdmin(c)
	if err := h.service.Subscribe(c.Request.Context(), subject.UserID, groupID, isAdmin); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"group_id": groupID, "subscribed": true})
}

// Unsubscribe DELETE /api/v1/group-watch/subscriptions/:group_id
func (h *GroupWatchHandler) Unsubscribe(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return
	}
	groupID, err := strconv.ParseInt(strings.TrimSpace(c.Param("group_id")), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "invalid group_id")
		return
	}
	if err := h.service.Unsubscribe(c.Request.Context(), subject.UserID, groupID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"group_id": groupID, "subscribed": false})
}

// NotificationSummary GET /api/v1/group-watch/notifications/summary
func (h *GroupWatchHandler) NotificationSummary(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return
	}
	total, unread, err := h.service.SummaryNotifications(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"total": total, "unread": unread})
}

// ListNotifications GET /api/v1/group-watch/notifications
func (h *GroupWatchHandler) ListNotifications(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return
	}
	unreadOnly := false
	rawUnread := strings.TrimSpace(strings.ToLower(c.Query("unread_only")))
	if rawUnread == "1" || rawUnread == "true" {
		unreadOnly = true
	}
	limit := 30
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		if parsed, err := strconv.Atoi(rawLimit); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	items, err := h.service.ListNotifications(c.Request.Context(), subject.UserID, unreadOnly, limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

// MarkNotificationRead POST /api/v1/group-watch/notifications/:id/read
func (h *GroupWatchHandler) MarkNotificationRead(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid notification id")
		return
	}
	if err := h.service.MarkNotificationRead(c.Request.Context(), subject.UserID, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "ok"})
}

// MarkAllNotificationsRead POST /api/v1/group-watch/notifications/read-all
func (h *GroupWatchHandler) MarkAllNotificationsRead(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return
	}
	if err := h.service.MarkAllNotificationsRead(c.Request.Context(), subject.UserID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "ok"})
}
