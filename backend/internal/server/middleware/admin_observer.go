package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// PrimaryAdminUserID is the sole operator authorized to change administration
// state or read account/customer secrets. Other administrators are observers.
const PrimaryAdminUserID int64 = 1

func IsPrimaryAdmin(c *gin.Context) bool {
	subject, ok := GetAuthSubjectFromContext(c)
	role, _ := GetUserRoleFromContext(c)
	return ok && subject.UserID == PrimaryAdminUserID && role == "admin"
}

func IsAdminObserver(c *gin.Context) bool {
	subject, ok := GetAuthSubjectFromContext(c)
	return ok && subject.UserID > 0 && subject.UserID != PrimaryAdminUserID
}

// An exact allowlist is intentional: GET endpoints can export secrets, return
// signed capabilities, or perform work. New endpoints are private by default.
func enforceAdminObserverAccess(c *gin.Context) bool {
	subject, ok := GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
		return false
	}
	if subject.UserID == PrimaryAdminUserID {
		return true
	}
	if c.Request.Method == http.MethodGet && !isWebSocketUpgradeRequest(c) {
		switch c.FullPath() {
		case "/api/v1/admin/usage", "/api/v1/admin/usage/stats",
			"/api/v1/admin/dashboard/stats", "/api/v1/admin/dashboard/realtime",
			"/api/v1/admin/dashboard/trend", "/api/v1/admin/dashboard/models",
			"/api/v1/admin/dashboard/groups", "/api/v1/admin/dashboard/snapshot-v2":
			c.Header("Cache-Control", "no-store")
			c.Header("Pragma", "no-cache")
			return true
		}
	}
	AbortWithError(c, http.StatusForbidden, "ADMIN_OBSERVER_READ_ONLY", "Observer administrators may only view anonymized usage statistics")
	return false
}
