package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerTicketRoutes(authenticated *gin.RouterGroup, h *handler.TicketHandler) {
	tickets := authenticated.Group("/tickets")
	tickets.GET("", h.List)
	tickets.GET("/stats", h.Stats)
	tickets.POST("", h.Create)
	tickets.POST("/attachments", h.UploadAttachment)
	tickets.DELETE("/attachments/:attachmentId", h.DeletePendingAttachment)
	tickets.GET("/:id", h.Detail)
	tickets.GET("/:id/attachments/:attachmentId", h.GetAttachment)
	tickets.POST("/:id/read", h.MarkRead)
	tickets.POST("/:id/replies", h.Reply)
	tickets.PATCH("/:id", h.Update)
}

func registerAdminTicketRoutes(admin *gin.RouterGroup, h *handler.TicketHandler) {
	tickets := admin.Group("/tickets")
	tickets.GET("", h.AdminList)
	tickets.POST("", h.AdminCreate)
	tickets.POST("/attachments", h.AdminUploadAttachment)
	tickets.DELETE("/attachments/:attachmentId", h.AdminDeletePendingAttachment)
	tickets.GET("/stats", h.AdminStats)
	tickets.GET("/:id", h.AdminDetail)
	tickets.GET("/:id/attachments/:attachmentId", h.AdminGetAttachment)
	tickets.POST("/:id/read", h.AdminMarkRead)
	tickets.POST("/:id/replies", h.AdminReply)
	tickets.PATCH("/:id", h.AdminUpdate)
}
