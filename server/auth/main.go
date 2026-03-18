package main

import (
	"context"
	"log"
	"os"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var ctx = context.Background()

func main() {
	// ── Redis ────────────────────────────────────
	redisAddr := os.Getenv("REDIS_ADDR")
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: os.Getenv("REDIS_PASSWORD"),
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Redis connect failed: %v", err)
	}
	log.Println("Redis connected")

	// ── PostgreSQL ───────────────────────────────
	dbURL := os.Getenv("DATABASE_URL")

	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("DB connect failed: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS users (
            id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            email      VARCHAR(255) UNIQUE NOT NULL,
            created_at TIMESTAMPTZ DEFAULT NOW()
        );
    `); err != nil {
		log.Fatalf("DB schema init failed: %v", err)
	}
	log.Println("PostgreSQL connected")

	// ── Mailer ───────────────────────────────────
	mailer := NewMailer(
		os.Getenv("SMTP_HOST"),
		os.Getenv("SMTP_PORT"),
		os.Getenv("SMTP_USER"),
		os.Getenv("SMTP_PASS"),
		os.Getenv("SMTP_FROM"),
	)

	// ── Fiber ────────────────────────────────────
	app := fiber.New()
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Content-Type,Authorization",
	}))

	registerAuthRoutes(app, rdb, db, mailer)

	log.Println("Auth Server: http://0.0.0.0:8080")
	log.Fatal(app.Listen(":8080"))
}
