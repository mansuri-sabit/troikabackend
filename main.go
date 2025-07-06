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


	  // Add graceful shutdown
    defer config.CloseMongoDB()


	    // Your existing initialization code...
    config.InitGemini()
    handlers.InitRateLimiters()



	// ✅ NEW: Initialize rate limiters
	handlers.InitRateLimiters()
	log.Println("✅ Rate limiters initialized")




	// Set up Gin
	r := gin.Default()
	r.LoadHTMLGlob("templates/**/*.html")
	r.Static("/static", "./static")

	    // Add CORS debug middleware only in development
    if gin.Mode() == gin.DebugMode {
        r.Use(handlers.CORSDebugMiddleware())
        log.Println("🔍 CORS debugging enabled")
    }
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
		ExposeHeaders:    []string{"Content-Length", "Content-Type", "X-RateLimit-Remaining", "X-RateLimit-Reset", "Retry-After"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}
	r.Use(cors.New(corsConfig))

	// Add conditional null origin for development
if gin.Mode() == gin.DebugMode {
    corsConfig.AllowOrigins = append(corsConfig.AllowOrigins, "null")
    log.Println("🔍 CORS: Allowing 'null' origin for development")
}

	// Iframe & security headers
	r.Use(func(c *gin.Context) {
		c.Header("X-Frame-Options", "ALLOWALL")
		c.Header("Content-Security-Policy", "frame-ancestors *")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	})

	// Setup Routes with rate limiting
	setupRoutes(r)

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
	log.Printf("📊 Rate Limiting: Chat(30/min), Auth(10/min), General(60/min)")
	log.Fatal(http.ListenAndServe("0.0.0.0:"+port, r))
}

func setupRoutes(r *gin.Engine) {
	// Health check (no rate limiting for monitoring)
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":      "healthy",
			"service":     "jevi-chat",
			"version":     "1.0.0",
			"cors":        "enabled",
			"iframe":      "enabled",
			"rate_limit":  "enabled",
			"timestamp":   time.Now().Format(time.RFC3339),
		})
	})

	// CORS test endpoint (light rate limiting)
	r.GET("/cors-test", handlers.RateLimitMiddleware("general"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "CORS is working!",
			"origin":  c.Request.Header.Get("Origin"),
			"method":  c.Request.Method,
			"iframe":  "supported",
		})
	})

	// ✅ UPDATED: Embed endpoints with proper rate limiting
	embedGroup := r.Group("/embed/:projectId")
	embedGroup.Use(handlers.RateLimitMiddleware("general")) // 60 req/min for embed pages
	{
		embedGroup.GET("", handlers.EmbedChat)                    // Main embed page
		embedGroup.GET("/chat", handlers.IframeChatInterface)     // Chat interface
		
		// Auth endpoints with stricter rate limiting
		authGroup := embedGroup.Group("/auth")
		authGroup.Use(handlers.RateLimitMiddleware("auth")) // 10 req/min for auth
		{
			authGroup.GET("", handlers.EmbedAuth)   // Show auth page
			authGroup.POST("", handlers.EmbedAuth)  // Handle auth submission
		}
		
		// Message endpoint with chat rate limiting
		embedGroup.POST("/message", handlers.RateLimitMiddleware("chat"), handlers.IframeSendMessage) // 30 req/min
	}

	// ✅ NEW: Embed health check
	r.GET("/embed/health", handlers.EmbedHealth)

	// ✅ UPDATED: Public Auth Routes with rate limiting
	authRoutes := r.Group("/")
	authRoutes.Use(handlers.RateLimitMiddleware("auth")) // 10 req/min for auth
	{
		authRoutes.POST("/login", handlers.Login)
		authRoutes.GET("/logout", handlers.Logout)
		authRoutes.GET("/register", handlers.RegisterPage)
		authRoutes.POST("/register", handlers.Register)
	}

	// ✅ UPDATED: API Routes with rate limiting
	api := r.Group("/api")
	api.Use(handlers.RateLimitMiddleware("general")) // 60 req/min for API
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

	// ✅ UPDATED: Admin Routes with moderate rate limiting
	admin := r.Group("/admin")
	admin.Use(handlers.RateLimitMiddleware("general")) // 60 req/min for admin
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

	// ✅ UPDATED: User Routes with rate limiting
	user := r.Group("/user")
	user.Use(handlers.RateLimitMiddleware("general")) // 60 req/min for user dashboard
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
		
		// Chat message endpoint with stricter rate limiting
		user.POST("/chat/:id/message", handlers.RateLimitMiddleware("chat"), handlers.SendMessage) // 30 req/min
		
		
		user.GET("/chat/:id/history", handlers.GetChatHistory)
	}

	// ✅ UPDATED: Chat API with proper rate limiting
	chat := r.Group("/chat")
	chat.Use(handlers.RateLimitMiddleware("chat")) // 30 req/min for chat
	{
		chat.POST("/:projectId/message", handlers.RateLimitMiddleware("chat"),handlers.IframeSendMessage)
		chat.GET("/:projectId/history", handlers.RateLimitMiddleware("general"),handlers.GetChatHistory)
		chat.POST("/:projectId/rate/:messageId", handlers.RateLimitMiddleware("general"),handlers.RateMessage) // Rate message endpoint
	}

	// ✅ ENHANCED: 404 and method errors with rate limiting info
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "Route not found",
			"message": "The requested endpoint does not exist",
			"path":    c.Request.URL.Path,
			"method":  c.Request.Method,
			"hint":    "Check the API documentation for valid endpoints",
		})
	})

	r.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"error":   "Method not allowed",
			"message": "The requested method is not allowed for this endpoint",
			"path":    c.Request.URL.Path,
			"method":  c.Request.Method,
			"hint":    "Check the allowed methods for this endpoint",
		})
	})
}
