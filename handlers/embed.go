package handlers

import (
    "context"
    "crypto/md5"
    "fmt"
    "net/http"
    "time"
    "crypto/rand"
    "encoding/hex"
    "os"
    "github.com/gin-gonic/gin"
    "go.mongodb.org/mongo-driver/bson"
    "go.mongodb.org/mongo-driver/bson/primitive"
    "jevi-chat/config"
    "jevi-chat/models"
)

func EmbedChat(c *gin.Context) {
    projectID := c.Param("projectId")
    
    // Check if user is already authenticated
    userToken := c.Query("token")
    if userToken == "" {
        // Show pre-chat authentication form
        c.HTML(http.StatusOK, "prechat.html", gin.H{
            "project_id": projectID,
            "api_url":    os.Getenv("APP_URL"), 
        })
        return
    }
    
    // Validate user token and proceed to chat
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.HTML(http.StatusOK, "error.html", gin.H{
            "error": "Invalid project ID: " + projectID,
        })
        return
    }
    
    // Get project details from database
    collection := config.DB.Collection("projects")
    var project models.Project
    err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
    if err != nil {
        c.HTML(http.StatusOK, "error.html", gin.H{
            "error": "Project not found or inactive",
        })
        return
    }
    
    // Check if project is active
    if !project.IsActive {
        c.HTML(http.StatusOK, "error.html", gin.H{
            "error": "This chat is currently inactive",
        })
        return
    }
    
    // Validate user token
    userID, err := validateUserToken(userToken)
    if err != nil {
        // Invalid token, redirect to auth
        c.Redirect(http.StatusFound, fmt.Sprintf("/embed/%s", projectID))
        return
    }
    
    // Get user details
    userCollection := config.DB.Collection("chat_users")
    var user models.ChatUser
    userObjID, _ := primitive.ObjectIDFromHex(userID)
    err = userCollection.FindOne(context.Background(), bson.M{"_id": userObjID}).Decode(&user)
    if err != nil {
        c.Redirect(http.StatusFound, fmt.Sprintf("/embed/%s", projectID))
        return
    }
    
    // Show chat interface with user info
    c.HTML(http.StatusOK, "chat.html", gin.H{
        "project":    project,
        "project_id": projectID,
        "api_url":    os.Getenv("APP_URL"),
        "user":       user,
        "user_token": userToken,
    })
}

// Handle authentication for embed chat
func EmbedAuth(c *gin.Context) {
    projectID := c.Param("projectId")
    
    var authData struct {
        Mode     string `json:"mode"`
        Name     string `json:"name"`
        Email    string `json:"email"`
        Password string `json:"password"`
    }
    
    if err := c.ShouldBindJSON(&authData); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid data"})
        return
    }
    
    // Validate project exists
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid project"})
        return
    }
    
    projectCollection := config.DB.Collection("projects")
    var project models.Project
    err = projectCollection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Project not found"})
        return
    }
    
    userCollection := config.DB.Collection("chat_users")
    
    if authData.Mode == "register" {
        // Handle registration
        
        // Check if user already exists
        var existingUser models.ChatUser
        err := userCollection.FindOne(context.Background(), bson.M{
            "project_id": projectID,
            "email":      authData.Email,
        }).Decode(&existingUser)
        
        if err == nil {
            c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Email already registered for this chat"})
            return
        }
        
        // Create new user
        user := models.ChatUser{
            ProjectID: projectID,
            Name:      authData.Name,
            Email:     authData.Email,
            Password:  hashPassword(authData.Password),
            CreatedAt: time.Now(),
            IsActive:  true,
        }
        
        result, err := userCollection.InsertOne(context.Background(), user)
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create account"})
            return
        }
        
        user.ID = result.InsertedID.(primitive.ObjectID)
        token := generateUserToken(user.ID.Hex())
        
        c.JSON(http.StatusOK, gin.H{
            "success": true,
            "user": gin.H{
                "id":    user.ID.Hex(),
                "name":  user.Name,
                "email": user.Email,
            },
            "token": token,
        })
        
    } else {
        // Handle login
        var user models.ChatUser
        err := userCollection.FindOne(context.Background(), bson.M{
            "project_id": projectID,
            "email":      authData.Email,
        }).Decode(&user)
        
        if err != nil || !verifyPassword(authData.Password, user.Password) {
            c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid email or password"})
            return
        }
        
        if !user.IsActive {
            c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Account is deactivated"})
            return
        }
        
        token := generateUserToken(user.ID.Hex())
        
        c.JSON(http.StatusOK, gin.H{
            "success": true,
            "user": gin.H{
                "id":    user.ID.Hex(),
                "name":  user.Name,
                "email": user.Email,
            },
            "token": token,
        })
    }
}

func IframeChatInterface(c *gin.Context) {
    projectID := c.Param("projectId")
    
    // Validate project ID
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
    
    c.JSON(http.StatusOK, gin.H{
        "project": project,
        "status":  "active",
    })
}



// Helper functions for authentication
func hashPassword(password string) string {
    hash := md5.Sum([]byte(password + "jevi_salt")) // Simple hashing, use bcrypt in production
    return hex.EncodeToString(hash[:])
}

func verifyPassword(password, hash string) bool {
    return hashPassword(password) == hash
}

func generateUserToken(userID string) string {
    // Simple token generation, use JWT in production
    bytes := make([]byte, 16)
    rand.Read(bytes)
    return fmt.Sprintf("%s_%s_%d", userID, hex.EncodeToString(bytes), time.Now().Unix())
}



func EmbedHealth(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{
        "status":    "healthy",
        "service":   "jevi-chat-embed",
        "timestamp": time.Now().Format(time.RFC3339),
    })
}
