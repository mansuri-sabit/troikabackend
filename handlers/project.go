package handlers

import (
    "context"
    "fmt"
    "net/http"
    "os"
    "path/filepath"
    "strings"
    "time"
    "log"
    "github.com/gin-gonic/gin"
    "go.mongodb.org/mongo-driver/bson"
    "go.mongodb.org/mongo-driver/bson/primitive"
    "github.com/google/generative-ai-go/genai"
    "google.golang.org/api/option"
    "jevi-chat/config"
    "jevi-chat/models"
)

// ===== PDF MANAGEMENT =====

// UploadPDF - Enhanced PDF upload with multiple file support
func UploadPDF(c *gin.Context) {
    startTime := time.Now() // Add timing
    projectID := c.Param("id")
    
    // ✅ FIXED: Remove duplicate log
    log.Printf("🔍 [DEBUG] Starting PDF upload for project: %s", projectID)
    log.Printf("🔍 [DEBUG] Request method: %s", c.Request.Method)
    log.Printf("🔍 [DEBUG] Content-Type: %s", c.Request.Header.Get("Content-Type"))
    
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        log.Printf("❌ Invalid project ID: %s", projectID)
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }

    // Get project to check if it exists
    collection := config.DB.Collection("projects")
    var project models.Project
    err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
    if err != nil {
        log.Printf("❌ Project not found: %s", projectID)
        c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
        return
    }

    log.Printf("✅ Project found: %s", project.Name)

    // Handle multiple file upload
    form, err := c.MultipartForm()
    if err != nil {
        log.Printf("❌ Failed to parse multipart form: %v", err)
        c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse form", "details": err.Error()})
        return
    }

    // ✅ CRITICAL FIX: Use "files" to match React frontend
    files := form.File["files"]
    log.Printf("📄 Received %d files for processing", len(files))
    
    if len(files) == 0 {
        log.Printf("❌ No files found in form data")
        
        // ✅ ENHANCED: Better debug information
        var availableFields []string
        for fieldName, fieldFiles := range form.File {
            log.Printf("Available form field: %s with %d files", fieldName, len(fieldFiles))
            availableFields = append(availableFields, fieldName)
        }
        
        c.JSON(http.StatusBadRequest, gin.H{
            "error": "No files uploaded",
            "debug_info": map[string]interface{}{
                "available_fields": availableFields,
                "expected_field":   "files",
            },
        })
        return
    }

    var uploadedFiles []models.PDFFile
    var allContent strings.Builder

    // Create uploads directory if it doesn't exist
    uploadDir := "./static/uploads"
    if err := os.MkdirAll(uploadDir, 0755); err != nil {
        log.Printf("❌ Failed to create upload directory: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create upload directory"})
        return
    }

    for i, file := range files {
        log.Printf("📄 Processing file %d/%d: %s", i+1, len(files), file.Filename)
        
        // Validate file type and size
        if !strings.HasSuffix(strings.ToLower(file.Filename), ".pdf") {
            log.Printf("⚠️ Skipping non-PDF file: %s", file.Filename)
            continue
        }
        if file.Size > 10*1024*1024 { // 10MB limit
            log.Printf("⚠️ Skipping oversized file: %s (size: %d bytes)", file.Filename, file.Size)
            continue
        }

        // ✅ ENHANCED: Safer filename generation
        fileID := primitive.NewObjectID().Hex()
        fileName := fmt.Sprintf("%s_%s", fileID, filepath.Base(file.Filename))
        filePath := filepath.Join(uploadDir, fileName)

        // Save file
        if err := c.SaveUploadedFile(file, filePath); err != nil {
            log.Printf("❌ Failed to save file %s: %v", file.Filename, err)
            continue
        }

        log.Printf("✅ File saved: %s", filePath)

        pdfFile := models.PDFFile{
            ID:         fileID,
            FileName:   file.Filename,
            FilePath:   filePath,
            FileSize:   file.Size,
            UploadedAt: time.Now(),
            Status:     "processing",
        }

        // Process with Gemini if enabled
        var content string
        if project.GeminiEnabled && project.GeminiAPIKey != "" {
            log.Printf("🤖 Processing PDF with Gemini: %s", file.Filename)
            content, err = processPDFWithGemini(filePath, project.GeminiAPIKey)
            if err == nil {
                pdfFile.ProcessedAt = time.Now()
                pdfFile.Status = "completed"
                log.Printf("✅ Gemini processing completed for: %s", file.Filename)
            } else {
                log.Printf("❌ Gemini processing failed for %s: %v", file.Filename, err)
                pdfFile.Status = "failed"
                content = "Failed to process PDF content"
            }
        } else {
            content = "PDF uploaded successfully (Gemini processing disabled)"
            pdfFile.Status = "completed"
        }

        uploadedFiles = append(uploadedFiles, pdfFile)
        allContent.WriteString(content + "\n\n")
    }

    if len(uploadedFiles) == 0 {
        log.Printf("❌ No files were successfully processed")
        c.JSON(http.StatusBadRequest, gin.H{"error": "No files could be processed"})
        return
    }

    // Update project with PDF files and content
    update := bson.M{
        "$push": bson.M{"pdf_files": bson.M{"$each": uploadedFiles}},
        "$set": bson.M{
            "pdf_content": allContent.String(),
            "updated_at":  time.Now(),
        },
    }

    _, err = collection.UpdateOne(context.Background(), bson.M{"_id": objID}, update)
    if err != nil {
        log.Printf("❌ Failed to update project: %v", err)
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update project"})
        return
    }

    processingTime := time.Since(startTime)
    log.Printf("✅ Successfully processed %d files for project %s in %v", len(uploadedFiles), project.Name, processingTime)

    // ✅ ENHANCED: More detailed response
    c.JSON(http.StatusOK, gin.H{
        "message":          "PDFs uploaded and processed successfully",
        "files_uploaded":   len(uploadedFiles),
        "total_files":      len(files),
        "skipped_files":    len(files) - len(uploadedFiles),
        "files":           uploadedFiles,
        "processing_time":  processingTime.Milliseconds(),
    })
}



// processPDFWithGemini - Enhanced PDF processing with Gemini AI
func processPDFWithGemini(filePath, apiKey string) (string, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()
    
    // Create client with project-specific API key
    client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
    if err != nil {
        return "", fmt.Errorf("failed to create Gemini client: %v", err)
    }
    defer client.Close()
    
    // Upload file to Gemini
    file, err := client.UploadFileFromPath(ctx, filePath, nil)
    if err != nil {
        return "", fmt.Errorf("failed to upload file to Gemini: %v", err)
    }
    
    // Wait for file to be processed with timeout
    maxWaitTime := 30 * time.Second
    startTime := time.Now()
    
    for file.State == genai.FileStateProcessing {
        if time.Since(startTime) > maxWaitTime {
            return "", fmt.Errorf("file processing timeout")
        }
        
        time.Sleep(2 * time.Second)
        file, err = client.GetFile(ctx, file.Name)
        if err != nil {
            return "", fmt.Errorf("failed to check file status: %v", err)
        }
    }
    
    if file.State != genai.FileStateActive {
        return "", fmt.Errorf("file processing failed with state: %v", file.State)
    }
    
    // Process the PDF with enhanced prompt
    model := client.GenerativeModel("gemini-1.5-flash")
    resp, err := model.GenerateContent(ctx, 
        genai.FileData{URI: file.URI, MIMEType: file.MIMEType},
        genai.Text(`Extract and organize all information from this document in a structured format. 
        Include:
        1. Main topics and sections with clear headings
        2. Key points and important details
        3. Any procedures, steps, or instructions
        4. Important facts, figures, and data
        5. Contact information if present
        6. Definitions and terminology
        7. Tables and lists if any
        
        Format the content clearly with headings and bullet points where appropriate. 
        This will be used as a knowledge base for answering user questions.
        Make sure to preserve the logical structure and hierarchy of information.`),
    )
    
    if err != nil {
        return "", fmt.Errorf("failed to generate content: %v", err)
    }
    
    if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
        return string(resp.Candidates[0].Content.Parts[0].(genai.Text)), nil
    }
    
    return "", fmt.Errorf("no content generated from PDF")
}

// DeletePDF - Delete specific PDF file
func DeletePDF(c *gin.Context) {
    projectID := c.Param("id")
    fileID := c.Param("fileId")
    
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid project ID"})
        return
    }

    collection := config.DB.Collection("projects")
    
    // Get project to find file path for deletion
    var project models.Project
    err = collection.FindOne(context.Background(), bson.M{"_id": objID}).Decode(&project)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
        return
    }
    
    // Find and delete physical file
    var fileToDelete models.PDFFile
    for _, file := range project.PDFFiles {
        if file.ID == fileID {
            fileToDelete = file
            break
        }
    }
    
    if fileToDelete.FilePath != "" {
        os.Remove(fileToDelete.FilePath)
    }
    
    // Remove file from array
    update := bson.M{
        "$pull": bson.M{"pdf_files": bson.M{"id": fileID}},
        "$set":  bson.M{"updated_at": time.Now()},
    }

    _, err = collection.UpdateOne(context.Background(), bson.M{"_id": objID}, update)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete PDF"})
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "message": "PDF deleted successfully",
        "file_id": fileID,
    })
}

// GetPDFFiles - Get all PDF files for a project
func GetPDFFiles(c *gin.Context) {
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
        "project_id": projectID,
        "pdf_files":  project.PDFFiles,
        "total_files": len(project.PDFFiles),
    })
}

// ===== ANALYTICS =====

// ===== PROJECT DASHBOARD FUNCTIONS =====

// ProjectDashboard - Display project dashboard page
func ProjectDashboard(c *gin.Context) {
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
    
    // Get additional statistics
    chatCollection := config.DB.Collection("chat_messages")
    messageCount, _ := chatCollection.CountDocuments(context.Background(), bson.M{"project_id": objID})
    
    c.HTML(http.StatusOK, "project/dashboard.html", gin.H{
        "title":         "Project Dashboard - " + project.Name,
        "project":       project,
        "message_count": messageCount,
        "embed_url":     fmt.Sprintf("/embed/%s", projectID),
    })
}

// GetProjectInfo - Get project information for API calls
func GetProjectInfo(c *gin.Context) {
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

    // Get additional stats
    chatCollection := config.DB.Collection("chat_messages")
    messageCount, _ := chatCollection.CountDocuments(context.Background(), bson.M{"project_id": objID})
    
    // Get unique sessions count
    pipeline := []bson.M{
        {"$match": bson.M{"project_id": objID}},
        {"$group": bson.M{"_id": "$session_id"}},
        {"$count": "unique_sessions"},
    }
    
    cursor, _ := chatCollection.Aggregate(context.Background(), pipeline)
    var result []bson.M
    cursor.All(context.Background(), &result)
    
    uniqueSessions := int64(0)
    if len(result) > 0 {
        if count, ok := result[0]["unique_sessions"].(int32); ok {
            uniqueSessions = int64(count)
        }
    }

    c.JSON(http.StatusOK, gin.H{
        "project_id":      projectID,
        "project":         project,
        "message_count":   messageCount,
        "unique_sessions": uniqueSessions,
        "embed_url":       fmt.Sprintf("/embed/%s", projectID),
    })
}

// ===== USER PROJECT FUNCTIONS =====

// UserProjects - Get projects for regular users
func UserProjects(c *gin.Context) {
    // Get user projects (implement based on your auth system)
    collection := config.DB.Collection("projects")
    
    // For now, return all active projects
    // In production, filter by user permissions
    cursor, err := collection.Find(context.Background(), bson.M{"is_active": true})
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch projects"})
        return
    }

    var projects []models.Project
    if err := cursor.All(context.Background(), &projects); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse projects"})
        return
    }

    c.JSON(http.StatusOK, gin.H{
        "projects": projects,
        "count":    len(projects),
    })
}

// ===== HELPER FUNCTIONS =====

// getGeminiModel - Get Gemini model with fallback
func getGeminiModel(model string) string {
    if model == "" {
        return "gemini-1.5-flash"
    }
    
    // Validate model name
    validModels := []string{"gemini-1.5-flash", "gemini-1.5-pro", "gemini-pro"}
    for _, validModel := range validModels {
        if model == validModel {
            return model
        }
    }
    
    return "gemini-1.5-flash" // fallback
}

// getWelcomeMessage - Get welcome message with fallback
func getWelcomeMessage(message string) string {
    if message == "" {
        return "Hello! How can I help you today?"
    }
    return message
}

// validateFileType - Validate uploaded file type
func validateFileType(filename string) bool {
    allowedExtensions := []string{".pdf", ".doc", ".docx", ".txt"}
    ext := strings.ToLower(filepath.Ext(filename))
    
    for _, allowed := range allowedExtensions {
        if ext == allowed {
            return true
        }
    }
    return false
}

// formatFileSize - Format file size for display
func formatFileSize(bytes int64) string {
    const unit = 1024
    if bytes < unit {
        return fmt.Sprintf("%d B", bytes)
    }
    div, exp := int64(unit), 0
    for n := bytes / unit; n >= unit; n /= unit {
        div *= unit
        exp++
    }
    return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// Add this to your handlers/pdf.go or similar file
func ProcessPDFForAI(pdfContent string) string {
    // Clean and structure the PDF content for better AI understanding
    lines := strings.Split(pdfContent, "\n")
    var processedLines []string
    
    for _, line := range lines {
        // Remove excessive whitespace
        cleaned := strings.TrimSpace(line)
        if cleaned != "" {
            processedLines = append(processedLines, cleaned)
        }
    }
    
    // Join with proper spacing
    structured := strings.Join(processedLines, "\n")
    
    // Add section markers for better AI understanding
    structured = "=== DOCUMENT CONTENT START ===\n" + structured + "\n=== DOCUMENT CONTENT END ==="
    
    return structured
}

// Chunk large PDF content for better processing
func ChunkPDFContent(content string, maxChunkSize int) []string {
    if len(content) <= maxChunkSize {
        return []string{content}
    }
    
    var chunks []string
    words := strings.Fields(content)
    
    var currentChunk []string
    currentSize := 0
    
    for _, word := range words {
        wordSize := len(word) + 1 // +1 for space
        
        if currentSize + wordSize > maxChunkSize && len(currentChunk) > 0 {
            chunks = append(chunks, strings.Join(currentChunk, " "))
            currentChunk = []string{word}
            currentSize = wordSize
        } else {
            currentChunk = append(currentChunk, word)
            currentSize += wordSize
        }
    }
    
    if len(currentChunk) > 0 {
        chunks = append(chunks, strings.Join(currentChunk, " "))
    }
    
    return chunks
}


// Add this function to validate and enhance PDF content
func ValidateAndEnhancePDFContent(projectID primitive.ObjectID) error {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    collection := getProjectsCollection()
    var project models.Project
    
    err := collection.FindOne(ctx, bson.M{"_id": projectID}).Decode(&project)
    if err != nil {
        return fmt.Errorf("project not found: %v", err)
    }
    
    // Check if PDF content exists and is meaningful
    if project.PDFContent == "" {
        log.Printf("⚠️ Project %s has no PDF content", projectID.Hex())
        return fmt.Errorf("no PDF content available")
    }
    
    // Check content length
    contentLength := len(project.PDFContent)
    log.Printf("📄 PDF content length for project %s: %d characters", projectID.Hex(), contentLength)
    
    if contentLength < 100 {
        log.Printf("⚠️ PDF content seems too short for project %s", projectID.Hex())
        return fmt.Errorf("PDF content appears incomplete")
    }
    
    // Enhance content if needed
    if !strings.Contains(project.PDFContent, "===") {
        enhancedContent := ProcessPDFForAI(project.PDFContent)
        
        // Update the project with enhanced content
        _, err = collection.UpdateOne(ctx, bson.M{"_id": projectID}, bson.M{
            "$set": bson.M{
                "pdf_content": enhancedContent,
                "updated_at": time.Now(),
            },
        })
        if err != nil {
            log.Printf("❌ Failed to update enhanced PDF content: %v", err)
        } else {
            log.Printf("✅ Enhanced PDF content for project %s", projectID.Hex())
        }
    }
    
    return nil
}
