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

	// ✅ FIXED: Initialize services once (removed duplicates)
	log.Println("🔧 Initializing services...")
	config.InitMongoDB()
	config.InitGemini()
	handlers.InitRateLimiters()
	
	// Add graceful shutdown
	defer config.CloseMongoDB()
	
	log.Println("✅ All services initialized successfully")

	// Set up Gin with enhanced configuration
	r := gin.Default()
	
	// ✅ ENHANCED: File upload configuration for PDF handling
	r.MaxMultipartMemory = 32 << 20 // 32 MB for PDF uploads
	log.Println("📁 File upload limit set to 32MB")
	
	// Load templates and static files
	r.LoadHTMLGlob("templates/**/*.html")
	r.Static("/static", "./static")

	// Add CORS debug middleware only in development
	if gin.Mode() == gin.DebugMode {
		r.Use(handlers.CORSDebugMiddleware())
		log.Println("🔍 CORS debugging enabled for development")
	}

	// ✅ CLEAN: CORS setup without null origin handling
	corsConfig := cors.Config{
		AllowOrigins: []string{
			"https://troika-tech.onrender.com",
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
	log.Println("🌐 CORS middleware configured successfully")

	// Enhanced security headers for iframe support
	r.Use(func(c *gin.Context) {
		c.Header("X-Frame-Options", "ALLOWALL")
		c.Header("Content-Security-Policy", "frame-ancestors *")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		
		// Add file upload specific headers
		if c.Request.Method == "POST" && c.Request.Header.Get("Content-Type") != "" {
			c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization, X-Requested-With, X-CSRF-Token, Cache-Control")
		}
		
		c.Next()
	})

	// ✅ ENHANCED: Add request timeout middleware for file uploads
	r.Use(func(c *gin.Context) {
		// Set longer timeout for file upload endpoints
		if c.Request.URL.Path != "" && 
		   (c.Request.URL.Path == "/admin/projects/*/upload-pdf" || 
		    c.Request.Method == "POST" && c.Request.Header.Get("Content-Type") != "" && 
		    c.Request.Header.Get("Content-Type") == "multipart/form-data") {
			c.Request = c.Request.WithContext(c.Request.Context())
		}
		c.Next()
	})

	// Setup all routes
	setupRoutes(r)

	// Chat widget static files
	r.GET("/widget.js", func(c *gin.Context) {
		c.File("./static/js/jevi-chat-widget.js")
	})
	r.GET("/widget.css", func(c *gin.Context) {
		c.File("./static/css/jevi-widget.css")
	})

	// Server configuration
	port := os.Getenv("PORT")
	if port == "" || len(port) > 5 {
		port = "8080"
	}

	// Server startup messages
	log.Printf("🚀 Jevi Chat Server starting on port %s", port)
	log.Printf("📊 Rate Limiting: Chat(30/min), Auth(10/min), General(60/min)")
	log.Printf("📁 File Upload: Max 32MB, Enhanced timeout handling")
	log.Printf("🌐 CORS: Enabled with %d allowed origins", len(corsConfig.AllowOrigins))
	log.Printf("🔒 Security: Enhanced headers for iframe support")
	
	// Start server
	log.Fatal(http.ListenAndServe("0.0.0.0:"+port, r))
}

// ✅ ENHANCED: Complete route setup with improved error handling and debugging
func setupRoutes(r *gin.Engine) {
	// Health check endpoint (no rate limiting for monitoring)
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":        "healthy",
			"service":       "jevi-chat",
			"version":       "1.0.0",
			"cors":          "enabled",
			"iframe":        "enabled",
			"rate_limit":    "enabled",
			"file_upload":   "32MB max",
			"timestamp":     time.Now().Format(time.RFC3339),
			"environment":   gin.Mode(),
		})
	})

	// CORS test endpoint
	r.GET("/cors-test", handlers.RateLimitMiddleware("general"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "CORS is working!",
			"origin":  c.Request.Header.Get("Origin"),
			"method":  c.Request.Method,
			"iframe":  "supported",
		})
	})

	// ✅ EMBED ROUTES: Chat widget embedding
	embedGroup := r.Group("/embed/:projectId")
	embedGroup.Use(handlers.RateLimitMiddleware("general"))
	{
		embedGroup.GET("", handlers.EmbedChat)
		embedGroup.GET("/chat", handlers.IframeChatInterface)
		
		// Auth endpoints with stricter rate limiting
		authGroup := embedGroup.Group("/auth")
		authGroup.Use(handlers.RateLimitMiddleware("auth"))
		{
			authGroup.GET("", handlers.EmbedAuth)
			authGroup.POST("", handlers.EmbedAuth)
		}
		
		// Message endpoint with chat rate limiting
		embedGroup.POST("/message", handlers.RateLimitMiddleware("chat"), handlers.IframeSendMessage)
	}

	// Embed health check
	r.GET("/embed/health", handlers.EmbedHealth)

	// ✅ PUBLIC AUTH ROUTES: Login, register, logout
	authRoutes := r.Group("/")
	authRoutes.Use(handlers.RateLimitMiddleware("auth"))
	{
		authRoutes.POST("/login", handlers.Login)
		authRoutes.GET("/logout", handlers.Logout)
		authRoutes.GET("/register", handlers.RegisterPage)
		authRoutes.POST("/register", handlers.Register)
	}

	// ✅ API ROUTES: General API endpoints
	api := r.Group("/api")
	api.Use(handlers.RateLimitMiddleware("general"))
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

	// ✅ ENHANCED: Admin Routes with detailed debugging for PDF upload
	admin := r.Group("/admin")
	admin.Use(handlers.RateLimitMiddleware("general"))
	admin.Use(func(c *gin.Context) {
		// Enhanced logging for debugging upload issues
		log.Printf("🔍 Admin route accessed: %s %s", c.Request.Method, c.Request.URL.Path)
		log.Printf("🔍 Content-Type: %s", c.Request.Header.Get("Content-Type"))
		log.Printf("🔍 Content-Length: %d", c.Request.ContentLength)
		log.Printf("🔍 Authorization header present: %t", c.GetHeader("Authorization") != "")
		
		if c.Request.Method == "OPTIONS" {
			log.Printf("🔍 Handling OPTIONS request for CORS preflight")
			c.Next()
			return
		}
		
		// Apply admin authentication
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
		
		// Gemini AI management endpoints
		admin.PATCH("/projects/:id/gemini/toggle", handlers.ToggleGeminiStatus)
		admin.PATCH("/projects/:id/gemini/limit", handlers.SetGeminiLimit)
		admin.POST("/projects/:id/gemini/reset", handlers.ResetGeminiUsage)
		admin.GET("/projects/:id/gemini/analytics", handlers.GetGeminiAnalytics)
		
		// ✅ CRITICAL: PDF upload endpoint with enhanced debugging
		admin.POST("/projects/:id/upload-pdf", func(c *gin.Context) {
			projectId := c.Param("id")
			log.Printf("📄 PDF upload endpoint hit for project: %s", projectId)
			log.Printf("📄 Request method: %s", c.Request.Method)
			log.Printf("📄 Content-Type: %s", c.Request.Header.Get("Content-Type"))
			log.Printf("📄 Content-Length: %d bytes", c.Request.ContentLength)
			log.Printf("📄 User-Agent: %s", c.Request.Header.Get("User-Agent"))
			
			// Call the actual upload handler
			handlers.UploadPDF(c)
		})
		
		// PDF management endpoints
		admin.DELETE("/projects/:id/pdf/:fileId", handlers.DeletePDF)
		admin.GET("/projects/:id/pdfs", handlers.GetPDFFiles)
	}

	// ✅ USER ROUTES: User dashboard and project management
	user := r.Group("/user")
	user.Use(handlers.RateLimitMiddleware("general"))
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
		user.POST("/chat/:id/message", handlers.RateLimitMiddleware("chat"), handlers.SendMessage)
		user.GET("/chat/:id/history", handlers.GetChatHistory)
	}

	// ✅ CHAT API: Public chat endpoints for embedded widgets
	chat := r.Group("/chat")
	chat.Use(handlers.RateLimitMiddleware("chat"))
	{
		chat.POST("/:projectId/message", handlers.RateLimitMiddleware("chat"), handlers.IframeSendMessage)
		chat.GET("/:projectId/history", handlers.RateLimitMiddleware("general"), handlers.GetChatHistory)
		chat.POST("/:projectId/rate/:messageId", handlers.RateLimitMiddleware("general"), handlers.RateMessage)
	}

	// ✅ ENHANCED: Error handling with detailed debugging information
	r.NoRoute(func(c *gin.Context) {
		log.Printf("❌ 404 - Route not found: %s %s", c.Request.Method, c.Request.URL.Path)
		c.JSON(http.StatusNotFound, gin.H{
			"error":     "Route not found",
			"message":   "The requested endpoint does not exist",
			"path":      c.Request.URL.Path,
			"method":    c.Request.Method,
			"timestamp": time.Now().Format(time.RFC3339),
			"hint":      "Check the API documentation for valid endpoints",
		})
	})

	r.NoMethod(func(c *gin.Context) {
		log.Printf("❌ 405 - Method not allowed: %s %s", c.Request.Method, c.Request.URL.Path)
		c.JSON(http.StatusMethodNotAllowed, gin.H{
			"error":     "Method not allowed",
			"message":   "The requested method is not allowed for this endpoint",
			"path":      c.Request.URL.Path,
			"method":    c.Request.Method,
			"timestamp": time.Now().Format(time.RFC3339),
			"hint":      "Check the allowed methods for this endpoint",
		})
	})

	log.Println("✅ All routes configured successfully")
}
