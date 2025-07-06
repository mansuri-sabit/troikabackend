package config

import (
    "context"
    "fmt"
    "log"
    "os"
    "time"
    "regexp"
    "github.com/google/generative-ai-go/genai"
    "google.golang.org/api/option"
)

var GeminiClient *genai.Client

// Initialize Gemini client (call this once in main or init)
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
    log.Println("✅ Gemini client initialized successfully")
}

// Generates a polished, human-like response
func GenerateResponse(userPrompt string, pdfContext string) (string, error) {
    ctx := context.Background()
    model := GeminiClient.GenerativeModel("gemini-1.5-flash")

    // Add randomness to avoid caching/repetition
    noise := fmt.Sprintf("<!-- %d -->", time.Now().UnixNano()%1000)

    // Final prompt construction
fullPrompt := fmt.Sprintf(`
You are a helpful and knowledgeable assistant.

Here is some background information to help answer the user's question:
%s

Now, respond to the user's question naturally and professionally without referencing the background or its source.

➡️ Limit your answer to 2–3 concise, informative sentences unless more is absolutely required.

User question: %s

%s
`, pdfContext, userPrompt, noise)


    // Generate content using Gemini
    resp, err := model.GenerateContent(ctx, genai.Text(fullPrompt))
    if err != nil {
        log.Printf("❌ Gemini content generation failed: %v", err)
        return "", fmt.Errorf("failed to generate content: %v", err)
    }

    if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
        text := string(resp.Candidates[0].Content.Parts[0].(genai.Text))

        // Optional: clean robotic endings if any
        cleaned := cleanResponse(text)
        return cleaned, nil
    }

    return "No response generated", nil
}

// Optional: clean common robotic phrases
func cleanResponse(raw string) string {
    cleaned := raw

    // Remove robotic starter phrases
    cleaned = removeFirstMatch(cleaned, `(?i)based on the .*?(document|pdf)[,:]?`)

    // Remove generic bot endings
    cleaned = removeFirstMatch(cleaned, `(?i)is there anything else.*?\?$`)

    return cleaned
}

// Helper: simple regex match remover
func removeFirstMatch(input string, pattern string) string {
    re := regexp.MustCompile(pattern)
    return re.ReplaceAllString(input, "")
}
