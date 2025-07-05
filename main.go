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
    // Load environment variables
    if err := godotenv.Load(); err != nil {
        log.Println("Warning: .env file not found")
    }

    // Initialize database and Gemini
    config.InitMongoDB()
    config.InitGemini()

    // Setup router
    r := gin.Default()

    // Load templates and static files
    r.LoadHTMLGlob("templates/**/*")
    r.Static("/static", "./static")

    // CORS middleware (fixes your error)
    corsConfig := cors.Config{
        AllowOrigins: []string{
            "http://localhost:8080",
            "http://localhost:3000",
            "http://127.0.0.1:3000",
            "http://localhost:3001",
            "http://127.0.0.1:3001",
            "https://155b-150-107-16-191.ngrok-free.app",
                "http://localhost:3000",   // CRA dev server
        "http://localhost:8081",   // if you proxy
            
        },
        AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH", "HEAD"},
        AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With", "X-CSRF-Token", "Cache-Control"},
        ExposeHeaders:    []string{"Content-Length", "Content-Type"},
        AllowCredentials: true,
        MaxAge:           12 * time.Hour,
    }
    r.Use(cors.New(corsConfig))

    // Add iframe-specific headers (optional, if needed)
    r.Use(func(c *gin.Context) {
        c.Header("X-Frame-Options", "ALLOWALL")
        c.Header("Content-Security-Policy", "frame-ancestors *")
        c.Header("X-Content-Type-Options", "nosniff")
        c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
        c.Next()
    })

    setupRoutes(r)

    // Embed routes
    r.GET("/embed/:projectId", handlers.EmbedChat)
    r.POST("/embed/:projectId/auth", handlers.EmbedAuth)
    r.GET("/embed/:projectId/chat", handlers.IframeChatInterface)

    // Widget API
    r.GET("/widget.js", func(c *gin.Context) {
        c.File("./static/js/jevi-chat-widget.js")
    })
    r.GET("/widget.css", func(c *gin.Context) {
        c.File("./static/css/jevi-widget.css")
    })

    port := os.Getenv("PORT")
    if port == "" {
        port = "https://troikabackend.onrender.com"
    }

    log.Printf("🚀 Jevi Chat Server starting on port %s", port)
    log.Printf("✅ CORS configured for React frontend")
    log.Printf("🌐 Frontend URL: http://localhost:3000")
    log.Printf("🔗 Backend URL: http://localhost:%s", port)
    log.Printf("📊 Health check: http://localhost:%s/health", port)
    log.Printf("🤖 Embed URL: http://localhost:%s/embed/PROJECT_ID", port)
    log.Printf("📱 Widget Script: http://localhost:%s/widget.js", port)

    log.Fatal(http.ListenAndServe(":"+port, r))
}

func setupRoutes(r *gin.Engine) {
    // Health check
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

    // CORS test endpoint
    r.GET("/cors-test", func(c *gin.Context) {
        c.JSON(http.StatusOK, gin.H{
            "message": "CORS is working!",
            "origin":  c.Request.Header.Get("Origin"),
            "method":  c.Request.Method,
            "iframe":  "supported",
        })
    })

    // Public routes
    r.GET("/", handlers.Home)
    r.GET("/login", handlers.LoginPage)
    r.POST("/login", handlers.Login)
    r.GET("/logout", handlers.Logout)
    r.GET("/register", handlers.RegisterPage)
    r.POST("/register", handlers.Register)

    // API routes for React frontend
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

    // Admin routes
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

        // Gemini Management
        admin.PATCH("/projects/:id/gemini/toggle", handlers.ToggleGeminiStatus)
        admin.PATCH("/projects/:id/gemini/limit", handlers.SetGeminiLimit)
        admin.POST("/projects/:id/gemini/reset", handlers.ResetGeminiUsage)
        admin.GET("/projects/:id/gemini/analytics", handlers.GetGeminiAnalytics)
        
        // PDF Management
        admin.POST("/projects/:id/upload-pdf", handlers.UploadPDF)
        admin.DELETE("/projects/:id/pdf/:fileId", handlers.DeletePDF)
    }

    // User routes - FIXED VERSION
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
        user.POST("/chat/:id/message", handlers.SendMessage)    // Use SendMessage for authenticated users
        user.POST("/project/:id/upload", handlers.UploadPDF)
        user.GET("/chat/:id/history", handlers.GetChatHistory)
        // REMOVED: duplicate user.POST("/chat/:id/message", handlers.SendMessage)
    }

    // Public chat routes (for embed widgets)
    chat := r.Group("/chat")
    {
        chat.POST("/:projectId/message", handlers.IframeSendMessage)  // Use IframeSendMessage for public/embed
        chat.GET("/:projectId/history", handlers.GetChatHistory)
    }

    // Error handlers
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
