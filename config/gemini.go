package config

import (
    "context"
    "log"
    "os"
    
    "github.com/google/generative-ai-go/genai"
    "google.golang.org/api/option"
)

var GeminiClient *genai.Client

func InitGemini() {
    apiKey := os.Getenv("GEMINI_API_KEY")
    if apiKey == "" {
        log.Fatal("GEMINI_API_KEY not set in environment")
    }
    
    ctx := context.Background()
    client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
    if err != nil {
        log.Fatal("Failed to initialize Gemini client:", err)
    }
    
    GeminiClient = client
    log.Println("Gemini client initialized successfully")
}

func GenerateResponse(prompt string, pdfContext string) (string, error) {
    ctx := context.Background()
    model := GeminiClient.GenerativeModel("gemini-1.5-flash")
    
    // Combine PDF context with user prompt
    fullPrompt := prompt
    if pdfContext != "" {
        fullPrompt = "Based on the following document context:\n" + pdfContext + "\n\nUser question: " + prompt
    }
    
    resp, err := model.GenerateContent(ctx, genai.Text(fullPrompt))
    if err != nil {
        return "", err
    }
    
    if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
        return string(resp.Candidates[0].Content.Parts[0].(genai.Text)), nil
    }
    
    return "No response generated", nil
}
