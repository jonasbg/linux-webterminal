package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jonasbg/linux-terminal/m/v2/socket"
	"github.com/jonasbg/linux-terminal/m/v2/ttycontroller"
)

func main() {
	// Initialize TTYController
	ttyCtrl, err := ttycontroller.NewTTYController()
	if err != nil {
		log.Fatalf("Failed to initialize TTY controller: %v", err)
	}

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Create a Gin router in release mode for production
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()

	// Add the built-in recovery middleware to handle panics
	router.Use(gin.Recovery())

	// Add a custom logger middleware that's less verbose than gin.Logger()
	router.Use(func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path

		// Process request
		c.Next()

		// Log only after completion and only if not WebSocket (to reduce noise)
		if path != "/ws" {
			latency := time.Since(start)
			log.Printf("%s %s %d %s", c.Request.Method, path, c.Writer.Status(), latency)
		}
	})

	// Serve static files from the static directory
	router.Static("/web/static", "./static")

	// Handle root path - serve the terminal template
	router.GET("/", func(c *gin.Context) {
		if c.Request.URL.Path != "/" {
			c.Status(http.StatusNotFound)
			return
		}
		c.File(filepath.Join("web/templates", "terminal.html"))
	})

	// WebSocket endpoint
	router.GET("/ws", func(c *gin.Context) {
		socket.HandleWebSocket(c.Writer, c.Request, ttyCtrl)
	})

	// Health check endpoint
	router.GET("/healthz", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	// Create a server with proper timeouts
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Run the server in a goroutine
	go func() {
		log.Printf("Server starting on port %s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-sigChan
	log.Println("Shutting down...")

	// Create a deadline for shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown server gracefully
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}

	// Clean up sessions with timeout
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanupCancel()

	done := make(chan struct{})
	go func() {
		ttyCtrl.CleanupAllSessions()
		close(done)
	}()

	select {
	case <-done:
		log.Println("Cleanup completed")
	case <-cleanupCtx.Done():
		log.Println("Cleanup timed out")
	}

	log.Println("Server exited")
}
