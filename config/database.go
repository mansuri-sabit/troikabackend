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

    // ✅ Initialize subscription defaults for existing projects
    go func() {
        time.Sleep(2 * time.Second) // Wait for connection to stabilize
        if err := InitializeSubscriptionDefaults(); err != nil {
            log.Printf("⚠️ Warning during subscription initialization: %v", err)
        }
    }()
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

// ✅ ENHANCED: Complete subscription management indexes
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
        // ✅ NEW: Subscription-specific indexes
        {
            Keys: bson.D{{"status", 1}},
            Options: options.Index().SetBackground(true),
        },
        {
            Keys: bson.D{{"expiry_date", 1}},
            Options: options.Index().SetBackground(true),
        },
        {
            Keys: bson.D{{"total_tokens_used", 1}},
            Options: options.Index().SetBackground(true),
        },
        {
            Keys: bson.D{{"status", 1}, {"expiry_date", 1}},
            Options: options.Index().SetBackground(true),
        },
        {
            Keys: bson.D{{"monthly_token_limit", 1}},
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
        {
            Keys: bson.D{{"project_id", 1}, {"success", 1}},
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

// ✅ ENHANCED: Complete subscription management function
func FixProjectLimits() error {
    if DB == nil {
        return fmt.Errorf("database not initialized")
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    collection := GetProjectsCollection()
    
    // Find projects with zero limits or missing subscription fields
    filter := bson.M{
        "$or": []bson.M{
            {"gemini_daily_limit": 0},
            {"gemini_monthly_limit": 0},
            {"last_daily_reset": bson.M{"$lt": time.Now().AddDate(0, 0, -1)}},
            {"last_monthly_reset": bson.M{"$lt": time.Now().AddDate(0, -1, 0)}},
            {"status": bson.M{"$exists": false}},
            {"expiry_date": bson.M{"$exists": false}},
            {"total_tokens_used": bson.M{"$exists": false}},
            {"monthly_token_limit": bson.M{"$exists": false}},
            {"start_date": bson.M{"$exists": false}},
            {"last_token_reset": bson.M{"$exists": false}},
        },
    }
    
    update := bson.M{
        "$set": bson.M{
            "gemini_daily_limit":   100,
            "gemini_monthly_limit": 3000,
            "last_daily_reset":     time.Now(),
            "last_monthly_reset":   time.Now(),
            "last_token_reset":     time.Now(),
            "updated_at":          time.Now(),
            // ✅ Subscription fields
            "status":              "active",
            "start_date":          time.Now(),
            "expiry_date":         time.Now().AddDate(0, 1, 0), // 1 month from now
            "total_tokens_used":   int64(0),
            "monthly_token_limit": int64(100000), // 100k tokens default
        },
    }
    
    result, err := collection.UpdateMany(ctx, filter, update)
    if err != nil {
        return fmt.Errorf("failed to fix project limits: %v", err)
    }
    
    log.Printf("✅ Fixed limits and subscription fields for %d projects", result.ModifiedCount)
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
            "last_token_reset":     time.Now(),
            "pdf_files":           []interface{}{},
            "pdf_content":         "",
            "welcome_message":     "Hello! How can I help you today?",
            "created_at":          time.Now(),
            "updated_at":          time.Now(),
            // ✅ Subscription defaults
            "status":              "active",
            "start_date":          time.Now(),
            "expiry_date":         time.Now().AddDate(0, 1, 0),
            "total_tokens_used":   int64(0),
            "monthly_token_limit": int64(100000),
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

// ✅ NEW: Initialize subscription defaults for existing projects
func InitializeSubscriptionDefaults() error {
    if DB == nil {
        return fmt.Errorf("database not initialized")
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    collection := GetProjectsCollection()
    
    // Find projects missing subscription fields
    filter := bson.M{
        "$or": []bson.M{
            {"status": bson.M{"$exists": false}},
            {"expiry_date": bson.M{"$exists": false}},
            {"total_tokens_used": bson.M{"$exists": false}},
            {"monthly_token_limit": bson.M{"$exists": false}},
            {"start_date": bson.M{"$exists": false}},
        },
    }
    
    update := bson.M{
        "$set": bson.M{
            "status":              "active",
            "start_date":          time.Now(),
            "expiry_date":         time.Now().AddDate(0, 1, 0), // 1 month from now
            "total_tokens_used":   int64(0),
            "monthly_token_limit": int64(100000), // 100k tokens default
            "updated_at":          time.Now(),
        },
    }
    
    result, err := collection.UpdateMany(ctx, filter, update)
    if err != nil {
        return fmt.Errorf("failed to initialize subscription defaults: %v", err)
    }
    
    log.Printf("✅ Initialized subscription defaults for %d projects", result.ModifiedCount)
    return nil
}

// ✅ NEW: Get expired projects
func GetExpiredProjects() ([]primitive.ObjectID, error) {
    if DB == nil {
        return nil, fmt.Errorf("database not initialized")
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    collection := GetProjectsCollection()
    
    filter := bson.M{
        "expiry_date": bson.M{"$lt": time.Now()},
        "status":      bson.M{"$ne": "expired"},
    }
    
    cursor, err := collection.Find(ctx, filter, options.Find().SetProjection(bson.M{"_id": 1}))
    if err != nil {
        return nil, err
    }
    defer cursor.Close(ctx)
    
    var expiredProjects []primitive.ObjectID
    for cursor.Next(ctx) {
        var project struct {
            ID primitive.ObjectID `bson:"_id"`
        }
        if err := cursor.Decode(&project); err != nil {
            continue
        }
        expiredProjects = append(expiredProjects, project.ID)
    }
    
    return expiredProjects, nil
}

// ✅ NEW: Update expired projects
func UpdateExpiredProjects() error {
    if DB == nil {
        return fmt.Errorf("database not initialized")
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    collection := GetProjectsCollection()
    
    filter := bson.M{
        "expiry_date": bson.M{"$lt": time.Now()},
        "status":      bson.M{"$ne": "expired"},
    }
    
    update := bson.M{
        "$set": bson.M{
            "status":     "expired",
            "updated_at": time.Now(),
        },
    }
    
    result, err := collection.UpdateMany(ctx, filter, update)
    if err != nil {
        return fmt.Errorf("failed to update expired projects: %v", err)
    }
    
    log.Printf("✅ Marked %d projects as expired", result.ModifiedCount)
    return nil
}

// ✅ NEW: Get subscription statistics
func GetSubscriptionStats() (map[string]interface{}, error) {
    if DB == nil {
        return nil, fmt.Errorf("database not initialized")
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    collection := GetProjectsCollection()
    
    // Aggregate subscription statistics
    pipeline := []bson.M{
        {
            "$group": bson.M{
                "_id": "$status",
                "count": bson.M{"$sum": 1},
                "total_tokens": bson.M{"$sum": "$total_tokens_used"},
                "avg_tokens": bson.M{"$avg": "$total_tokens_used"},
            },
        },
    }
    
    cursor, err := collection.Aggregate(ctx, pipeline)
    if err != nil {
        return nil, err
    }
    defer cursor.Close(ctx)
    
    var stats []bson.M
    if err := cursor.All(ctx, &stats); err != nil {
        return nil, err
    }
    
    return map[string]interface{}{
        "subscription_stats": stats,
        "timestamp":         time.Now().Format(time.RFC3339),
    }, nil
}

// ✅ NEW: Run subscription maintenance
func RunSubscriptionMaintenance() error {
    log.Println("🔄 Running subscription maintenance...")
    
    // Update expired projects
    if err := UpdateExpiredProjects(); err != nil {
        log.Printf("❌ Failed to update expired projects: %v", err)
        return err
    }
    
    // Fix any projects with missing limits
    if err := FixProjectLimits(); err != nil {
        log.Printf("❌ Failed to fix project limits: %v", err)
        return err
    }
    
    log.Println("✅ Subscription maintenance completed")
    return nil
}

// ✅ NEW: Reset monthly token usage for all projects
func ResetMonthlyTokenUsage() error {
    if DB == nil {
        return fmt.Errorf("database not initialized")
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    
    collection := GetProjectsCollection()
    
    update := bson.M{
        "$set": bson.M{
            "total_tokens_used": int64(0),
            "updated_at":        time.Now(),
        },
    }
    
    result, err := collection.UpdateMany(ctx, bson.M{}, update)
    if err != nil {
        return fmt.Errorf("failed to reset monthly token usage: %v", err)
    }
    
    log.Printf("✅ Reset monthly token usage for %d projects", result.ModifiedCount)
    return nil
}

// ✅ NEW: Get projects with high token usage (above 80% of limit)
func GetHighUsageProjects() ([]primitive.ObjectID, error) {
    if DB == nil {
        return nil, fmt.Errorf("database not initialized")
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    collection := GetProjectsCollection()
    
    // Find projects using more than 80% of their monthly token limit
    pipeline := []bson.M{
        {
            "$match": bson.M{
                "monthly_token_limit": bson.M{"$gt": 0},
                "total_tokens_used": bson.M{"$gt": 0},
            },
        },
        {
            "$addFields": bson.M{
                "usage_percentage": bson.M{
                    "$multiply": []interface{}{
                        bson.M{"$divide": []interface{}{"$total_tokens_used", "$monthly_token_limit"}},
                        100,
                    },
                },
            },
        },
        {
            "$match": bson.M{
                "usage_percentage": bson.M{"$gte": 80},
            },
        },
        {
            "$project": bson.M{"_id": 1},
        },
    }
    
    cursor, err := collection.Aggregate(ctx, pipeline)
    if err != nil {
        return nil, err
    }
    defer cursor.Close(ctx)
    
    var highUsageProjects []primitive.ObjectID
    for cursor.Next(ctx) {
        var project struct {
            ID primitive.ObjectID `bson:"_id"`
        }
        if err := cursor.Decode(&project); err != nil {
            continue
        }
        highUsageProjects = append(highUsageProjects, project.ID)
    }
    
    return highUsageProjects, nil
}
