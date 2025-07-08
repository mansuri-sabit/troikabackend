package middleware

import (
    "context"
    "net/http"
    "time"
    "github.com/gin-gonic/gin"
    "go.mongodb.org/mongo-driver/bson"
    "go.mongodb.org/mongo-driver/bson/primitive"
    "jevi-chat/config"
    "jevi-chat/models"
)

func ValidateSubscription() gin.HandlerFunc {
    return func(c *gin.Context) {
        projectID := c.Param("projectId")
        if projectID == "" {
            c.JSON(http.StatusBadRequest, gin.H{
                "error": "Project ID is required",
                "blocked": true,
            })
            c.Abort()
            return
        }
        
        objID, err := primitive.ObjectIDFromHex(projectID)
        if err != nil {
            c.JSON(http.StatusBadRequest, gin.H{
                "error": "Invalid project ID",
                "blocked": true,
            })
            c.Abort()
            return
        }
        
        // Get project details
        collection := config.DB.Collection("projects")
        var project models.Project
        err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
        if err != nil {
            c.JSON(http.StatusNotFound, gin.H{
                "error": "Project not found",
                "blocked": true,
            })
            c.Abort()
            return
        }
        
        now := time.Now()
        
        // 1. Check if subscription is expired
        if !project.ExpiryDate.IsZero() && now.After(project.ExpiryDate) {
            c.JSON(http.StatusForbidden, gin.H{
                "error": "Your subscription has expired. Please renew to continue.",
                "blocked": true,
                "expiry_date": project.ExpiryDate,
            })
            c.Abort()
            return
        }
        
        // 2. Check if status is active
        if project.Status != "" && project.Status != "active" {
            var message string
            switch project.Status {
            case "expired":
                message = "Your subscription has expired. Please renew to continue."
            case "suspended":
                message = "Your account has been suspended. Please contact support."
            default:
                message = "Your account is not active. Please contact support."
            }
            
            c.JSON(http.StatusForbidden, gin.H{
                "error": message,
                "blocked": true,
                "status": project.Status,
            })
            c.Abort()
            return
        }
        
        // 3. Check monthly token limit
        if project.MonthlyTokenLimit > 0 && project.TotalTokensUsed >= project.MonthlyTokenLimit {
            c.JSON(http.StatusForbidden, gin.H{
                "error": "Monthly usage limit reached. Please upgrade your plan.",
                "blocked": true,
                "tokens_used": project.TotalTokensUsed,
                "token_limit": project.MonthlyTokenLimit,
            })
            c.Abort()
            return
        }
        
        // Store project in context for later use
        c.Set("project", project)
        c.Next()
    }
}
