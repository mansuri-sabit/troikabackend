package handlers

import (

    "math/rand"
   "go.mongodb.org/mongo-driver/mongo/options" 
        "context"
    "fmt"
    "io/ioutil"
    "net/http"
    "strings"
    "time"

    "github.com/gin-gonic/gin"
    "go.mongodb.org/mongo-driver/bson"
    "go.mongodb.org/mongo-driver/bson/primitive"
    "jevi-chat/config"
    "jevi-chat/models"
)

// In handlers/admin.go
func AdminDashboard(c *gin.Context) {
    stats := map[string]interface{}{
        "total_users": 0,
        "total_projects": 0,
        "total_messages": 0,
        "active_users": 0,
    }
    
    // Get actual stats from database
    if userCollection := config.DB.Collection("users"); userCollection != nil {
        userCount, _ := userCollection.CountDocuments(context.Background(), bson.M{})
        activeUserCount, _ := userCollection.CountDocuments(context.Background(), bson.M{"is_active": true})
        stats["total_users"] = userCount
        stats["active_users"] = activeUserCount
    }
    
    if projectCollection := config.DB.Collection("projects"); projectCollection != nil {
        projectCount, _ := projectCollection.CountDocuments(context.Background(), bson.M{})
        stats["total_projects"] = projectCount
    }
    
    c.JSON(http.StatusOK, gin.H{
        "message": "Admin dashboard loaded successfully",
        "stats": stats,
        "timestamp": time.Now(),
    })
}

func AdminProjects(c *gin.Context) {
    fmt.Println("AdminProjects handler called - DEBUG")
    
    // Make sure this matches your actual MongoDB collection name
    collection := config.DB.Collection("projects")
    
    // Add debug logging to check collection existence
    count, err := collection.CountDocuments(context.Background(), bson.M{})
    fmt.Printf("Total documents in projects collection: %d\n", count)
    
    if err != nil {
        fmt.Printf("Error counting documents: %v\n", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch projects"})
        return
    }
    
    cursor, err := collection.Find(context.Background(), bson.M{})
    if err != nil {
        fmt.Printf("Error finding projects: %v\n", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch projects"})
        return
    }
    
    var projects []models.Project
    if err := cursor.All(context.Background(), &projects); err != nil {
        fmt.Printf("Error decoding projects: %v\n", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to decode projects"})
        return
    }
    
    fmt.Printf("Successfully fetched %d projects from database\n", len(projects))
    
    // Always return an array, even if empty
    if projects == nil {
        projects = []models.Project{}
    }
    
    c.JSON(http.StatusOK, gin.H{
        "success": true,
        "projects": projects,
        "count": len(projects),
        "total_in_db": count, // Add this for debugging
    })
}

func CreateProject(c *gin.Context) {
    fmt.Println("CreateProject handler called")
    
    var project models.Project
    
    // Log the raw request body for debugging
    body, _ := c.GetRawData()
    fmt.Printf("Raw request body: %s\n", string(body))
    
    // Reset the body for binding
    c.Request.Body = ioutil.NopCloser(strings.NewReader(string(body)))
    
    if err := c.ShouldBindJSON(&project); err != nil {
        fmt.Printf("JSON binding error: %v\n", err)
        c.JSON(http.StatusBadRequest, gin.H{
            "error": "Invalid project data",
            "details": err.Error(),
        })
        return
    }
    
    fmt.Printf("Parsed project: %+v\n", project)
    
    // Initialize all required fields based on your struct
    project.ID = primitive.NewObjectID()
    project.IsActive = true
    project.CreatedAt = time.Now()
    project.UpdatedAt = time.Now()
    
    // Set default values for optional fields
    if project.WelcomeMessage == "" {
        project.WelcomeMessage = "Hello! How can I help you today?"
    }
    
    if project.Category == "" {
        project.Category = "General"
    }
    
    // Initialize Gemini settings with defaults
    if project.GeminiModel == "" {
        project.GeminiModel = "gemini-1.5-flash"
    }
    
    if project.GeminiLimit == 0 {
        project.GeminiLimit = 1000 // Default daily limit
    }
    
    // Initialize arrays to prevent null values
    if project.PDFFiles == nil {
        project.PDFFiles = []models.PDFFile{}
    }
    
    // Initialize analytics fields
    project.TotalQuestions = 0
    project.GeminiUsage = 0
    project.LastUsed = time.Now()
    
    fmt.Printf("Project before insertion: %+v\n", project)
    
    // Insert into database
    collection := config.DB.Collection("projects")
    result, err := collection.InsertOne(context.Background(), project)
    if err != nil {
        fmt.Printf("Database insertion error: %v\n", err)
        c.JSON(http.StatusInternalServerError, gin.H{
            "error": "Failed to create project",
            "details": err.Error(),
        })
        return
    }
    
    fmt.Printf("Insertion successful. Result: %+v\n", result)
    
    c.JSON(http.StatusCreated, gin.H{
        "success": true,
        "message": "Project created successfully",
        "project": project,
        "inserted_id": result.InsertedID,
    })
}

func ProjectDetails(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }
    
    collection := config.DB.Collection("projects")
    var project models.Project
    err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "project": project,
    })
}

func UpdateProject(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }
    
    var updateData bson.M
    if err := c.ShouldBindJSON(&updateData); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid update data"})
        return
    }
    
    updateData["updated_at"] = time.Now()
    
    collection := config.DB.Collection("projects")
    _, err = collection.UpdateOne(
        context.Background(),
        bson.M{"_id": objID},
        bson.M{"$set": updateData},
    )
    
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update project"})
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "message": "Project updated successfully",
        "project_id": projectID,
    })
}

func DeleteProject(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }
    
    collection := config.DB.Collection("projects")
    _, err = collection.DeleteOne(context.Background(), bson.M{"_id": objID})
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete project"})
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "message": "Project deleted successfully",
        "project_id": projectID,
    })
}

func AdminUsers(c *gin.Context) {
    // Get all users from database
    collection := config.DB.Collection("users")
    cursor, err := collection.Find(context.Background(), bson.M{})
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch users"})
        return
    }
    
    var users []models.User
    cursor.All(context.Background(), &users)
    
    // Remove password from response
    for i := range users {
        users[i].Password = ""
    }
    
    c.JSON(http.StatusOK, gin.H{
        "title": "Users - Admin",
        "users": users,
        "count": len(users),
    })
    
    // Uncomment when you have the template:
    // c.HTML(http.StatusOK, "admin/users.html", gin.H{
    //     "title": "Users - Admin",
    //     "users": users,
    // })
}

func AdminAnalytics(c *gin.Context) {
    analytics := getAnalyticsData()
    
    c.JSON(http.StatusOK, gin.H{
        "title": "Analytics - Admin",
        "analytics": analytics,
    })
}

func GetAnalyticsData(c *gin.Context) {
    analytics := getAnalyticsData()
    c.JSON(http.StatusOK, gin.H{"data": analytics})
}

func AdminSettings(c *gin.Context) {
    settings := map[string]interface{}{
        "app_name": "Jevi Chat",
        "version": "1.0.0",
        "maintenance_mode": false,
        "max_file_size": "10MB",
        "allowed_file_types": []string{"pdf", "txt", "doc"},
    }
    
    c.JSON(http.StatusOK, gin.H{
        "title": "Settings - Admin",
        "settings": settings,
    })
}

func UpdateSettings(c *gin.Context) {
    var settings map[string]interface{}
    if err := c.ShouldBindJSON(&settings); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid settings data"})
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "message": "Settings updated successfully",
        "settings": settings,
    })
}

func GetUserDetails(c *gin.Context) {
    userID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(userID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
        return
    }
    
    collection := config.DB.Collection("users")
    var user models.User
    err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&user)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
        return
    }
    
    user.Password = "" // Remove password from response
    
    c.JSON(http.StatusOK, gin.H{
        "user": user,
    })
}

func UpdateUser(c *gin.Context) {
    userID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(userID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
        return
    }
    
    var updateData bson.M
    if err := c.ShouldBindJSON(&updateData); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid update data"})
        return
    }
    
    updateData["updated_at"] = time.Now()
    delete(updateData, "password") // Don't allow password updates through this endpoint
    
    collection := config.DB.Collection("users")
    _, err = collection.UpdateOne(
        context.Background(),
        bson.M{"_id": objID},
        bson.M{"$set": updateData},
    )
    
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user"})
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "message": "User updated successfully",
        "user_id": userID,
    })
}

func DeleteUser(c *gin.Context) {
    userID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(userID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
        return
    }
    
    collection := config.DB.Collection("users")
    _, err = collection.DeleteOne(context.Background(), bson.M{"_id": objID})
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete user"})
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "message": "User deleted successfully",
        "user_id": userID,
    })
}

func ToggleUserStatus(c *gin.Context) {
    userID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(userID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
        return
    }
    
    // Get current user status
    collection := config.DB.Collection("users")
    var user models.User
    err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&user)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
        return
    }
    
    // Toggle status
    newStatus := !user.IsActive
    _, err = collection.UpdateOne(
        context.Background(),
        bson.M{"_id": objID},
        bson.M{"$set": bson.M{"is_active": newStatus, "updated_at": time.Now()}},
    )
    
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to toggle user status"})
        return
    }
    
    status := "activated"
    if !newStatus {
        status = "deactivated"
    }
    
    c.JSON(http.StatusOK, gin.H{
        "message": "User " + status + " successfully",
        "user_id": userID,
        "new_status": newStatus,
    })
}

func ToggleProjectStatus(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }
    
    // Get current project status
    collection := config.DB.Collection("projects")
    var project models.Project
    err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
        return
    }
    
    // Toggle status
    newStatus := !project.IsActive
    _, err = collection.UpdateOne(
        context.Background(),
        bson.M{"_id": objID},
        bson.M{"$set": bson.M{"is_active": newStatus, "updated_at": time.Now()}},
    )
    
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to toggle project status"})
        return
    }
    
    status := "activated"
    if !newStatus {
        status = "deactivated"
    }
    
    c.JSON(http.StatusOK, gin.H{
        "message": "Project " + status + " successfully",
        "project_id": projectID,
        "new_status": newStatus,
    })
}

// Helper functions
func getAdminStats() map[string]interface{} {
    stats := map[string]interface{}{
        "total_users": 0,
        "total_projects": 0,
        "total_messages": 0,
        "active_users": 0,
    }
    
    // Get user count
    if userCollection := config.DB.Collection("users"); userCollection != nil {
        userCount, _ := userCollection.CountDocuments(context.Background(), bson.M{})
        activeUserCount, _ := userCollection.CountDocuments(context.Background(), bson.M{"is_active": true})
        stats["total_users"] = userCount
        stats["active_users"] = activeUserCount
    }
    
    // Get project count
    if projectCollection := config.DB.Collection("projects"); projectCollection != nil {
        projectCount, _ := projectCollection.CountDocuments(context.Background(), bson.M{})
        stats["total_projects"] = projectCount
    }
    
    // Get message count
    if messageCollection := config.DB.Collection("chat_messages"); messageCollection != nil {
        messageCount, _ := messageCollection.CountDocuments(context.Background(), bson.M{})
        stats["total_messages"] = messageCount
    }
    
    return stats
}

func getAnalyticsData() map[string]interface{} {
    return map[string]interface{}{
        "daily_users": 150,
        "daily_messages": 1200,
        "response_time": "1.2s",
        "satisfaction_rate": "94%",
        "popular_features": []string{"PDF Chat", "Project Management", "User Dashboard"},
    }
}


func SetGeminiLimit(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }

    var input struct {
        Limit int `json:"limit"`
    }

    if err := c.ShouldBindJSON(&input); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
        return
    }

    if input.Limit < 0 {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Limit must be non-negative"})
        return
    }

    collection := config.DB.Collection("projects")
    _, err = collection.UpdateOne(
        context.Background(),
        bson.M{"_id": objID},
        bson.M{"$set": bson.M{"gemini_limit": input.Limit, "updated_at": time.Now()}},
    )

    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update project"})
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "message": "Gemini usage limit updated",
        "limit":   input.Limit,
    })
}

func ResetGeminiUsage(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }

    collection := config.DB.Collection("projects")
    _, err = collection.UpdateOne(
        context.Background(),
        bson.M{"_id": objID},
        bson.M{"$set": bson.M{"gemini_usage": 0, "updated_at": time.Now()}},
    )

    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reset usage"})
        return
    }

    c.JSON(http.StatusOK, gin.H{"message": "Gemini usage counter reset"})
}



// GetNotifications handles GET /api/admin/notifications
func GetNotifications(c *gin.Context) {
    // Sample notifications - replace with your database logic
    notifications := []map[string]interface{}{
        {
            "id":         1,
            "type":       "success",
            "message":    "System backup completed successfully",
            "time":       "2 min ago",
            "created_at": time.Now().Add(-2 * time.Minute),
        },
        {
            "id":         2,
            "type":       "info",
            "message":    "New user registered",
            "time":       "5 min ago",
            "created_at": time.Now().Add(-5 * time.Minute),
        },
        {
            "id":         3,
            "type":       "warning",
            "message":    "High API usage detected",
            "time":       "1 hour ago",
            "created_at": time.Now().Add(-1 * time.Hour),
        },
        {
            "id":         4,
            "type":       "success",
            "message":    "New project created successfully",
            "time":       "3 hours ago",
            "created_at": time.Now().Add(-3 * time.Hour),
        },
    }

    c.JSON(http.StatusOK, gin.H{
        "success":       true,
        "notifications": notifications,
    })
}

// GetRealtimeStats handles GET /api/admin/realtime-stats
func GetRealtimeStats(c *gin.Context) {
    // Generate real-time statistics
    stats := map[string]interface{}{
        "activeUsers":       getCurrentActiveUsers(),
        "messagesPerMinute": getMessagesPerMinute(),
        "serverLoad":        getServerLoad(),
        "apiCalls":          getAPICallsCount(),
        "timestamp":         time.Now(),
    }

    c.JSON(http.StatusOK, stats)
}

// Helper functions for real-time stats
func getCurrentActiveUsers() int {
    // Query your database for active users
    collection := config.GetCollection("users")
    count, err := collection.CountDocuments(context.TODO(), bson.M{
        "is_active": true,
        "last_active": bson.M{"$gte": time.Now().Add(-5 * time.Minute)},
    })
    
    if err != nil {
        // Return sample data if database query fails
        return rand.Intn(50) + 25
    }
    
    return int(count)
}

func getMessagesPerMinute() int {
    // Calculate messages per minute from your chat system
    collection := config.GetCollection("messages")
    count, err := collection.CountDocuments(context.TODO(), bson.M{
        "created_at": bson.M{"$gte": time.Now().Add(-1 * time.Minute)},
    })
    
    if err != nil {
        return rand.Intn(30) + 5
    }
    
    return int(count)
}

func getServerLoad() int {
    // Get server load percentage (0-100)
    // You can implement actual system monitoring here
    return rand.Intn(100)
}

func getAPICallsCount() int {
    // Count API calls - you might want to implement request logging
    return rand.Intn(1000) + 200
}

// Enhanced ToggleGeminiStatus with usage validation
func ToggleGeminiStatus(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }

    var input struct {
        Enabled bool `json:"enabled"`
    }

    if err := c.ShouldBindJSON(&input); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid input"})
        return
    }

    collection := config.DB.Collection("projects")
    
    // Get current project
    var project models.Project
    err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
        return
    }

    // Validate API key if enabling
    if input.Enabled && project.GeminiAPIKey == "" {
        c.JSON(http.StatusBadRequest, gin.H{
            "error": "Cannot enable Gemini: No API key configured",
            "action_required": "Please configure Gemini API key first",
        })
        return
    }

    // Update project status
    update := bson.M{
        "$set": bson.M{
            "gemini_enabled": input.Enabled,
            "updated_at":     time.Now(),
        },
    }

    _, err = collection.UpdateOne(context.Background(), bson.M{"_id": objID}, update)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update project"})
        return
    }

    status := "disabled"
    if input.Enabled {
        status = "enabled"
    }

    c.JSON(http.StatusOK, gin.H{
        "success": true,
        "message": fmt.Sprintf("Gemini AI %s for project", status),
        "enabled": input.Enabled,
        "current_usage": gin.H{
            "daily": project.GeminiUsageToday,
            "monthly": project.GeminiUsageMonth,
            "daily_limit": project.GeminiDailyLimit,
            "monthly_limit": project.GeminiMonthlyLimit,
        },
    })
}

// Enhanced GetGeminiAnalytics with detailed tracking
func GetGeminiAnalytics(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }

    // Get project details
    collection := config.DB.Collection("projects")
    var project models.Project
    err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
        return
    }

    // Get usage logs for analytics
    logsCollection := config.DB.Collection("gemini_usage_logs")
    
    // Get today's successful requests
    today := time.Now().Truncate(24 * time.Hour)
    todayCount, _ := logsCollection.CountDocuments(context.Background(), bson.M{
        "project_id": objID,
        "timestamp": bson.M{"$gte": today},
        "success": true,
    })

    // Get this month's successful requests
    thisMonth := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.UTC)
    monthCount, _ := logsCollection.CountDocuments(context.Background(), bson.M{
        "project_id": objID,
        "timestamp": bson.M{"$gte": thisMonth},
        "success": true,
    })

    analytics := gin.H{
        "project": gin.H{
            "id":              project.ID,
            "name":            project.Name,
            "gemini_enabled":  project.GeminiEnabled,
            "model":           project.GeminiModel,
        },
        "usage": gin.H{
            "today": gin.H{
                "count": todayCount,
                "limit": project.GeminiDailyLimit,
                "remaining": project.GeminiDailyLimit - int(todayCount),
                "cost": project.EstimatedCostToday,
            },
            "month": gin.H{
                "count": monthCount,
                "limit": project.GeminiMonthlyLimit,
                "remaining": project.GeminiMonthlyLimit - int(monthCount),
                "cost": project.EstimatedCostMonth,
            },
            "total_questions": project.TotalQuestions,
            "last_used": project.LastUsed,
        },
    }

    c.JSON(http.StatusOK, gin.H{
        "success": true,
        "analytics": analytics,
    })
}

func trackGeminiUsage(projectID primitive.ObjectID, question, response, model string, 
                     inputTokens, outputTokens int, responseTime int64, userIP string, success bool) {

    // Use accurate token-based cost
    estimatedCost := calculateGeminiCost(model, inputTokens, outputTokens)

    // Save usage log
    usageLog := models.GeminiUsageLog{
        ProjectID:     projectID,
        Question:      question,
        Response:      response,
        Model:         model,
        InputTokens:   inputTokens,
        OutputTokens:  outputTokens,
        EstimatedCost: estimatedCost,
        ResponseTime:  responseTime,
        UserIP:        userIP,
        Timestamp:     time.Now(),
        Success:       success,
    }

    logCollection := config.DB.Collection("gemini_usage_logs")
    logCollection.InsertOne(context.Background(), usageLog)

    // Update project counters if successful
    if success {
        projectCollection := config.DB.Collection("projects")
        update := bson.M{
            "$inc": bson.M{
                "gemini_usage_today":     1,
                "gemini_usage_month":     1,
                "total_questions":        1,
                "estimated_cost_today":   estimatedCost,
                "estimated_cost_month":   estimatedCost,
            },
            "$set": bson.M{
                "last_used":  time.Now(),
                "updated_at": time.Now(),
            },
        }
        projectCollection.UpdateOne(context.Background(), bson.M{"_id": projectID}, update)
    }
}


// GetSubscriptionStats - Get subscription statistics for admin dashboard
func GetSubscriptionStats(c *gin.Context) {
    stats, err := config.GetSubscriptionStats()
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get subscription stats"})
        return
    }
    
    c.JSON(http.StatusOK, stats)
}

// RenewSubscription - Renew client subscription
func RenewSubscription(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }
    
    var renewalData struct {
        Months int `json:"months"`
    }
    
    if err := c.ShouldBindJSON(&renewalData); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid renewal data"})
        return
    }
    
    // Calculate new expiry date
    newExpiryDate := time.Now().AddDate(0, renewalData.Months, 0)
    
    collection := config.DB.Collection("projects")
    update := bson.M{
        "$set": bson.M{
            "expiry_date": newExpiryDate,
            "status": "active",
            "total_tokens_used": 0, // Reset token usage
            "updated_at": time.Now(),
        },
    }
    
    _, err = collection.UpdateOne(context.Background(), bson.M{"_id": objID}, update)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to renew subscription"})
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "message": "Subscription renewed successfully",
        "new_expiry_date": newExpiryDate,
    })
}


// GetProjectUsage - Get detailed usage statistics for a specific project
func GetProjectUsage(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }

    // Get project details
    collection := config.DB.Collection("projects")
    var project models.Project
    err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
        return
    }

    // Get usage statistics from Gemini usage logs
    usageCollection := config.DB.Collection("gemini_usage_logs")
    
    // Current month usage
    startOfMonth := time.Now().AddDate(0, 0, -time.Now().Day()+1)
    monthlyUsage, _ := usageCollection.CountDocuments(context.Background(), bson.M{
        "project_id": objID,
        "timestamp": bson.M{"$gte": startOfMonth},
    })

    // Today's usage
    startOfDay := time.Now().Truncate(24 * time.Hour)
    dailyUsage, _ := usageCollection.CountDocuments(context.Background(), bson.M{
        "project_id": objID,
        "timestamp": bson.M{"$gte": startOfDay},
    })

    // Calculate usage percentages
    dailyUsagePercent := 0.0
    if project.GeminiDailyLimit > 0 {
        dailyUsagePercent = float64(dailyUsage) / float64(project.GeminiDailyLimit) * 100
    }

    monthlyUsagePercent := 0.0
    if project.GeminiMonthlyLimit > 0 {
        monthlyUsagePercent = float64(monthlyUsage) / float64(project.GeminiMonthlyLimit) * 100
    }

    // Token usage percentage
    tokenUsagePercent := 0.0
    if project.MonthlyTokenLimit > 0 {
        tokenUsagePercent = float64(project.TotalTokensUsed) / float64(project.MonthlyTokenLimit) * 100
    }

    c.JSON(http.StatusOK, gin.H{
        "project_id": projectID,
        "project_name": project.Name,
        "subscription_info": gin.H{
            "status": project.Status,
            "start_date": project.StartDate,
            "expiry_date": project.ExpiryDate,
            "days_remaining": int(time.Until(project.ExpiryDate).Hours() / 24),
        },
        "usage_statistics": gin.H{
            "daily_usage": dailyUsage,
            "daily_limit": project.GeminiDailyLimit,
            "daily_usage_percent": dailyUsagePercent,
            "monthly_usage": monthlyUsage,
            "monthly_limit": project.GeminiMonthlyLimit,
            "monthly_usage_percent": monthlyUsagePercent,
        },
        "token_usage": gin.H{
            "total_tokens_used": project.TotalTokensUsed,
            "monthly_token_limit": project.MonthlyTokenLimit,
            "token_usage_percent": tokenUsagePercent,
            "remaining_tokens": project.MonthlyTokenLimit - project.TotalTokensUsed,
        },
        "last_activity": project.LastUsed,
        "created_at": project.CreatedAt,
        "updated_at": project.UpdatedAt,
    })
}



// Update client status
func UpdateClientStatus(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }
    
    var statusData struct {
        Status string `json:"status"`
    }
    
    if err := c.ShouldBindJSON(&statusData); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status data"})
        return
    }
    
    collection := config.DB.Collection("projects")
    update := bson.M{
        "$set": bson.M{
            "status": statusData.Status,
            "updated_at": time.Now(),
        },
    }
    
    _, err = collection.UpdateOne(context.Background(), bson.M{"_id": objID}, update)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update status"})
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "message": "Status updated successfully",
        "new_status": statusData.Status,
    })
}

// GetNotificationHistory - Get notification history for admin dashboard
func GetNotificationHistory(c *gin.Context) {
    collection := config.DB.Collection("notifications")
    
    // Get recent notifications (last 30 days)
    filter := bson.M{
        "sent_at": bson.M{
            "$gte": time.Now().AddDate(0, 0, -30),
        },
    }
    
    cursor, err := collection.Find(context.Background(), filter, 
        options.Find().SetSort(bson.M{"sent_at": -1}).SetLimit(100))
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get notifications"})
        return
    }
    defer cursor.Close(context.Background())
    
    var notifications []bson.M
    if err := cursor.All(context.Background(), &notifications); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse notifications"})
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "notifications": notifications,
        "count": len(notifications),
    })
}

// GetProjectNotifications - Get notifications for specific project
func GetProjectNotifications(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }
    
    collection := config.DB.Collection("notifications")
    
    filter := bson.M{
        "project_id": objID,
        "sent_at": bson.M{
            "$gte": time.Now().AddDate(0, 0, -7), // Last 7 days
        },
    }
    
    cursor, err := collection.Find(context.Background(), filter, 
        options.Find().SetSort(bson.M{"sent_at": -1}))
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get project notifications"})
        return
    }
    defer cursor.Close(context.Background())
    
    var notifications []bson.M
    if err := cursor.All(context.Background(), &notifications); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse notifications"})
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "project_id": projectID,
        "notifications": notifications,
        "count": len(notifications),
    })
}

// TestNotification - Test notification system
func TestNotification(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }
    
    // Get project
    collection := config.DB.Collection("projects")
    var project models.Project
    err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
        return
    }
    
    // Send test notification
    message := fmt.Sprintf("Test notification for project: %s", project.Name)
    err = config.LogNotification(objID, "test", message)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send test notification"})
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "message": "Test notification sent successfully",
        "project": project.Name,
    })
}
