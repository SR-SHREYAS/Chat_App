package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"time"

	"real_time_chat_app/internal/api"
	authapp "real_time_chat_app/internal/app/auth"
	"real_time_chat_app/internal/app/chat"
	"real_time_chat_app/internal/middleware"
	"real_time_chat_app/internal/store"

	"real_time_chat_app/internal/model"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

// ...existing imports...

type authStoreAdapter struct {
	*store.AuthStore
	db *sql.DB
}

// UpdateUsername updates a user's username based on the userID
// and returns the updated user to satisfy auth.AuthStore.
func (a *authStoreAdapter) UpdateUsername(ctx context.Context, userID string, username string) (model.User, error) {
	// Start a transaction to ensure consistency
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return model.User{}, err
	}
	defer func() {
		// rollback on panic; commit/rollback handled below
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	// Update username by user ID
	_, err = tx.ExecContext(ctx,
		"UPDATE users SET username = $1 WHERE id = $2",
		username, userID,
	)
	if err != nil {
		_ = tx.Rollback()
		return model.User{}, err
	}

	// Load updated user by user ID
	var u model.User
	err = tx.QueryRowContext(ctx,
		"SELECT id, username, email FROM users WHERE id = $1",
		userID,
	).Scan(&u.ID, &u.Username, &u.Email)
	if err != nil {
		_ = tx.Rollback()
		return model.User{}, err
	}

	if err := tx.Commit(); err != nil {
		return model.User{}, err
	}

	return u, nil
}

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

	authCtx, cancelAuth := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelAuth()

	authStore := store.NewAuthStore(db)
	if err := authStore.EnsureSchema(authCtx); err != nil {
		log.Fatalf("Could not create auth tables: %v", err)
	}
	log.Println("Auth tables created or already exist.")

	roomsCtx, cancelRooms := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRooms()

	signedRoomStore := store.NewSignedRoomStore(db)
	if err := signedRoomStore.EnsureSchema(roomsCtx); err != nil {
		log.Fatalf("Could not create signed room tables: %v", err)
	}
	log.Println("Signed room tables created or already exist.")

	schemaCtx, cancelSchema := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelSchema()

	messageStore := store.NewMessageStore(db)
	if err := messageStore.EnsureSchema(schemaCtx); err != nil {
		log.Fatalf("Could not create messages table: %v", err)
	}
	log.Println("Messages table created or already exists.")

	service := chat.NewService(messageStore)
	service.BindSignedRoomStore(signedRoomStore)
	authAdapter := &authStoreAdapter{AuthStore: authStore, db: db}
	authService := authapp.NewService(authAdapter)
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
