package config

import (
    "context"
    "fmt"
    "log"
    "os"
    "strings"
    "time"
    "go.mongodb.org/mongo-driver/bson/primitive"
    "go.mongodb.org/mongo-driver/bson"
    "go.mongodb.org/mongo-driver/mongo"
    "go.mongodb.org/mongo-driver/mongo/options"
)

var (
    DB     *mongo.Database
    Client *mongo.Client
)

func InitMongoDB() {
    uri := os.Getenv("MONGODB_URI")
    if uri == "" {
        log.Fatal("❌ MONGODB_URI not set in environment")
    }
    
    // Log connection attempt (hide password for security)
    safeURI := hideSensitiveInfo(uri)
    log.Printf("🔗 Connecting to MongoDB: %s", safeURI)
    
    ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    
    // Enhanced client options
    clientOptions := options.Client().ApplyURI(uri)
    clientOptions.SetMaxPoolSize(10)
    clientOptions.SetMinPoolSize(1)
    clientOptions.SetMaxConnIdleTime(30 * time.Second)
    clientOptions.SetServerSelectionTimeout(10 * time.Second)
    
    client, err := mongo.Connect(ctx, clientOptions)
    if err != nil {
        log.Fatalf("❌ Failed to connect to MongoDB: %v", err)
    }
    
    // Test connection with retry logic
    if err := testConnection(ctx, client); err != nil {
        log.Fatalf("❌ Failed to establish MongoDB connection: %v", err)
    }
    
    // Get database name from environment or use default
    dbName := os.Getenv("MONGODB_DATABASE")
    if dbName == "" {
        dbName = "jevi_chat"
        log.Printf("⚠️ MONGODB_DATABASE not set, using default: %s", dbName)
    }
    
    Client = client
    DB = client.Database(dbName)
    
    log.Printf("✅ Connected to MongoDB successfully (Database: %s)", dbName)
    
    // Verify collections and setup indexes
    if err := verifyCollections(ctx); err != nil {
        log.Printf("⚠️ Warning during collection verification: %v", err)
    }
}

func testConnection(ctx context.Context, client *mongo.Client) error {
    maxRetries := 3
    for i := 0; i < maxRetries; i++ {
        if err := client.Ping(ctx, nil); err != nil {
            if i == maxRetries-1 {
                return fmt.Errorf("ping failed after %d attempts: %v", maxRetries, err)
            }
            log.Printf("⚠️ Ping attempt %d failed, retrying...", i+1)
            time.Sleep(time.Duration(i+1) * time.Second)
            continue
        }
        return nil
    }
    return nil
}

func hideSensitiveInfo(uri string) string {
    if strings.Contains(uri, "@") {
        parts := strings.Split(uri, "@")
        if len(parts) >= 2 {
            credPart := parts[0]
            if strings.Contains(credPart, ":") {
                credParts := strings.Split(credPart, ":")
                if len(credParts) >= 3 {
                    return fmt.Sprintf("%s:%s:***@%s", credParts[0], credParts[1], parts[1])
                }
            }
        }
    }
    return uri
}

func verifyCollections(ctx context.Context) error {
    requiredCollections := []string{"projects", "chat_messages", "chat_users", "gemini_usage_logs"}
    
    // List existing collections
    collections, err := DB.ListCollectionNames(ctx, bson.M{})
    if err != nil {
        return fmt.Errorf("failed to list collections: %v", err)
    }
    
    log.Printf("📊 Available collections: %v", collections)
    
    // Check for required collections
    existingMap := make(map[string]bool)
    for _, col := range collections {
        existingMap[col] = true
    }
    
    for _, required := range requiredCollections {
        if !existingMap[required] {
            log.Printf("⚠️ Collection '%s' does not exist, it will be created on first use", required)
        } else {
            log.Printf("✅ Collection '%s' found", required)
        }
    }
    
    // Setup indexes for better performance
    return setupIndexes(ctx)
}

func setupIndexes(ctx context.Context) error {
    // Projects collection indexes
    projectsCol := DB.Collection("projects")
    _, err := projectsCol.Indexes().CreateMany(ctx, []mongo.IndexModel{
        {
            Keys: bson.D{{"name", 1}},
            Options: options.Index().SetBackground(true),
        },
        {
            Keys: bson.D{{"is_active", 1}},
            Options: options.Index().SetBackground(true),
        },
        {
            Keys: bson.D{{"created_at", -1}},
            Options: options.Index().SetBackground(true),
        },
    })
    if err != nil {
        log.Printf("⚠️ Failed to create projects indexes: %v", err)
    }
    
    // Chat messages collection indexes
    chatCol := DB.Collection("chat_messages")
    _, err = chatCol.Indexes().CreateMany(ctx, []mongo.IndexModel{
        {
            Keys: bson.D{{"project_id", 1}, {"session_id", 1}},
            Options: options.Index().SetBackground(true),
        },
        {
            Keys: bson.D{{"timestamp", -1}},
            Options: options.Index().SetBackground(true),
        },
        {
            Keys: bson.D{{"project_id", 1}, {"timestamp", -1}},
            Options: options.Index().SetBackground(true),
        },
    })
    if err != nil {
        log.Printf("⚠️ Failed to create chat_messages indexes: %v", err)
    }
    
    // Chat users collection indexes
    usersCol := DB.Collection("chat_users")
    _, err = usersCol.Indexes().CreateMany(ctx, []mongo.IndexModel{
        {
            Keys: bson.D{{"project_id", 1}, {"email", 1}},
            Options: options.Index().SetBackground(true).SetUnique(true),
        },
        {
            Keys: bson.D{{"created_at", -1}},
            Options: options.Index().SetBackground(true),
        },
    })
    if err != nil {
        log.Printf("⚠️ Failed to create chat_users indexes: %v", err)
    }
    
    // Gemini usage logs collection indexes
    usageCol := DB.Collection("gemini_usage_logs")
    _, err = usageCol.Indexes().CreateMany(ctx, []mongo.IndexModel{
        {
            Keys: bson.D{{"project_id", 1}, {"timestamp", -1}},
            Options: options.Index().SetBackground(true),
        },
        {
            Keys: bson.D{{"timestamp", -1}},
            Options: options.Index().SetBackground(true),
        },
    })
    if err != nil {
        log.Printf("⚠️ Failed to create gemini_usage_logs indexes: %v", err)
    }
    
    log.Println("📈 Database indexes setup completed")
    return nil
}

// Enhanced collection access with validation
func GetCollection(collectionName string) *mongo.Collection {
    if DB == nil {
        log.Fatal("❌ Database not initialized. Call InitMongoDB() first.")
    }
    
    if collectionName == "" {
        log.Fatal("❌ Collection name cannot be empty")
    }
    
    return DB.Collection(collectionName)
}

// Convenience functions for commonly used collections
func GetProjectsCollection() *mongo.Collection {
    return GetCollection("projects")
}

func GetChatMessagesCollection() *mongo.Collection {
    return GetCollection("chat_messages")
}

func GetChatUsersCollection() *mongo.Collection {
    return GetCollection("chat_users")
}

func GetGeminiUsageLogsCollection() *mongo.Collection {
    return GetCollection("gemini_usage_logs")
}

// Health check and connection monitoring
func HealthCheck() error {
    if DB == nil {
        return fmt.Errorf("database not initialized")
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    // Test connection
    if err := Client.Ping(ctx, nil); err != nil {
        return fmt.Errorf("database ping failed: %v", err)
    }
    
    // Test a simple query
    collection := GetCollection("projects")
    count, err := collection.CountDocuments(ctx, bson.M{})
    if err != nil {
        return fmt.Errorf("database query failed: %v", err)
    }
    
    log.Printf("💚 Database health check passed (Projects: %d)", count)
    return nil
}

func GetDatabaseStats() map[string]interface{} {
    if DB == nil {
        return map[string]interface{}{"error": "database not initialized"}
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    
    stats := make(map[string]interface{})
    
    // Get collection counts
    collections := []string{"projects", "chat_messages", "chat_users", "gemini_usage_logs"}
    for _, colName := range collections {
        count, err := GetCollection(colName).CountDocuments(ctx, bson.M{})
        if err != nil {
            stats[colName] = "error"
        } else {
            stats[colName] = count
        }
    }
    
    // Add connection info
    stats["database_name"] = DB.Name()
    stats["connected"] = true
    stats["timestamp"] = time.Now().Format(time.RFC3339)
    
    return stats
}

// Graceful shutdown
func CloseMongoDB() {
    if Client != nil {
        ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        
        if err := Client.Disconnect(ctx); err != nil {
            log.Printf("❌ Error disconnecting from MongoDB: %v", err)
        } else {
            log.Println("✅ Disconnected from MongoDB successfully")
        }
    }
}

// Fix project limits for zero values
func FixProjectLimits() error {
    if DB == nil {
        return fmt.Errorf("database not initialized")
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    collection := GetProjectsCollection()
    
    // Find projects with zero limits
    filter := bson.M{
        "$or": []bson.M{
            {"gemini_daily_limit": 0},
            {"gemini_monthly_limit": 0},
            {"last_daily_reset": bson.M{"$lt": time.Now().AddDate(0, 0, -1)}},
            {"last_monthly_reset": bson.M{"$lt": time.Now().AddDate(0, -1, 0)}},
        },
    }
    
    update := bson.M{
        "$set": bson.M{
            "gemini_daily_limit":   100,
            "gemini_monthly_limit": 3000,
            "last_daily_reset":     time.Now(),
            "last_monthly_reset":   time.Now(),
            "updated_at":          time.Now(),
        },
    }
    
    result, err := collection.UpdateMany(ctx, filter, update)
    if err != nil {
        return fmt.Errorf("failed to fix project limits: %v", err)
    }
    
    log.Printf("✅ Fixed limits for %d projects", result.ModifiedCount)
    return nil
}

// Initialize default project settings
func InitializeProjectDefaults(projectID string) error {
    if DB == nil {
        return fmt.Errorf("database not initialized")
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    collection := GetProjectsCollection()
    
    update := bson.M{
        "$setOnInsert": bson.M{
            "gemini_daily_limit":   100,
            "gemini_monthly_limit": 3000,
            "gemini_usage_today":   0,
            "gemini_usage_month":   0,
            "last_daily_reset":     time.Now(),
            "last_monthly_reset":   time.Now(),
            "pdf_files":           []interface{}{},
            "pdf_content":         "",
            "welcome_message":     "Hello! How can I help you today?",
            "created_at":          time.Now(),
            "updated_at":          time.Now(),
        },
    }
    
    objID, err := primitive.ObjectIDFromHex(projectID)
    if err != nil {
        return fmt.Errorf("invalid project ID: %v", err)
    }
    
    _, err = collection.UpdateOne(ctx, bson.M{"_id": objID}, update, options.Update().SetUpsert(true))
    if err != nil {
        return fmt.Errorf("failed to initialize project defaults: %v", err)
    }
    
    log.Printf("✅ Initialized defaults for project: %s", projectID)
    return nil
}
