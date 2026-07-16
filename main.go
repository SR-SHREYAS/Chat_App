package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"real_time_chat_app/internal/api"
	authapp "real_time_chat_app/internal/app/auth"
	"real_time_chat_app/internal/app/chat"
	"real_time_chat_app/internal/middleware"
	"real_time_chat_app/internal/repository"
	"real_time_chat_app/internal/storage/postgres"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables from system")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is not set")
	}

	database, err := postgres.Open(databaseURL)
	if err != nil {
		log.Fatalf("Could not connect to database: %v", err)
	}
	defer database.Close()

	authCtx, cancelAuth := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelAuth()

	authRepository := repository.NewAuthRepository(database.Client())
	if err := authRepository.EnsureSchema(authCtx); err != nil {
		log.Fatalf("Could not create auth tables: %v", err)
	}
	log.Println("Auth tables created or already exist.")

	roomsCtx, cancelRooms := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRooms()

	signedRoomRepository := repository.NewSignedRoomRepository(database.Client())
	if err := signedRoomRepository.EnsureSchema(roomsCtx); err != nil {
		log.Fatalf("Could not create signed room tables: %v", err)
	}
	log.Println("Signed room tables created or already exist.")

	schemaCtx, cancelSchema := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelSchema()

	messageRepository := repository.NewMessageRepository(database.Client())
	if err := messageRepository.EnsureSchema(schemaCtx); err != nil {
		log.Fatalf("Could not create messages table: %v", err)
	}
	log.Println("Messages table created or already exists.")

	service := chat.NewService(messageRepository, signedRoomRepository)
	authService := authapp.NewService(authRepository)
	handler := api.NewHandler(service, authService)

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
