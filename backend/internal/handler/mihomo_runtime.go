package handler

import (
	"os"
	"path/filepath"

	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func ProvideMihomoHandler(checker *service.MihomoProxyService) *admin.MihomoHandler {
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		dir = "./data"
	}
	return admin.NewMihomoHandler(mihomo.NewManaged(filepath.Join(dir, "mihomo")), checker)
}
