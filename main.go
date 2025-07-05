package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"jevi-chat/config"
	"jevi-chat/handlers"
	"jevi-chat/middleware"
)

func main() {
	// Load .env variables
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env file not found")
	}

	// Initialize MongoDB and Gemini
	config.InitMongoDB()
	config.InitGemini()

	// Set up Gin
	r := gin.Default()
	r.LoadHTMLGlob("templates/**/*.html")
	r.Static("/static", "./static")

	// CORS setup
	corsConfig := cors.Config{
		AllowOrigins: []string{
			"https://troikafrontend.onrender.com",
			"http://localhost:3000",
			"http://127.0.0.1:3000",
			"http://localhost:3001",
			"http://127.0.0.1:3001",
			"http://localhost:8081",
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH", "HEAD"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With", "X-CSRF-Token", "Cache-Control"},
		ExposeHeaders:    []string{"Content-Length", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}
	r.Use(cors.New(corsConfig))

	// Iframe & security headers
	r.Use(func(c *gin.Context) {
		c.Header("X-Frame-Options", "ALLOWALL")
		c.Header("Content-Security-Policy", "frame-ancestors *")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	})

	// Setup Routes
	setupRoutes(r)

	// ✅ FIXED: Embed endpoints with proper route organization
	embedGroup := r.Group("/embed/:projectId")
	{
		embedGroup.GET("", handlers.EmbedChat)                    // Main embed page
		embedGroup.GET("/auth", handlers.EmbedAuth)               // ✅ NEW: GET auth page
		embedGroup.POST("/auth", handlers.EmbedAuth)              // ✅ EXISTING: POST auth submission
		embedGroup.GET("/chat", handlers.IframeChatInterface)     // Chat interface
		embedGroup.POST("/message", handlers.IframeSendMessage)   // Send message
	}

	// ✅ NEW: Embed health check
	r.GET("/embed/health", handlers.EmbedHealth)

	// Chat widget JS and CSS
	r.GET("/widget.js", func(c *gin.Context) {
		c.File("./static/js/jevi-chat-widget.js")
	})
	r.GET("/widget.css", func(c *gin.Context) {
		c.File("./static/css/jevi-widget.css")
	})

	// Server port
	port := os.Getenv("PORT")
	if port == "" || len(port) > 5 {
		port = "8080"
	}

	log.Printf("🚀 Jevi Chat Server running on port %s", port)
	log.Fatal(http.ListenAndServe("0.0.0.0:"+port, r))
}

func setupRoutes(r *gin.Engine) {
	// Health
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"service":   "jevi-chat",
			"version":   "1.0.0",
			"cors":      "enabled",
			"iframe":    "enabled",
			"timestamp": time.Now().Format(time.RFC3339),
		})
	})

	r.GET("/cors-test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "CORS is working!",
			"origin":  c.Request.Header.Get("Origin"),
			"method":  c.Request.Method,
			"iframe":  "supported",
		})
	})

	// Public Auth Routes
	r.POST("/login", handlers.Login)
	r.GET("/logout", handlers.Logout)
	r.GET("/register", handlers.RegisterPage)
	r.POST("/register", handlers.Register)

	// API Routes (optional)
	api := r.Group("/api")
	{
		api.POST("/login", handlers.Login)
		api.POST("/register", handlers.Register)
		api.POST("/logout", handlers.Logout)
		api.GET("/admin/dashboard", handlers.AdminDashboard)
		api.GET("/admin/projects", handlers.AdminProjects)
		api.POST("/admin/projects", handlers.CreateProject)
		api.GET("/admin/users", handlers.AdminUsers)
		api.DELETE("/admin/users/:id", handlers.DeleteUser)
		api.GET("/project/:id", handlers.ProjectDetails)
		api.PUT("/project/:id", handlers.UpdateProject)
		api.DELETE("/project/:id", handlers.DeleteProject)
		api.GET("/admin/notifications", handlers.GetNotifications)
		api.GET("/admin/realtime-stats", handlers.GetRealtimeStats)
	}

	// Admin Routes (protected)
	admin := r.Group("/admin")
	admin.Use(func(c *gin.Context) {
		if c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}
		middleware.AdminAuth()(c)
	})
	{
		admin.GET("/", handlers.AdminDashboard)
		admin.GET("/dashboard", handlers.AdminDashboard)
		admin.GET("/projects", handlers.AdminProjects)
		admin.POST("/projects", handlers.CreateProject)
		admin.GET("/projects/:id", handlers.ProjectDetails)
		admin.PUT("/projects/:id", handlers.UpdateProject)
		admin.DELETE("/projects/:id", handlers.DeleteProject)
		admin.GET("/users", handlers.AdminUsers)
		admin.DELETE("/users/:id", handlers.DeleteUser)
		admin.PATCH("/projects/:id/gemini/toggle", handlers.ToggleGeminiStatus)
		admin.PATCH("/projects/:id/gemini/limit", handlers.SetGeminiLimit)
		admin.POST("/projects/:id/gemini/reset", handlers.ResetGeminiUsage)
		admin.GET("/projects/:id/gemini/analytics", handlers.GetGeminiAnalytics)
		admin.POST("/projects/:id/upload-pdf", handlers.UploadPDF)
		admin.DELETE("/projects/:id/pdf/:fileId", handlers.DeletePDF)
	}

	// User Routes (protected)
	user := r.Group("/user")
	user.Use(func(c *gin.Context) {
		if c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}
		middleware.UserAuth()(c)
	})
	{
		user.GET("/dashboard", handlers.UserDashboard)
		user.GET("/project/:id", handlers.ProjectDashboard)
		user.GET("/chat/:id", handlers.IframeChatInterface)
		user.POST("/chat/:id/message", handlers.SendMessage)
		user.POST("/project/:id/upload", handlers.UploadPDF)
		user.GET("/chat/:id/history", handlers.GetChatHistory)
	}

	// Chat API for Iframe or Embedded Usage (public)
	chat := r.Group("/chat")
	{
		chat.POST("/:projectId/message", handlers.IframeSendMessage)
		chat.GET("/:projectId/history", handlers.GetChatHistory)
	}

	// 404 and method errors
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Route not found",
			"message": "The requested endpoint does not exist",
			"path":    c.Request.URL.Path,
			"method":  c.Request.Method,
		})
	})

	r.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"error":   "Method not allowed",
			"message": "The requested method is not allowed for this endpoint",
			"path":    c.Request.URL.Path,
			"method":  c.Request.Method,
		})
	})
}
