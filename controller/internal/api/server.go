package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"controller/internal/api/handlers"
	"controller/internal/config"
	"controller/internal/redis"
)

type Server struct {
	router      *gin.Engine
	httpServer  *http.Server
	redisClient *redis.Client
	config      *config.Config
}

func NewServer(cfg *config.Config, redisClient *redis.Client) *Server {
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()

	// Middleware
	router.Use(gin.Recovery())
	router.Use(loggerMiddleware())

	server := &Server{
		router:      router,
		redisClient: redisClient,
		config:      cfg,
	}

	// Setup routes
	server.setupRoutes()

	return server
}

func (s *Server) setupRoutes() {
	// Health check endpoints
	healthHandler := handlers.NewHealthHandler(s.redisClient)

	s.router.GET("/health", healthHandler.Health)
	s.router.GET("/ready", healthHandler.Ready)
	s.router.GET("/test/redis", healthHandler.TestRedis)
	s.router.GET("/test/topology", healthHandler.TestTopology)

	testHandler := handlers.NewTestHandler()
	s.router.GET("/test/domain", testHandler.TestDomainModels)
	s.router.GET("/test/ports", testHandler.TestPortAllocator)
	s.router.GET("/test/ports/allocation", testHandler.TestPortAllocation)
	s.router.GET("/test/ports/release", testHandler.TestPortRelease)

	s.router.GET("/test/webrtc", testHandler.TestWebRTCAllocation)
	s.router.GET("/test/tree", testHandler.TestFullTreeAllocation)
	s.router.GET("/test/release-range", testHandler.TestReleaseWebRTCRange)

	// Provisioner tests
	provHandler, err := handlers.NewProvisionerHandler(s.config.DockerNetwork, s.redisClient)
	if err != nil {
		panic("Failed to create provisioner handler: " + err.Error())
	}
	s.router.POST("/test/provision/injection", provHandler.TestCreateInjection)
	s.router.POST("/test/provision/relay", provHandler.TestCreateRelay)
	s.router.POST("/test/provision/egress", provHandler.TestCreateEgress)
	s.router.POST("/test/provision/tree", provHandler.TestCreateTree)
	s.router.DELETE("/test/provision/tree", provHandler.TestDestroyTree)
	s.router.GET("/test/provision/list", provHandler.TestListProvisioned)
	s.router.GET("/test/provision/:nodeId", provHandler.TestGetProvisionInfo)

	// Tree handler
	treeHandler := handlers.NewTreeHandler(s.redisClient, provHandler.GetProvisioner())

	s.router.POST("/trees", treeHandler.CreateTree)
	s.router.GET("/trees/:id", treeHandler.GetTree)
	s.router.DELETE("/trees/:id", treeHandler.DestroyTree)
	s.router.GET("/trees", treeHandler.ListTrees)
	// Root endpoint
	s.router.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"service": "media-tree-controller",
			"version": "0.1.0",
			"status":  "running",
		})
	})
}

func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.config.ServerPort)

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("API server listening on %s", addr)

	// ListenAndServe blocca fino a shutdown
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start server: %w", err)
	}

	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Shutting down API server...")

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("server shutdown failed: %w", err)
	}

	log.Println("API server stopped gracefully")
	return nil
}

// Logger middleware semplice
func loggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		c.Next()

		duration := time.Since(start)
		statusCode := c.Writer.Status()

		log.Printf("[API] %s %s - %d (%v)", method, path, statusCode, duration)
	}
}
