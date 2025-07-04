package config

import (
    "context"
    "log"
    "os"
    "time"
    
    "go.mongodb.org/mongo-driver/mongo"
    "go.mongodb.org/mongo-driver/mongo/options"
)

var DB *mongo.Database

func InitMongoDB() {
    uri := os.Getenv("MONGODB_URI")
    if uri == "" {
        log.Fatal("MONGODB_URI not set in environment")
    }
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
    if err != nil {
        log.Fatal("Failed to connect to MongoDB:", err)
    }
    
    // Test connection
    if err := client.Ping(ctx, nil); err != nil {
        log.Fatal("Failed to ping MongoDB:", err)
    }
    
    DB = client.Database("jevi_chat")
    log.Println("Connected to MongoDB successfully")
}

// Add this function to fix the undefined error
func GetCollection(collectionName string) *mongo.Collection {
    if DB == nil {
        log.Fatal("Database not initialized. Call InitMongoDB() first.")
    }
    return DB.Collection(collectionName)
}
