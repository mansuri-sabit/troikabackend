package handlers

import (
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/api/option"
	"html"
	"jevi-chat/config"
	"jevi-chat/models"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
	"regexp"
)

// ===== RATE LIMITING IMPLEMENTATION =====

type RateLimiter struct {
	visitors map[string]*Visitor
	mu       sync.RWMutex
	rate     time.Duration
	burst    int
}

type Visitor struct {
	lastSeen time.Time
	count    int
	window   time.Time
}

var (
	chatRateLimiter    *RateLimiter
	authRateLimiter    *RateLimiter
	generalRateLimiter *RateLimiter
)

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(rate time.Duration, burst int) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*Visitor),
		rate:     rate,
		burst:    burst,
	}

	go rl.cleanupVisitors()
	return rl
}

// Allow checks if the request is allowed
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	visitor, exists := rl.visitors[ip]
	if !exists {
		visitor = &Visitor{
			lastSeen: now,
			count:    1,
			window:   now.Truncate(rl.rate),
		}
		rl.visitors[ip] = visitor
		return true
	}

	currentWindow := now.Truncate(rl.rate)
	if visitor.window.Before(currentWindow) {
		visitor.count = 1
		visitor.window = currentWindow
		visitor.lastSeen = now
		return true
	}

	if visitor.count < rl.burst {
		visitor.count++
		visitor.lastSeen = now
		return true
	}

	return false
}

// GetRemainingRequests returns remaining requests in current window
func (rl *RateLimiter) GetRemainingRequests(ip string) int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	visitor, exists := rl.visitors[ip]
	if !exists {
		return rl.burst
	}

	now := time.Now()
	currentWindow := now.Truncate(rl.rate)

	if visitor.window.Before(currentWindow) {
		return rl.burst
	}

	remaining := rl.burst - visitor.count
	if remaining < 0 {
		return 0
	}
	return remaining
}

// cleanupVisitors removes old visitors
func (rl *RateLimiter) cleanupVisitors() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for ip, visitor := range rl.visitors {
				if visitor.lastSeen.Before(cutoff) {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// InitRateLimiters initializes rate limiters
func InitRateLimiters() {
	chatRateLimiter = NewRateLimiter(time.Minute, 100)    // Increased from 30
	authRateLimiter = NewRateLimiter(time.Minute, 50)     // Increased from 10
	generalRateLimiter = NewRateLimiter(time.Minute, 200) // Increased from 60
	log.Println("✅ Rate limiters initialized with enhanced limits")
}

// RateLimitMiddleware creates a rate limiting middleware
func RateLimitMiddleware(limiterType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		clientIP := c.ClientIP()

		if chatRateLimiter == nil {
			InitRateLimiters()
		}

		var allowed bool
		var remaining int

		switch limiterType {
		case "chat":
			allowed = chatRateLimiter.Allow(clientIP)
			remaining = chatRateLimiter.GetRemainingRequests(clientIP)
		case "auth":
			allowed = authRateLimiter.Allow(clientIP)
			remaining = authRateLimiter.GetRemainingRequests(clientIP)
		case "general":
			allowed = generalRateLimiter.Allow(clientIP)
			remaining = generalRateLimiter.GetRemainingRequests(clientIP)
		default:
			allowed = generalRateLimiter.Allow(clientIP)
			remaining = generalRateLimiter.GetRemainingRequests(clientIP)
		}

		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Minute).Unix()))

		if !allowed {
			c.Header("Retry-After", "60")
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "Rate limit exceeded",
				"message":     "Too many requests. Please wait before trying again.",
				"retry_after": 60,
				"remaining":   0,
				"limit_type":  limiterType,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// ===== MAIN CHAT HANDLERS =====

func SendMessage(c *gin.Context) {
	projectID := c.Param("id")
	clientIP := c.ClientIP()

	var messageData struct {
		Message   string `json:"message"`
		SessionID string `json:"session_id"`
	}

	if err := c.ShouldBindJSON(&messageData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid message data"})
		return
	}

	messageData.Message = sanitizeInput(messageData.Message)
	if messageData.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Message cannot be empty"})
		return
	}

	if !checkRateLimit(clientIP) {
		remaining := 0
		if chatRateLimiter != nil {
			remaining = chatRateLimiter.GetRemainingRequests(clientIP)
		}

		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Minute).Unix()))
		c.Header("Retry-After", "60")

		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":       "Rate limit exceeded",
			"message":     "Too many requests. Please wait before sending another message.",
			"retry_after": 60,
			"remaining":   remaining,
		})
		return
	}

	objID, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	// Use enhanced database config
	collection := getProjectsCollection()
	var project models.Project
	err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	if !project.IsActive {
		c.JSON(http.StatusForbidden, gin.H{"error": "Project is inactive"})
		return
	}

	var response string
	var err2 error

	if project.GeminiEnabled && project.GeminiUsage < project.GeminiLimit && project.GeminiAPIKey != "" {
		if isFirstMessage(objID, messageData.SessionID) {
			time.Sleep(4 * time.Second)
			response = project.WelcomeMessage
		} else {
			time.Sleep(4 * time.Second)
			response, err2 = generateAIResponse(
				messageData.Message,
				project.PDFContent,
				project.GeminiAPIKey,
				project.Name,
				project.GeminiModel,
			)
			if err2 != nil {
				response = fmt.Sprintf("I apologize, but I'm experiencing technical difficulties with my AI system. However, I received your message about %s and will help you as best I can. Please try rephrasing your question.", project.Name)
			} else {
				// Fixed: Use correct function signature
				go updateGeminiUsage(objID, 0, 0)
			}
		}
	} else {
		time.Sleep(4 * time.Second)
		if !project.GeminiEnabled {
			response = "AI responses are currently disabled for this project."
		} else if project.GeminiAPIKey == "" {
			response = "AI configuration is incomplete. Please contact the administrator."
		} else {
			response = "AI usage limit reached for this project. Please contact the administrator to increase the limit."
		}
	}

	chatMessage := models.ChatMessage{
		ProjectID: objID,
		SessionID: messageData.SessionID,
		Message:   messageData.Message,
		Response:  response,
		IsUser:    false,
		Timestamp: time.Now(),
		IPAddress: clientIP,
	}

	chatCollection := getChatMessagesCollection()
	result, err := chatCollection.InsertOne(context.Background(), chatMessage)
	if err != nil {
		log.Printf("Failed to save chat message: %v", err)
	} else {
		chatMessage.ID = result.InsertedID.(primitive.ObjectID)
	}

	if chatRateLimiter != nil {
		remaining := chatRateLimiter.GetRemainingRequests(clientIP)
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Minute).Unix()))
	}

	c.JSON(http.StatusOK, gin.H{
		"response":   response,
		"message_id": chatMessage.ID,
		"timestamp":  chatMessage.Timestamp,
		"session_id": messageData.SessionID,
		"usage_info": gin.H{
			"current_usage": project.GeminiUsage + 1,
			"limit":         project.GeminiLimit,
			"remaining":     max64(0, int64(project.GeminiLimit)-int64(project.GeminiUsage)-1),
		},
	})
}

// IframeSendMessage - For embed widget users with enhanced features
func IframeSendMessage(c *gin.Context) {
	projectID := c.Param("projectId")
	startTime := time.Now()
	clientIP := c.ClientIP()

	log.Printf("📨 Received message request - Project: %s, IP: %s", projectID, clientIP)

	objID, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		log.Printf("❌ Invalid project ID: %s", projectID)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "Invalid project ID format",
			"status": "invalid_project_id",
		})
		return
	}

	var messageData struct {
		Message   string `json:"message"`
		SessionID string `json:"session_id"`
		UserToken string `json:"user_token"`
	}

	if err := c.ShouldBindJSON(&messageData); err != nil {
		log.Printf("❌ JSON binding error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "Invalid message data format",
			"status": "invalid_request",
		})
		return
	}

	log.Printf("📨 Message data: Session=%s, Message length=%d", messageData.SessionID, len(messageData.Message))

	messageData.Message = sanitizeInput(messageData.Message)
	if messageData.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "Message cannot be empty after sanitization",
			"status": "empty_message",
		})
		return
	}

	if !checkRateLimit(clientIP) {
		remaining := 0
		if chatRateLimiter != nil {
			remaining = chatRateLimiter.GetRemainingRequests(clientIP)
		}

		log.Printf("❌ Rate limit exceeded for IP: %s, Remaining: %d", clientIP, remaining)

		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Minute).Unix()))
		c.Header("Retry-After", "60")

		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":       "Rate limit exceeded",
			"message":     "Please wait before sending another message",
			"retry_after": 60,
			"remaining":   remaining,
			"status":      "rate_limited",
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	collection := getProjectsCollection()
	var project models.Project
	err = collection.FindOne(ctx, bson.M{"_id": objID}).Decode(&project)
	if err != nil {
		log.Printf("❌ Project not found: %s, Error: %v", projectID, err)
		c.JSON(http.StatusNotFound, gin.H{
			"error":  "Project not found",
			"status": "project_not_found",
		})
		return
	}

	log.Printf("✅ Project found: %s (Active: %v, Gemini: %v)", project.Name, project.IsActive, project.GeminiEnabled)

	if !project.IsActive {
		log.Printf("❌ Project inactive: %s", projectID)
		c.JSON(http.StatusForbidden, gin.H{
			"error":  "This chat is currently unavailable",
			"status": "project_inactive",
		})
		return
	}

	if !project.GeminiEnabled {
		log.Printf("❌ Gemini disabled for project: %s", projectID)
		c.JSON(http.StatusForbidden, gin.H{
			"error":  "AI responses are currently disabled for this project",
			"status": "gemini_disabled",
		})
		return
	}

	// Fixed: Handle zero daily limits properly
	if project.GeminiDailyLimit == 0 {
		log.Printf("⚠️ Project %s has daily limit set to 0, setting default limit", projectID)
		project.GeminiDailyLimit = 100

		go func() {
			updateCtx, updateCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer updateCancel()
			collection.UpdateOne(updateCtx, bson.M{"_id": objID}, bson.M{
				"$set": bson.M{"gemini_daily_limit": 100},
			})
		}()
	}

	if project.GeminiUsageToday >= project.GeminiDailyLimit {
		log.Printf("❌ Daily limit exceeded for project: %s (%d/%d)", projectID, project.GeminiUsageToday, project.GeminiDailyLimit)
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

	if project.GeminiMonthlyLimit == 0 {
		log.Printf("⚠️ Project %s has monthly limit set to 0, setting default limit", projectID)
		project.GeminiMonthlyLimit = 3000

		go func() {
			updateCtx, updateCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer updateCancel()
			collection.UpdateOne(updateCtx, bson.M{"_id": objID}, bson.M{
				"$set": bson.M{"gemini_monthly_limit": 3000},
			})
		}()
	}

	if project.GeminiUsageMonth >= project.GeminiMonthlyLimit {
		log.Printf("❌ Monthly limit exceeded for project: %s (%d/%d)", projectID, project.GeminiUsageMonth, project.GeminiMonthlyLimit)
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

	var user models.ChatUser
	if messageData.UserToken != "" {
		userID, err := validateUserToken(messageData.UserToken)
		if err == nil {
			userCollection := getChatUsersCollection()
			userObjID, _ := primitive.ObjectIDFromHex(userID)
			userCollection.FindOne(ctx, bson.M{"_id": userObjID}).Decode(&user)
			log.Printf("👤 User identified: %s (%s)", user.Name, user.Email)
		} else {
			log.Printf("⚠️ Invalid user token: %v", err)
		}
	}

	var response string
	var inputTokens, outputTokens int
	var success bool = true
	var errorMsg string

	if project.GeminiAPIKey == "" {
		log.Printf("❌ No Gemini API key configured for project: %s", projectID)
		success = false
		errorMsg = "No API key configured"
		response = "AI configuration is incomplete. Please contact support."
	} else {
		if isFirstMessage(objID, messageData.SessionID) {
			log.Printf("👋 First message for session: %s", messageData.SessionID)
			time.Sleep(3 * time.Second)

			response = project.WelcomeMessage
			if response == "" {
				response = "Hello! How can I help you today?"
			}
		} else {
			log.Printf("🤖 Generating AI response for message: %s", messageData.Message[:min(50, len(messageData.Message))])
			time.Sleep(4 * time.Second)

			response, inputTokens, outputTokens, err = generateGeminiResponseWithTracking(
				project, messageData.Message, clientIP, user)
			if err != nil {
				log.Printf("❌ AI response generation failed: %v", err)
				success = false
				errorMsg = err.Error()

				if user.Name != "" {
					response = fmt.Sprintf("Hello %s! I'm having trouble answering just now. Please try again later.", user.Name)
				} else {
					response = "I'm having trouble answering just now. Please try again later."
				}
			} else {
				log.Printf("✅ AI response generated successfully (Tokens: %d input, %d output)", inputTokens, outputTokens)
				go updateGeminiUsage(objID, inputTokens, outputTokens)
			}
		}
	}

	responseTime := time.Since(startTime).Milliseconds()
	log.Printf("⏱️ Response time: %dms", responseTime)

	go saveMessage(objID, messageData.Message, response, messageData.SessionID, clientIP, user)

	if chatRateLimiter != nil {
		remaining := chatRateLimiter.GetRemainingRequests(clientIP)
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		c.Header("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Minute).Unix()))
	}

	dailyUsageAfter := int64(project.GeminiUsageToday)
	monthlyUsageAfter := int64(project.GeminiUsageMonth)

	if success && !isFirstMessage(objID, messageData.SessionID) {
		dailyUsageAfter++
		monthlyUsageAfter++
	}

	responseData := gin.H{
		"response":   response,
		"project_id": projectID,
		"status":     "success",
		"timestamp":  time.Now().Format(time.RFC3339),
		"session_id": messageData.SessionID,
		"usage_info": gin.H{
			"daily_usage":     dailyUsageAfter,
			"daily_limit":     int64(project.GeminiDailyLimit),
			"daily_remaining": max64(0, int64(project.GeminiDailyLimit)-dailyUsageAfter),
			"monthly_usage":   monthlyUsageAfter,
			"monthly_limit":   int64(project.GeminiMonthlyLimit),
			"response_time":   responseTime,
			"tokens_used":     inputTokens + outputTokens,
		},
	}

	if user.Name != "" {
		responseData["user_name"] = user.Name
	}

	if !success {
		responseData["status"] = "error"
		responseData["error_details"] = errorMsg
	}

	log.Printf("✅ Response sent successfully for project: %s", projectID)
	c.JSON(http.StatusOK, responseData)
}

// ===== AI RESPONSE GENERATION =====

// Utility: Trim response to 3 sentences max
func limitToSentences(text string, max int) string {
	re := regexp.MustCompile(`[^.?!]+[.?!]`)
	sentences := re.FindAllString(text, -1)
	if len(sentences) <= max {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(strings.Join(sentences[:max], " "))
}


func generateAIResponse(userMessage, pdfContent, geminiKey, projectName, geminiModel string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := genai.NewClient(ctx, option.WithAPIKey(geminiKey))
	if err != nil {
		return "", fmt.Errorf("failed to create Gemini client: %v", err)
	}
	defer client.Close()

	if geminiModel == "" {
		geminiModel = "gemini-1.5-flash"
	}

	model := client.GenerativeModel(geminiModel)
	model.SetTemperature(0.85)
	model.SetTopP(0.9)
	model.SetTopK(40)

	uniqueTag := fmt.Sprintf("<!-- %d -->", time.Now().UnixNano()%1000)

	prompt := fmt.Sprintf(`
You are a professional AI assistant for %s. 
You speak in a confident, clear, and natural tone — like a knowledgeable human expert.

Your job is to answer user questions strictly based on the KNOWLEDGE BASE below, 
but without explicitly mentioning the source or saying things like "the document says".

KNOWLEDGE BASE:
%s

USER QUESTION:
%s

STRICT GUIDELINES:
– You MUST limit your answer to a MAXIMUM of 3 full sentences. Do NOT exceed this under any condition.  
– NEVER include email addresses, phone numbers, or URLs in the answer unless user asks explicitly.  
– Speak directly to the user, like a helpful expert.  
– Do NOT say things like "according to the document" or "based on the file".  
– If no answer is found, say so clearly and offer general guidance.  
– Vary your tone and sentence structure — avoid repeating phrases.  
– End naturally, without filler.

%s
Answer:`, projectName, pdfContent, userMessage, uniqueTag)

	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", fmt.Errorf("failed to generate content: %v", err)
	}

	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		full := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])
		short := limitToSentences(full, 3)
		return short, nil
	}

	return "I'm sorry, I couldn't generate a response at the moment. Please try again.", nil
}


// Enhanced generateGeminiResponseWithTracking function
func generateGeminiResponseWithTracking(project models.Project, userMessage, userIP string, user models.ChatUser) (string, int, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := genai.NewClient(ctx, option.WithAPIKey(project.GeminiAPIKey))
	if err != nil {
		return "", 0, 0, fmt.Errorf("failed to create Gemini client: %v", err)
	}
	defer client.Close()

	modelName := project.GeminiModel
	if modelName == "" {
		modelName = "gemini-1.5-flash"
	}

	model := client.GenerativeModel(modelName)
	model.SetTemperature(0.7) // Slightly lower for more focused responses
	model.SetTopP(0.9)
	model.SetTopK(40)

	// Process PDF content for better AI understanding
	processedPDFContent := ProcessPDFForAI(project.PDFContent)

	// Enhanced prompt with explicit instructions for PDF usage
	userContext := ""
	if user.Name != "" {
		userContext = fmt.Sprintf("The user's name is %s. ", user.Name)
	}

	prompt := fmt.Sprintf(`You are an AI assistant for %s. %s

IMPORTANT INSTRUCTIONS:
1. You MUST base your answers primarily on the provided document content below
2. If the document contains relevant information, use it as your primary source
3. Quote specific sections from the document when applicable
4. If the document doesn't contain the answer, clearly state this and provide general guidance
5. Always prioritize document content over general knowledge

DOCUMENT CONTENT:
%s

USER QUESTION:
%s

RESPONSE GUIDELINES:
- Start by checking if the document contains information relevant to the user's question
- If found, provide a detailed answer based on the document content
- Include specific quotes or references from the document when helpful
- If the document doesn't contain the answer, say: "Based on the provided document, I don't have specific information about [topic]. However, I can provide general guidance..."
- Keep responses conversational but informative
- Use bullet points or numbered lists for complex information
- End with an offer to help with related questions

Answer:`, project.Name, userContext, processedPDFContent, userMessage)

	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		return "", 0, 0, fmt.Errorf("failed to generate content: %v", err)
	}

	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		response := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])

		inputTokens := estimateTokens(prompt)
		outputTokens := estimateTokens(response)

		return response, inputTokens, outputTokens, nil
	}

	return "", 0, 0, fmt.Errorf("no response generated")
}

// ===== UTILITY FUNCTIONS =====

// Helper functions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Database collection helpers
func getProjectsCollection() *mongo.Collection {
	if config.DB != nil {
		return config.DB.Collection("projects")
	}
	return config.GetCollection("projects")
}

func getChatMessagesCollection() *mongo.Collection {
	if config.DB != nil {
		return config.DB.Collection("chat_messages")
	}
	return config.GetCollection("chat_messages")
}

func getChatUsersCollection() *mongo.Collection {
	if config.DB != nil {
		return config.DB.Collection("chat_users")
	}
	return config.GetCollection("chat_users")
}

// Fixed: Single updateGeminiUsage function with correct signature
func updateGeminiUsage(projectID primitive.ObjectID, inputTokens, outputTokens int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	collection := getProjectsCollection()

	cost := calculateGeminiCost("gemini-1.5-flash", inputTokens, outputTokens)

	_, err := collection.UpdateOne(
		ctx,
		bson.M{"_id": projectID},
		bson.M{
			"$inc": bson.M{
				"gemini_usage":       1,
				"gemini_usage_today": 1,
				"gemini_usage_month": 1,
				"total_questions":    1,
				"total_tokens_used":  inputTokens + outputTokens,
				"total_cost":         cost,
			},
			"$set": bson.M{
				"last_used": time.Now(),
			},
		},
	)
	if err != nil {
		log.Printf("❌ Failed to update Gemini usage: %v", err)
	} else {
		log.Printf("📊 Usage updated for project: %s", projectID.Hex())
	}
}

func isFirstMessage(projectID primitive.ObjectID, sessionID string) bool {
	count, _ := getChatMessagesCollection().
		CountDocuments(context.Background(), bson.M{
			"project_id": projectID,
			"session_id": sessionID,
		})
	return count == 0
}

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

	if user.ID != primitive.NilObjectID {
		chatMessage.UserID = user.ID
		chatMessage.UserName = user.Name
		chatMessage.UserEmail = user.Email
	}

	chatCollection := getChatMessagesCollection()
	_, err := chatCollection.InsertOne(context.Background(), chatMessage)
	if err != nil {
		log.Printf("Failed to save chat message: %v", err)
	}
}

func sanitizeInput(input string) string {
	cleaned := html.EscapeString(strings.TrimSpace(input))
	if len(cleaned) > 1000 {
		cleaned = cleaned[:1000]
	}
	return cleaned
}

func checkRateLimit(userIP string) bool {
	if chatRateLimiter == nil {
		InitRateLimiters()
	}
	return chatRateLimiter.Allow(userIP)
}

func validateUserToken(token string) (string, error) {
	if len(token) < 24 {
		return "", fmt.Errorf("invalid token")
	}

	userID := token[:24]

	_, err := primitive.ObjectIDFromHex(userID)
	if err != nil {
		return "", fmt.Errorf("invalid user ID in token")
	}

	return userID, nil
}

func calculateGeminiCost(model string, inputTokens, outputTokens int) float64 {
	var inputCostPer1K, outputCostPer1K float64

	switch model {
	case "gemini-1.5-flash":
		inputCostPer1K = 0.000075
		outputCostPer1K = 0.0003
	case "gemini-1.5-pro":
		inputCostPer1K = 0.00125
		outputCostPer1K = 0.005
	default:
		inputCostPer1K = 0.000075
		outputCostPer1K = 0.0003
	}

	inputCost := (float64(inputTokens) / 1000.0) * inputCostPer1K
	outputCost := (float64(outputTokens) / 1000.0) * outputCostPer1K

	return math.Round((inputCost+outputCost)*100000) / 100000
}

func getNextDailyReset() string {
	tomorrow := time.Now().AddDate(0, 0, 1).Truncate(24 * time.Hour)
	return tomorrow.Format(time.RFC3339)
}

func getNextMonthlyReset() string {
	now := time.Now()
	nextMonth := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
	return nextMonth.Format(time.RFC3339)
}

func estimateTokens(text string) int {
	return len(text) / 4
}

// Additional handlers for completeness
func GetChatHistory(c *gin.Context) {
	projectID := c.Param("id")
	sessionID := c.Query("session_id")

	objID, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	filter := bson.M{"project_id": objID}
	if sessionID != "" {
		filter["session_id"] = sessionID
	}

	opts := options.Find().
		SetSort(bson.D{{"timestamp", -1}}).
		SetLimit(50)

	collection := getChatMessagesCollection()
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

	totalCount, _ := collection.CountDocuments(context.Background(), filter)

	c.JSON(http.StatusOK, gin.H{
		"messages":    messages,
		"count":       len(messages),
		"total_count": totalCount,
	})
}

func RateMessage(c *gin.Context) {
	messageID := c.Param("messageId")
	objID, err := primitive.ObjectIDFromHex(messageID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid message ID"})
		return
	}

	var rating struct {
		Rating   int    `json:"rating"`
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

	collection := getChatMessagesCollection()
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

// CORS Debug Middleware
func CORSDebugMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if gin.Mode() == gin.DebugMode {
			origin := c.Request.Header.Get("Origin")
			method := c.Request.Method

			log.Printf("🔍 CORS Debug - Origin: %s, Method: %s, Path: %s",
				origin, method, c.Request.URL.Path)

			corsHeaders := []string{
				"Origin",
				"Access-Control-Request-Method",
				"Access-Control-Request-Headers",
				"Referer",
			}

			for _, header := range corsHeaders {
				if value := c.Request.Header.Get(header); value != "" {
					log.Printf("  %s: %s", header, value)
				}
			}
		}

		c.Next()
	}
}

// Update Project Limits
func UpdateProjectLimits(c *gin.Context) {
	projectID := c.Param("id")

	var limitData struct {
		DailyLimit   int64 `json:"daily_limit"`
		MonthlyLimit int64 `json:"monthly_limit"`
	}

	if err := c.ShouldBindJSON(&limitData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid limit data"})
		return
	}

	if limitData.DailyLimit < 0 || limitData.MonthlyLimit < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Limits cannot be negative"})
		return
	}

	objID, err := primitive.ObjectIDFromHex(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
		return
	}

	collection := getProjectsCollection()
	_, err = collection.UpdateOne(
		context.Background(),
		bson.M{"_id": objID},
		bson.M{"$set": bson.M{
			"gemini_daily_limit":   limitData.DailyLimit,
			"gemini_monthly_limit": limitData.MonthlyLimit,
			"updated_at":           time.Now(),
		}},
	)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update limits"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "Project limits updated successfully",
		"daily_limit":   limitData.DailyLimit,
		"monthly_limit": limitData.MonthlyLimit,
	})
}
