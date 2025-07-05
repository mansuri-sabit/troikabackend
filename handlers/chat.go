package handlers

import (
    "context"
    "fmt"
    "github.com/gin-gonic/gin"
    "github.com/google/generative-ai-go/genai"
    "go.mongodb.org/mongo-driver/bson"
    "go.mongodb.org/mongo-driver/bson/primitive"
    "go.mongodb.org/mongo-driver/mongo/options"
    "google.golang.org/api/option"
    "html"
    "jevi-chat/config"
    "jevi-chat/models"
    "math"
    "net/http"
    "strings"
    "sync"
    "time"
)

// ===== GLOBAL VARIABLES FOR RATE LIMITING =====
var rateLimitMap = make(map[string][]time.Time)
var rateLimitMutex sync.RWMutex

// ===== MAIN CHAT HANDLERS =====

func SendMessage(c *gin.Context) {
    projectID := c.Param("id")
    sessionID := c.Query("session_id")

    var request struct {
        Message string `json:"message"`
    }

    if err := c.ShouldBindJSON(&request); err != nil || request.Message == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid message input"})
        return
    }

    // Load project
    var project models.Project
    projectCollection := config.DB.Collection("projects")
    objID, _ := primitive.ObjectIDFromHex(projectID)
    err := projectCollection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
        return
    }

    // Check Gemini is enabled
    if !project.GeminiEnabled {
        c.JSON(http.StatusForbidden, gin.H{"error": "Gemini is not enabled for this project"})
        return
    }

    // Keep project-level usage limit check
    if !project.IsWithinLimit() {
        c.JSON(http.StatusForbidden, gin.H{"error": "Project Gemini usage limit reached"})
        return
    }

    // Generate AI response
    userIP := c.ClientIP()
    user := models.ChatUser{} // Optional: populate if logged in
    response, inputTokens, outputTokens, err := generateGeminiResponseWithTracking(project, request.Message, userIP, user)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Gemini API error", "details": err.Error()})
        return
    }

    // Save to chat history
    chatMsg := models.ChatMessage{
        ProjectID: objID,
        SessionID: sessionID,
        Message:   request.Message,
        Response:  response,
        IsUser:    true,
        Timestamp: time.Now(),
        IPAddress: userIP,
    }
    config.DB.Collection("chat_messages").InsertOne(context.Background(), chatMsg)

    // Update Gemini usage count
    usageIncrement := inputTokens + outputTokens
    update := bson.M{
        "$inc": bson.M{
            "gemini_usage":        usageIncrement,
            "gemini_usage_today":  usageIncrement,
            "gemini_usage_month":  usageIncrement,
        },
        "$set": bson.M{"last_used": time.Now()},
    }
    projectCollection.UpdateOne(context.Background(), bson.M{"_id": objID}, update)

    // Return AI response
    c.JSON(http.StatusOK, gin.H{
        "response": response,
        "tokens": map[string]int{
            "input":  inputTokens,
            "output": outputTokens,
        },
    })
}

// IframeSendMessage - For embed widget users with enhanced features
func IframeSendMessage(c *gin.Context) {
    projectID := c.Param("projectId")
    startTime := time.Now()

    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }

    var messageData struct {
        Message   string `json:"message"`
        SessionID string `json:"session_id"`
        UserToken string `json:"user_token"`
    }

    if err := c.ShouldBindJSON(&messageData); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid message data"})
        return
    }

    // Generate session ID if not provided
    if messageData.SessionID == "" {
        messageData.SessionID = generateSessionID()
    }

    // Sanitize and validate input
    messageData.Message = sanitizeInput(messageData.Message)
    if messageData.Message == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Message cannot be empty"})
        return
    }

    // Remove user-specific rate limiting - only basic spam protection
    if !checkBasicRateLimit(c.ClientIP()) {
        c.JSON(http.StatusTooManyRequests, gin.H{"error": "Please wait before sending another message"})
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

    // Check if project is active
    if !project.IsActive {
        c.JSON(http.StatusForbidden, gin.H{"error": "This chat is currently unavailable"})
        return
    }

    // Check if Gemini is enabled
    if !project.GeminiEnabled {
        c.JSON(http.StatusForbidden, gin.H{
            "error":  "AI responses are currently disabled for this project",
            "status": "gemini_disabled",
        })
        return
    }

    // Keep project-level usage limits
    if project.GeminiUsageToday >= project.GeminiDailyLimit {
        c.JSON(http.StatusTooManyRequests, gin.H{
            "error":  "Daily AI usage limit reached for this project",
            "status": "daily_limit_exceeded",
            "usage_info": gin.H{
                "daily_usage": project.GeminiUsageToday,
                "daily_limit": project.GeminiDailyLimit,
                "resets_at":   getNextDailyReset(),
            },
        })
        return
    }

    if project.GeminiUsageMonth >= project.GeminiMonthlyLimit {
        c.JSON(http.StatusTooManyRequests, gin.H{
            "error":  "Monthly AI usage limit reached for this project",
            "status": "monthly_limit_exceeded",
            "usage_info": gin.H{
                "monthly_usage": project.GeminiUsageMonth,
                "monthly_limit": project.GeminiMonthlyLimit,
                "resets_at":     getNextMonthlyReset(),
            },
        })
        return
    }

    // Get user info if token provided
    var user models.ChatUser
    if messageData.UserToken != "" {
        userID, err := validateUserToken(messageData.UserToken)
        if err == nil {
            userCollection := config.DB.Collection("chat_users")
            userObjID, _ := primitive.ObjectIDFromHex(userID)
            userCollection.FindOne(context.Background(), bson.M{"_id": userObjID}).Decode(&user)
        }
    }

    var response string
    var inputTokens, outputTokens int
    var success bool = true
    var errorMsg string

    // First-message greeting logic + 4-second delay for all responses
    time.Sleep(4 * time.Second)

    if isFirstMessage(objID, messageData.SessionID) {
        response = getWelcomeMessage(project.WelcomeMessage)
    } else if project.GeminiAPIKey != "" {
        response, inputTokens, outputTokens, err = generateGeminiResponseWithTracking(
            project, messageData.Message, c.ClientIP(), user)
        if err != nil {
            success = false
            errorMsg = err.Error()
            if user.Name != "" {
                response = fmt.Sprintf("Hello %s! I'm having trouble answering just now. Please try again later.", user.Name)
            } else {
                response = "I'm having trouble answering just now. Please try again later."
            }
        }
    } else {
        success = false
        errorMsg = "No API key configured"
        response = "AI configuration is incomplete. Please contact support."
    }

    // Calculate response time
    responseTime := time.Since(startTime).Milliseconds()

    // Save message to database with user info
    saveMessage(objID, messageData.Message, response, messageData.SessionID, c.ClientIP(), user)

    // Prepare response with project usage information
    responseData := gin.H{
        "response":   response,
        "project_id": projectID,
        "status":     "success",
        "timestamp":  time.Now().Format(time.RFC3339),
        "user_name":  user.Name,
        "usage_info": gin.H{
            "daily_usage":     project.GeminiUsageToday + 1,
            "daily_limit":     project.GeminiDailyLimit,
            "daily_remaining": max(0, project.GeminiDailyLimit-project.GeminiUsageToday-1),
            "monthly_usage":   project.GeminiUsageMonth + 1,
            "monthly_limit":   project.GeminiMonthlyLimit,
            "response_time":   responseTime,
            "tokens_used":     inputTokens + outputTokens,
        },
    }

    if !success {
        responseData["status"] = "error"
        responseData["error_details"] = errorMsg
    }

    c.JSON(http.StatusOK, responseData)
}

// HandleChatMessage - Alternative handler for project-specific PDF content
func HandleChatMessage(c *gin.Context) {
    projectID := c.Param("projectId")

    var messageData struct {
        Message   string `json:"message"`
        SessionID string `json:"session_id"`
        UserToken string `json:"user_token"`
    }

    if err := c.ShouldBindJSON(&messageData); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid data"})
        return
    }

    // Generate session ID if not provided
    if messageData.SessionID == "" {
        messageData.SessionID = generateSessionID()
    }

    // Sanitize input
    messageData.Message = sanitizeInput(messageData.Message)
    if messageData.Message == "" {
        c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Message cannot be empty"})
        return
    }

    // Validate project
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid project"})
        return
    }

    // Get project with PDF content
    collection := config.DB.Collection("projects")
    var project models.Project
    err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
    if err != nil || !project.IsActive {
        c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Project not found"})
        return
    }

    // Check if Gemini is enabled and has PDF content
    if !project.GeminiEnabled || project.GeminiAPIKey == "" {
        c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "AI service not enabled"})
        return
    }

    if project.PDFContent == "" {
        c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "No knowledge base available. Please upload PDF files first."})
        return
    }

    // Keep project usage limits
    if !project.IsWithinLimit() {
        c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": "Project usage limit exceeded"})
        return
    }

    // Generate response using project's PDF content
    response, err := generateResponseWithPDFContent(messageData.Message, project)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "Failed to generate response"})
        return
    }

    // Save chat message
    chatMessage := models.ChatMessage{
        ProjectID: objID,
        SessionID: messageData.SessionID,
        Message:   messageData.Message,
        Response:  response,
        IsUser:    false,
        Timestamp: time.Now(),
        IPAddress: c.ClientIP(),
    }

    chatCollection := config.DB.Collection("chat_messages")
    result, _ := chatCollection.InsertOne(context.Background(), chatMessage)
    if result != nil {
        chatMessage.ID = result.InsertedID.(primitive.ObjectID)
    }

    // Update project usage
    go updateProjectUsage(objID)

    c.JSON(http.StatusOK, gin.H{
        "success":    true,
        "response":   response,
        "message_id": chatMessage.ID.Hex(),
    })
}

// ===== AI RESPONSE GENERATION =====

// generateResponseWithPDFContent - Use project's PDF content for AI response
func generateResponseWithPDFContent(userMessage string, project models.Project) (string, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // Debug log
    fmt.Printf("DEBUG: Processing message for Project ID: %s, Project Name: %s\n", project.ID.Hex(), project.Name)
    fmt.Printf("DEBUG: PDF Content Length: %d characters\n", len(project.PDFContent))

    // Check if PDF content is available
    if project.PDFContent == "" {
        return "", fmt.Errorf("no PDF content available for project %s", project.Name)
    }

    // Create Gemini client
    client, err := genai.NewClient(ctx, option.WithAPIKey(project.GeminiAPIKey))
    if err != nil {
        return "", fmt.Errorf("failed to create Gemini client: %v", err)
    }
    defer client.Close()

    // Use specified model or default
    modelName := getGeminiModel(project.GeminiModel)
    model := client.GenerativeModel(modelName)

    // Configure model for better responses
    model.SetTemperature(0.85)
    model.SetTopP(0.9)
    model.SetTopK(40)

    // Enhanced prompt with project's PDF content
    prompt := fmt.Sprintf(`You are an AI assistant for %s. Use the following knowledge base to answer user questions accurately and helpfully.

KNOWLEDGE BASE (from uploaded PDFs):
%s

USER QUESTION: %s

INSTRUCTIONS:
1. Answer based ONLY on the information provided in the knowledge base above
2. If the answer is not in the knowledge base, politely say you don't have that information
3. Be helpful, accurate, and conversational
4. Provide specific details when available
5. If relevant, mention which section or document the information comes from
6. Use a warm, friendly tone (avoid robotic phrases)
7. Keep it concise: 2-3 well-formed sentences unless detail is essential
8. Never repeat any word, phrase, or sentence in the same reply
9. Vary your wording and sentence structure

RESPONSE:`, project.Name, project.PDFContent, userMessage)

    // Generate response
    resp, err := model.GenerateContent(ctx, genai.Text(prompt))
    if err != nil {
        return "", fmt.Errorf("failed to generate content: %v", err)
    }

    if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
        return string(resp.Candidates[0].Content.Parts[0].(genai.Text)), nil
    }

    return "I'm sorry, I couldn't generate a response at this time.", nil
}

// generateAIResponse - Enhanced AI response generation for authenticated users
func generateAIResponse(userMessage, pdfContent, geminiKey, projectName, geminiModel string) (string, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    client, err := genai.NewClient(ctx, option.WithAPIKey(geminiKey))
    if err != nil {
        return "", fmt.Errorf("failed to create Gemini client: %v", err)
    }
    defer client.Close()

    // Use specified model or default
    modelName := getGeminiModel(geminiModel)
    model := client.GenerativeModel(modelName)

    // Configure model for better responses
    model.SetTemperature(0.85)
    model.SetTopP(0.9)
    model.SetTopK(40)

    // Enhanced prompt with natural tone and anti-repetition
    prompt := fmt.Sprintf(`
You are a helpful AI assistant for %s. Respond naturally and conversationally without repeating phrases.

KNOWLEDGE BASE:
%s

USER QUESTION:
%s

GUIDELINES:
– Base the answer on the knowledge-base content when possible  
– Use a warm, friendly tone (avoid robotic phrases)  
– Keep it short: 2-3 well-formed sentences unless detail is essential  
– **Never** repeat any word, phrase, or sentence in the same reply  
– Vary your wording and sentence structure  
– If the docs don't contain the answer, say so politely and offer general help  
– End the reply naturally without filler or repetition.

Answer:`, projectName, pdfContent, userMessage)

    resp, err := model.GenerateContent(ctx, genai.Text(prompt))
    if err != nil {
        return "", fmt.Errorf("failed to generate content: %v", err)
    }

    if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
        return fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0]), nil
    }

    return "I'm sorry, I couldn't generate a response at the moment. Please try again.", nil
}

// generateGeminiResponse - Enhanced response generation for embed users
func generateGeminiResponse(project models.Project, userMessage, userIP string, user models.ChatUser) (string, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    client, err := genai.NewClient(ctx, option.WithAPIKey(project.GeminiAPIKey))
    if err != nil {
        return "", err
    }
    defer client.Close()

    // Use specified model or default
    modelName := getGeminiModel(project.GeminiModel)
    model := client.GenerativeModel(modelName)

    // Configure model for better responses
    model.SetTemperature(0.85)
    model.SetTopP(0.9)
    model.SetTopK(40)

    // Personalized greeting if user is known
    userContext := ""
    if user.Name != "" {
        userContext = fmt.Sprintf("The user's name is %s. ", user.Name)
    }

    // Enhanced prompt with natural tone
    prompt := fmt.Sprintf(`
You are a helpful AI assistant for %s. %sRespond naturally and conversationally without repeating phrases.

KNOWLEDGE BASE:
%s

USER QUESTION:
%s

GUIDELINES:
– Base the answer on the knowledge-base content when possible  
– Use a warm, friendly tone (avoid robotic phrases)  
– Keep it short: 2-3 well-formed sentences unless detail is essential  
– **Never** repeat any word, phrase, or sentence in the same reply  
– Vary your wording and sentence structure  
– If the docs don't contain the answer, say so politely and offer general help  
– End the reply naturally without filler or repetition.

Answer:`, project.Name, userContext, project.PDFContent, userMessage)

    resp, err := model.GenerateContent(ctx, genai.Text(prompt))
    if err != nil {
        return "", err
    }

    if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
        response := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])

        // Log usage asynchronously
        go logGeminiUsage(project.ID, userMessage, response, userIP, user)

        return response, nil
    }

    return "", fmt.Errorf("no response generated")
}

// generateGeminiResponseWithTracking - Enhanced AI response generation with token tracking
func generateGeminiResponseWithTracking(project models.Project, userMessage, userIP string, user models.ChatUser) (string, int, int, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    client, err := genai.NewClient(ctx, option.WithAPIKey(project.GeminiAPIKey))
    if err != nil {
        return "", 0, 0, fmt.Errorf("failed to create Gemini client: %v", err)
    }
    defer client.Close()

    // Use specified model or default
    modelName := getGeminiModel(project.GeminiModel)
    model := client.GenerativeModel(modelName)

    // Configure model for better responses
    model.SetTemperature(0.85)
    model.SetTopP(0.9)
    model.SetTopK(40)

    // Personalized greeting if user is known
    userContext := ""
    if user.Name != "" {
        userContext = fmt.Sprintf("The user's name is %s. ", user.Name)
    }

    // Enhanced prompt with anti-repetition and natural tone instructions
    prompt := fmt.Sprintf(`
You are a helpful AI assistant for %s. %sRespond naturally and conversationally without repeating phrases.

KNOWLEDGE BASE:
%s

USER QUESTION:
%s

GUIDELINES:
– Base the answer on the knowledge-base content when possible  
– Use a warm, friendly tone (avoid robotic phrases)  
– Keep it short: 2-3 well-formed sentences unless detail is essential  
– **Never** repeat any word, phrase, or sentence in the same reply  
– Vary your wording and sentence structure  
– If the docs don't contain the answer, say so politely and offer general help  
– End the reply naturally without filler or repetition.

Answer:`, project.Name, userContext, project.PDFContent, userMessage)

    resp, err := model.GenerateContent(ctx, genai.Text(prompt))
    if err != nil {
        return "", 0, 0, fmt.Errorf("failed to generate content: %v", err)
    }

    if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
        response := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])

        // Estimate token usage (approximate values since Gemini API doesn't return exact counts)
        inputTokens := estimateTokens(prompt)
        outputTokens := estimateTokens(response)

        // Update usage with tokens
        go updateProjectUsageWithTokens(project.ID, inputTokens, outputTokens, modelName)

        return response, inputTokens, outputTokens, nil
    }

    return "", 0, 0, fmt.Errorf("no response generated")
}

// ===== CHAT HISTORY AND ANALYTICS =====

// GetChatHistory - Retrieve chat history with enhanced filtering
func GetChatHistory(c *gin.Context) {
    projectID := c.Param("id")
    sessionID := c.Query("session_id")
    limit := c.DefaultQuery("limit", "50")
    page := c.DefaultQuery("page", "1")

    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }

    filter := bson.M{"project_id": objID}
    if sessionID != "" {
        filter["session_id"] = sessionID
    }

    // Pagination options
    opts := options.Find().
        SetSort(bson.D{{"timestamp", -1}}).
        SetLimit(50) // Max 50 messages per request

    collection := config.DB.Collection("chat_messages")
    cursor, err := collection.Find(context.Background(), filter, opts)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch chat history"})
        return
    }
    defer cursor.Close(context.Background())

    var messages []models.ChatMessage
    if err := cursor.All(context.Background(), &messages); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse chat history"})
        return
    }

    // Get total count
    totalCount, _ := collection.CountDocuments(context.Background(), filter)

    c.JSON(http.StatusOK, gin.H{
        "messages":    messages,
        "count":       len(messages),
        "total_count": totalCount,
        "page":        page,
        "limit":       limit,
    })
}

// GetChatAnalytics - Get chat analytics for a project
func GetChatAnalytics(c *gin.Context) {
    projectID := c.Param("id")
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }

    collection := config.DB.Collection("chat_messages")

    // Get total messages count
    totalMessages, _ := collection.CountDocuments(context.Background(), bson.M{"project_id": objID})

    // Get messages from last 7 days
    weekAgo := time.Now().AddDate(0, 0, -7)
    recentMessages, _ := collection.CountDocuments(context.Background(), bson.M{
        "project_id": objID,
        "timestamp":  bson.M{"$gte": weekAgo},
    })

    // Get unique sessions
    pipeline := []bson.M{
        {"$match": bson.M{"project_id": objID}},
        {"$group": bson.M{"_id": "$session_id"}},
        {"$count": "unique_sessions"},
    }

    cursor, _ := collection.Aggregate(context.Background(), pipeline)
    var result []bson.M
    cursor.All(context.Background(), &result)

    uniqueSessions := int64(0)
    if len(result) > 0 {
        if count, ok := result[0]["unique_sessions"].(int32); ok {
            uniqueSessions = int64(count)
        }
    }

    c.JSON(http.StatusOK, gin.H{
        "total_messages":  totalMessages,
        "recent_messages": recentMessages,
        "unique_sessions": uniqueSessions,
        "period":          "last_7_days",
    })
}

// RateMessage - Allow users to rate responses
func RateMessage(c *gin.Context) {
    messageID := c.Param("messageId")
    objID, err := primitive.ObjectIDFromHex(messageID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid message ID"})
        return
    }

    var rating struct {
        Rating   int    `json:"rating"` // 1-5 stars
        Feedback string `json:"feedback"`
    }

    if err := c.ShouldBindJSON(&rating); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid rating data"})
        return
    }

    if rating.Rating < 1 || rating.Rating > 5 {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Rating must be between 1 and 5"})
        return
    }

    // Update message with rating
    collection := config.DB.Collection("chat_messages")
    _, err = collection.UpdateOne(
        context.Background(),
        bson.M{"_id": objID},
        bson.M{"$set": bson.M{
            "rating":   rating.Rating,
            "feedback": rating.Feedback,
            "rated_at": time.Now(),
        }},
    )

    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save rating"})
        return
    }

    c.JSON(http.StatusOK, gin.H{"message": "Rating saved successfully"})
}

// ===== DEBUG FUNCTIONS =====

// DebugProjectPDFContent - Debug function to check PDF content
func DebugProjectPDFContent(c *gin.Context) {
    projectID := c.Param("projectId")
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
        "project_id":          projectID,
        "project_name":        project.Name,
        "pdf_files_count":     len(project.PDFFiles),
        "pdf_content_length":  len(project.PDFContent),
        "pdf_content_preview": project.PDFContent[:min(500, len(project.PDFContent))],
        "gemini_enabled":      project.GeminiEnabled,
        "gemini_model":        project.GeminiModel,
        "usage_info": gin.H{
            "daily_usage":   project.GeminiUsageToday,
            "daily_limit":   project.GeminiDailyLimit,
            "monthly_usage": project.GeminiUsageMonth,
            "monthly_limit": project.GeminiMonthlyLimit,
        },
    })
}

// ===== UTILITY FUNCTIONS =====

// generateSessionID - Generate unique session ID
func generateSessionID() string {
    return fmt.Sprintf("session_%d_%s", time.Now().Unix(), primitive.NewObjectID().Hex()[:8])
}

// isFirstMessage returns true the very first time a given session_id is seen for the project
func isFirstMessage(projectID primitive.ObjectID, sessionID string) bool {
    count, _ := config.DB.Collection("chat_messages").
        CountDocuments(context.Background(), bson.M{
            "project_id": projectID,
            "session_id": sessionID,
        })
    return count == 0
}




// saveMessage - Save chat message with user context
func saveMessage(projectID primitive.ObjectID, message, response, sessionID, userIP string, user models.ChatUser) {
    chatMessage := models.ChatMessage{
        ProjectID: projectID,
        SessionID: sessionID,
        Message:   message,
        Response:  response,
        IsUser:    false,
        Timestamp: time.Now(),
        IPAddress: userIP,
    }

    // Add user info if available
    if user.ID != primitive.NilObjectID {
        chatMessage.UserID = user.ID
        chatMessage.UserName = user.Name
        chatMessage.UserEmail = user.Email
    }

    chatCollection := config.DB.Collection("chat_messages")
    _, err := chatCollection.InsertOne(context.Background(), chatMessage)
    if err != nil {
        fmt.Printf("Failed to save chat message: %v\n", err)
    }
}

// updateProjectUsage - Update usage counters
func updateProjectUsage(projectID primitive.ObjectID) {
    collection := config.DB.Collection("projects")

    now := time.Now()
    update := bson.M{
        "$inc": bson.M{
            "gemini_usage":       1,
            "gemini_usage_today": 1,
            "gemini_usage_month": 1,
            "total_questions":    1,
        },
        "$set": bson.M{
            "last_used": now,
        },
    }

    _, err := collection.UpdateOne(context.Background(), bson.M{"_id": projectID}, update)
    if err != nil {
        fmt.Printf("Failed to update Gemini usage: %v\n", err)
    }
}

// updateProjectUsageWithTokens - Update usage with detailed token tracking
func updateProjectUsageWithTokens(projectID primitive.ObjectID, inputTokens, outputTokens int, model string) {
    collection := config.DB.Collection("projects")

    // Calculate cost
    cost := calculateGeminiCost(model, inputTokens, outputTokens)

    update := bson.M{
        "$inc": bson.M{
            "gemini_usage":         1,
            "gemini_usage_today":   1,
            "gemini_usage_month":   1,
            "total_questions":      1,
            "estimated_cost_today": cost,
            "estimated_cost_month": cost,
        },
        "$set": bson.M{
            "last_used": time.Now(),
        },
    }

    _, err := collection.UpdateOne(context.Background(), bson.M{"_id": projectID}, update)
    if err != nil {
        fmt.Printf("Failed to update Gemini usage with tokens: %v\n", err)
    }
}

// logGeminiUsage - Enhanced usage logging with complete details
func logGeminiUsage(projectID primitive.ObjectID, question, response, userIP string, user models.ChatUser) {
    usageLog := models.GeminiUsageLog{
        ProjectID:    projectID,
        Question:     question,
        Response:     response,
        Timestamp:    time.Now(),
        UserIP:       userIP,
        Success:      true,
        ResponseTime: 4000, // 4 seconds (your fixed delay)
    }

    // Add user info if available
    if user.ID != primitive.NilObjectID {
        usageLog.UserID = user.ID
        usageLog.UserName = user.Name
    }

    // Estimate tokens and cost
    inputTokens := estimateTokens(question)
    outputTokens := estimateTokens(response)
    usageLog.InputTokens = inputTokens
    usageLog.OutputTokens = outputTokens
    usageLog.TokensUsed = inputTokens + outputTokens
    usageLog.EstimatedCost = calculateGeminiCost("gemini-1.5-flash", inputTokens, outputTokens)

    collection := config.DB.Collection("gemini_usage_logs")
    _, err := collection.InsertOne(context.Background(), usageLog)
    if err != nil {
        fmt.Printf("Failed to log Gemini usage: %v\n", err)
    }
}

// sanitizeInput - Clean and validate user input
func sanitizeInput(input string) string {
    // Remove HTML tags and trim whitespace
    cleaned := html.EscapeString(strings.TrimSpace(input))

    // Limit message length
    if len(cleaned) > 1000 {
        cleaned = cleaned[:1000]
    }

    return cleaned
}

// checkBasicRateLimit - Basic rate limiting to prevent spam (more relaxed)
func checkBasicRateLimit(userIP string) bool {
    rateLimitMutex.Lock()
    defer rateLimitMutex.Unlock()

    now := time.Now()
    windowStart := now.Add(-time.Minute) // 1-minute window

    // Clean old entries
    if timestamps, exists := rateLimitMap[userIP]; exists {
        var validTimestamps []time.Time
        for _, timestamp := range timestamps {
            if timestamp.After(windowStart) {
                validTimestamps = append(validTimestamps, timestamp)
            }
        }
        rateLimitMap[userIP] = validTimestamps
    }

    // More relaxed limit (30 requests per minute instead of 10)
    if len(rateLimitMap[userIP]) >= 30 {
        return false
    }

    // Add current request
    rateLimitMap[userIP] = append(rateLimitMap[userIP], now)
    return true
}

// validateUserToken - Validate user authentication token
func validateUserToken(token string) (string, error) {
    // Simple token validation - implement proper JWT validation in production
    if len(token) < 24 {
        return "", fmt.Errorf("invalid token")
    }

    // Extract user ID from token (first 24 characters should be ObjectID)
    userID := token[:24]

    // Validate if it's a valid ObjectID
    _, err := primitive.ObjectIDFromHex(userID)
    if err != nil {
        return "", fmt.Errorf("invalid user ID in token")
    }

    return userID, nil
}

// calculateGeminiCost - Cost calculation function
func calculateGeminiCost(model string, inputTokens, outputTokens int) float64 {
    var inputCostPer1K, outputCostPer1K float64

    switch model {
    case "gemini-1.5-flash":
        inputCostPer1K = 0.000075 // $0.075 per 1K input tokens
        outputCostPer1K = 0.0003  // $0.30 per 1K output tokens
    case "gemini-1.5-pro":
        inputCostPer1K = 0.00125 // $1.25 per 1K input tokens
        outputCostPer1K = 0.005  // $5.00 per 1K output tokens
    default:
        inputCostPer1K = 0.000075 // Default to Flash pricing
        outputCostPer1K = 0.0003
    }

    inputCost := (float64(inputTokens) / 1000.0) * inputCostPer1K
    outputCost := (float64(outputTokens) / 1000.0) * outputCostPer1K

    return math.Round((inputCost+outputCost)*100000) / 100000
}

// getNextDailyReset - Reset time helpers
func getNextDailyReset() string {
    tomorrow := time.Now().AddDate(0, 0, 1).Truncate(24 * time.Hour)
    return tomorrow.Format(time.RFC3339)
}

// getNextMonthlyReset - Monthly reset helper
func getNextMonthlyReset() string {
    now := time.Now()
    nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
    return nextMonth.Format(time.RFC3339)
}

// estimateTokens - Helper function to estimate token count
func estimateTokens(text string) int {
    // Rough estimation: 1 token ≈ 4 characters for English text
    return len(text) / 4
}

// min - Helper function for minimum of two integers
func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}

// max - Helper function for maximum of two integers
func max(a, b int) int {
    if a > b {
        return a
    }
    return b
}
