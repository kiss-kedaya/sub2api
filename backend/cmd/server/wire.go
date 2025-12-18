//go:build wireinject
// +build wireinject

package main

import (
	"sub2api/internal/config"
	"sub2api/internal/handler"
	"sub2api/internal/repository"
	"sub2api/internal/service"

	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/wire"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Application struct {
	Server  *http.Server
	Cleanup func()
}

func initializeApplication(buildInfo handler.BuildInfo) (*Application, error) {
	wire.Build(
		// Config provider
		provideConfig,

		// Database provider
		provideDB,

		// Redis provider
		provideRedis,

		// Repository provider
		provideRepositories,

		// Service provider
		provideServices,

		// Handler provider
		provideHandlers,

		// Router provider
		provideRouter,

		// HTTP Server provider
		provideHTTPServer,

		// Cleanup provider
		provideCleanup,

		// Application provider
		wire.Struct(new(Application), "Server", "Cleanup"),
	)
	return nil, nil
}

func provideConfig() (*config.Config, error) {
	return config.Load()
}

func provideDB(cfg *config.Config) (*gorm.DB, error) {
	return initDB(cfg)
}

func provideRedis(cfg *config.Config) *redis.Client {
	return initRedis(cfg)
}

func provideRepositories(db *gorm.DB) *repository.Repositories {
	return repository.NewRepositories(db)
}

func provideServices(repos *repository.Repositories, rdb *redis.Client, cfg *config.Config) *service.Services {
	return service.NewServices(repos, rdb, cfg)
}

func provideHandlers(services *service.Services, repos *repository.Repositories, rdb *redis.Client, buildInfo handler.BuildInfo) *handler.Handlers {
	return handler.NewHandlers(services, repos, rdb, buildInfo)
}

func provideRouter(cfg *config.Config, handlers *handler.Handlers, services *service.Services, repos *repository.Repositories) *gin.Engine {
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())

	return setupRouter(r, cfg, handlers, services, repos)
}

func provideHTTPServer(cfg *config.Config, router *gin.Engine) *http.Server {
	return createHTTPServer(cfg, router)
}

func provideCleanup() func() {
	return func() {
		//	@todo
	}
}
