package admin

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// DebugDumpHandler serves the opt-in upstream failure dumps as downloadable
// text so a failing request can be diagnosed without shell access.
type DebugDumpHandler struct{}

func NewDebugDumpHandler() *DebugDumpHandler { return &DebugDumpHandler{} }

// List returns the dump file names, newest first.
// GET /api/v1/admin/debug-dumps
func (h *DebugDumpHandler) List(c *gin.Context) {
	names, err := service.ListDebugDumps()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"code":    0,
			"message": "ok",
			"data":    gin.H{"enabled": false, "files": []string{}, "hint": err.Error()},
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": gin.H{"enabled": true, "files": names}})
}

// Download streams one dump as a .txt attachment.
// GET /api/v1/admin/debug-dumps/:name
func (h *DebugDumpHandler) Download(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))
	body, err := service.ReadDebugDump(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": http.StatusNotFound, "message": err.Error(), "data": nil})
		return
	}
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, "text/plain; charset=utf-8", body)
}
