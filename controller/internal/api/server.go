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

	s.router.GET("/api/health", healthHandler.Health)
	s.router.GET("/api/ready", healthHandler.Ready)

	testHandler := handlers.NewTestHandler()
	s.router.GET("/api/test/domain", testHandler.TestDomainModels)
	s.router.GET("/api/test/ports", testHandler.TestPortAllocator)
	s.router.GET("/api/test/ports/allocation", testHandler.TestPortAllocation)
	s.router.GET("/api/test/ports/release", testHandler.TestPortRelease)

	s.router.GET("/api/test/webrtc", testHandler.TestWebRTCAllocation)
	s.router.GET("/api/test/tree", testHandler.TestFullTreeAllocation)
	s.router.GET("/api/test/release-range", testHandler.TestReleaseWebRTCRange)

	// Provisioner tests
	provHandler, err := handlers.NewProvisionerHandler(s.config.DockerNetwork, s.redisClient)
	if err != nil {
		panic("Failed to create provisioner handler: " + err.Error())
	}
	s.router.POST("/api/test/provision/injection", provHandler.TestCreateInjection)
	s.router.POST("/api/test/provision/relay", provHandler.TestCreateRelay)
	s.router.POST("/api/test/provision/egress", provHandler.TestCreateEgress)
	s.router.POST("/api/test/provision/tree", provHandler.TestCreateTree)
	s.router.DELETE("/api/test/provision/tree", provHandler.TestDestroyTree)
	s.router.GET("/api/test/provision/list", provHandler.TestListProvisioned)
	s.router.GET("/api/test/provision/:nodeId", provHandler.TestGetProvisionInfo)

	// Tree handler
	treeHandler := handlers.NewTreeHandler(s.redisClient, provHandler.GetProvisioner())

	s.router.POST("/api/trees", treeHandler.CreateTree)
	s.router.GET("/api/trees/:treeId", treeHandler.GetTree)
	s.router.DELETE("/api/trees/:treeId", treeHandler.DestroyTree)
	s.router.GET("/api/trees", treeHandler.ListTrees)

	//  Session handler
	sessionHandler := handlers.NewSessionHandler(s.redisClient, treeHandler.GetTreeManager())

	// Create session (ingresso)
	s.router.POST("/api/sessions", sessionHandler.CreateSession)
	// List session
	s.router.GET("/api/sessions", sessionHandler.ListSessions)
	// Watch session (create session uscita)
	s.router.GET("/api/sessions/:sessionId/view", sessionHandler.ViewSession)
	// GET Sessioni
	s.router.GET("/api/trees/:treeId/sessions", sessionHandler.ListSessions)
	// GET Sessione
	s.router.GET("/api/trees/:treeId/sessions/:sessionId", sessionHandler.GetSession)
	// DELETE Rimuove intera sessione
	s.router.DELETE("/api/trees/:treeId/sessions/:sessionId", sessionHandler.DestroySession)
	// DELETE Rimuove sessione su un egress (debug)
	s.router.DELETE("/api/trees/:treeId/sessions/:sessionId/egress/:egressId",
		sessionHandler.DestroySessionPath)
	//File statici
	s.router.GET("/", func(c *gin.Context) {
		c.File("web/sessions.html")
	})
	s.router.GET("/sessions.html", func(c *gin.Context) {
		c.File("web/sessions.html")
	})

	s.router.Static("/web", "./web")
	// Root Endpoint
	s.router.GET("/api", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"service": "media-tree-controller",
			"version": "0.1.0",
			"status":  "running",
			"endpoints": gin.H{
				"health":   "/api/health",
				"trees":    "/api/trees",
				"sessions": "/api/sessions",
				"ui":       "/sessions.html",
			},
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

	log.Printf(" Controller starting on %s", addr)
	log.Printf(" UI:  http://localhost:%d", s.config.ServerPort)
	log.Printf(" API: http://localhost:%d/api", s.config.ServerPort)

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

// Logger middleware
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
