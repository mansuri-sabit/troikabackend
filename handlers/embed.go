package handlers

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"jevi-chat/config"
	"jevi-chat/models"
)

// GET /embed/:projectId
func EmbedChat(c *gin.Context) {
	projectID := c.Param("projectId")

	userToken := c.Query("token")
	if userToken == "" {
		c.HTML(http.StatusOK, "prechat.html", gin.H{
			"project_id": projectID,
			"api_url":    os.Getenv("APP_URL"),
		})
		return
	}

	objID, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		c.HTML(http.StatusOK, "error.html", gin.H{"error": "Invalid project ID"})
		return
	}

	projectCollection := config.DB.Collection("projects")
	var project models.Project
	err = projectCollection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
	if err != nil || !project.IsActive {
		c.HTML(http.StatusOK, "error.html", gin.H{"error": "Project not found or inactive"})
		return
	}

	userID, err := validateUserToken(userToken)
	if err != nil {
		c.Redirect(http.StatusFound, fmt.Sprintf("/embed/%s", projectID))
		return
	}

	userCollection := config.DB.Collection("chat_users")
	var user models.ChatUser
	userObjID, _ := primitive.ObjectIDFromHex(userID)
	err = userCollection.FindOne(context.Background(), bson.M{"_id": userObjID}).Decode(&user)
	if err != nil {
		c.Redirect(http.StatusFound, fmt.Sprintf("/embed/%s", projectID))
		return
	}

	c.HTML(http.StatusOK, "chat.html", gin.H{
		"project":    project,
		"project_id": projectID,
		"api_url":    os.Getenv("APP_URL"),
		"user":       user,
		"user_token": userToken,
	})
}

// Handle both GET and POST for /embed/:projectId/auth
func EmbedAuth(c *gin.Context) {
	projectID := c.Param("projectId")

	// Handle GET request - show auth page
	if c.Request.Method == "GET" {
		objID, err := primitive.ObjectIDFromHex(projectID)
		if err != nil {
			c.HTML(http.StatusOK, "error.html", gin.H{"error": "Invalid project ID"})
			return
		}

		collection := config.DB.Collection("projects")
		var project models.Project
		err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
		if err != nil || !project.IsActive {
			c.HTML(http.StatusOK, "error.html", gin.H{"error": "Project not found or inactive"})
			return
		}

		c.HTML(http.StatusOK, "embed/auth.html", gin.H{
			"project":    project,
			"project_id": projectID,
			"api_url":    os.Getenv("APP_URL"),
		})
		return
	}

	// Handle POST request - process authentication
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

	objID, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid project"})
		return
	}

	projectCollection := config.DB.Collection("projects")
	var project models.Project
	if err := projectCollection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Project not found"})
		return
	}

	userCollection := config.DB.Collection("chat_users")

	if authData.Mode == "register" {
		var existingUser models.ChatUser
		err := userCollection.FindOne(context.Background(), bson.M{
			"project_id": projectID,
			"email":      authData.Email,
		}).Decode(&existingUser)
		if err == nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Email already registered"})
			return
		}

		user := models.ChatUser{
			ProjectID: projectID,
			Name:      authData.Name,
			Email:     authData.Email,
			Password:  hashPassword(authData.Password),
			IsActive:  true,
			CreatedAt: time.Now(),
		}

		result, err := userCollection.InsertOne(context.Background(), user)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to create user"})
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
		return
	}

	// Login
	var user models.ChatUser
	err = userCollection.FindOne(context.Background(), bson.M{
		"project_id": projectID,
		"email":      authData.Email,
	}).Decode(&user)
	if err != nil || !verifyPassword(authData.Password, user.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Invalid credentials"})
		return
	}

	if !user.IsActive {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "Account deactivated"})
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

// GET /embed/:projectId/chat - Chat interface
func IframeChatInterface(c *gin.Context) {
    projectID := c.Param("projectId")

    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.String(http.StatusBadRequest, "Invalid project ID")
        return
    }

    var project models.Project
    err = config.DB.Collection("projects").FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
    if err != nil {
        c.String(http.StatusNotFound, "Project not found")
        return
    }

    c.HTML(http.StatusOK, "embed/chat.html", gin.H{
        "project":     project,
        "project_id":  project.ID.Hex(),
        "api_url":     os.Getenv("APP_URL"),
    })
}

// Simple health check
func EmbedHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"service":   "jevi-chat-embed",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// Utility functions
func hashPassword(password string) string {
	hash := md5.Sum([]byte(password + "jevi_salt"))
	return hex.EncodeToString(hash[:])
}

func verifyPassword(password, hash string) bool {
	return hashPassword(password) == hash
}

func generateUserToken(userID string) string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return fmt.Sprintf("%s_%s_%d", userID, hex.EncodeToString(bytes), time.Now().Unix())
}
