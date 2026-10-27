// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
package trafficquery

import (
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func ParseKeys(c *gin.Context, userID int64, admin bool) (int64, string, bool) {
	query := strings.TrimSpace(c.Query("q"))
	if len(query) > 256 {
		response.BadRequest(c, "Search query too long")
		return 0, "", false
	}
	if admin && c.Query("user_id") != "" {
		id, err := strconv.ParseInt(c.Query("user_id"), 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid user_id")
			return 0, "", false
		}
		userID = id
	}
	return userID, query, true
}

func Parse(c *gin.Context, userID int64, admin bool) (service.TrafficFilter, bool) {
	f, err := service.NewTrafficFilter(c.DefaultQuery("period", "month"), c.Query("date"), time.Now())
	if err != nil {
		response.BadRequest(c, err.Error())
		return f, false
	}
	f.UserID = userID
	f.View = c.DefaultQuery("view", "all")
	if f.View != "all" && f.View != "summary" && f.View != "details" {
		response.BadRequest(c, "Invalid view")
		return f, false
	}
	if raw := c.Query("cursor"); raw != "" {
		f.Cursor, err = service.ParseTrafficCursor(raw)
		if err != nil || f.Cursor.StartedAt.Before(f.Start) || !f.Cursor.StartedAt.Before(f.End) || f.View == "summary" {
			response.BadRequest(c, "Invalid cursor")
			return f, false
		}
	}
	fields := []struct {
		name   string
		target *int64
	}{{"api_key_id", &f.APIKeyID}}
	if admin {
		fields = append(fields, struct {
			name   string
			target *int64
		}{"user_id", &f.UserID})
	}
	for _, field := range fields {
		if raw := c.Query(field.name); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n <= 0 {
				response.BadRequest(c, "Invalid "+field.name)
				return f, false
			}
			*field.target = n
		}
	}
	for _, field := range []struct {
		name   string
		target *int
		max    int
	}{{"page", &f.Page, 10000}, {"page_size", &f.PageSize, 100}} {
		if raw := c.Query(field.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > field.max {
				response.BadRequest(c, "Invalid "+field.name)
				return f, false
			}
			*field.target = n
		}
	}
	// Old clients may continue using page/offset. Cursor clients must not mix
	// the two mechanisms, otherwise their page could silently skip records.
	if f.Cursor != nil && f.Page != 1 {
		response.BadRequest(c, "cursor cannot be combined with page")
		return f, false
	}
	return f, true
}
