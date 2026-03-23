package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"real_time_chat_app/internal/api"
	"real_time_chat_app/internal/app/chat"
	"real_time_chat_app/internal/middleware"
	"real_time_chat_app/internal/store"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables from system")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is not set")
	}

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		log.Fatalf("Could not connect to database: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)

	schemaCtx, cancelSchema := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelSchema()

	messageStore := store.NewMessageStore(db)
	if err := messageStore.EnsureSchema(schemaCtx); err != nil {
		log.Fatalf("Could not create messages table: %v", err)
	}
	log.Println("Messages table created or already exists.")

	service := chat.NewService(messageStore)
	handler := api.NewHandler(service)

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	api.RegisterRoutes(mux, handler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	log.Println("starting web server on", addr)
	if err := http.ListenAndServe(addr, middleware.CORS(mux)); err != nil {
		log.Fatal("ListenAndServe:", err)
	}
}
