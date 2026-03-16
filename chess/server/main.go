package main

import (
	"context"
	"fmt"
	"log"
	"time"
	"os"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var ctx = context.Background()

const gameTTL = 2 * time.Hour

var initialBoard = []byte{
	0x04, 0x02, 0x03, 0x05, 0x06, 0x03, 0x02, 0x04,
	0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x01,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07, 0x07,
	0x0a, 0x08, 0x09, 0x0b, 0x0c, 0x09, 0x08, 0x0a,
}

func squareToIndex(sq string) int {
	if len(sq) != 2 {
		return -1
	}
	file := int(sq[0] - 'a')
	rank := int(sq[1] - '1')
	if file < 0 || file > 7 || rank < 0 || rank > 7 {
		return -1
	}
	return rank*8 + file
}

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "redis-primary.redis.svc.cluster.local:6379"
	}
	redisPassword := os.Getenv("REDIS_PASSWORD")

	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: redisPassword,
		DB:       0,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("Redis connect failed: %v", err)
	}
	log.Println("Redis connect success")

	app := fiber.New()
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Content-Type",
	}))

	api := app.Group("/chess")
	
	// POST /api/games
	app.Post("/api/games", func(c *fiber.Ctx) error {
		roomId   := uuid.NewString()
		boardKey := fmt.Sprintf("board:%s", roomId)
		roomKey  := fmt.Sprintf("room:%s", roomId)

		pipe := rdb.Pipeline()
		pipe.Set(ctx, boardKey, initialBoard, gameTTL)
		pipe.HSet(ctx, roomKey, map[string]interface{}{
			"status":     "playing",
			"turn":       "w",
			"created_at": time.Now().Unix(),
		})
		pipe.Expire(ctx, roomKey, gameTTL)
		if _, err := pipe.Exec(ctx); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "fail to create room"})
		}

		log.Printf("create game room: %s", roomId)
		return c.Status(201).JSON(fiber.Map{"roomId": roomId})
	})

	// POST /api/games/:roomId/move
	app.Post("/api/games/:roomId/move", func(c *fiber.Ctx) error {
		roomId   := c.Params("roomId")
		boardKey := fmt.Sprintf("board:%s", roomId)
		roomKey  := fmt.Sprintf("room:%s", roomId)

		type MoveReq struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		var req MoveReq
		if err := c.BodyParser(&req); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "잘못된 요청"})
		}

		fromIdx := squareToIndex(req.From)
		toIdx   := squareToIndex(req.To)
		if fromIdx < 0 || toIdx < 0 {
			return c.Status(400).JSON(fiber.Map{"error": "잘못된 좌표"})
		}

		boardBytes, err := rdb.Get(ctx, boardKey).Bytes()
		if err != nil || len(boardBytes) != 64 {
			return c.Status(404).JSON(fiber.Map{"error": "게임 없음"})
		}
		piece := boardBytes[fromIdx]
		if piece == 0 {
			return c.Status(400).JSON(fiber.Map{"error": "말이 없습니다"})
		}

		pipe := rdb.Pipeline()
		pipe.SetRange(ctx, boardKey, int64(fromIdx), string([]byte{0}))
		pipe.SetRange(ctx, boardKey, int64(toIdx),   string([]byte{piece}))
		if _, err := pipe.Exec(ctx); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "이동 실패"})
		}

		turn, _ := rdb.HGet(ctx, roomKey, "turn").Result()
		next := "b"
		if turn == "b" {
			next = "w"
		}
		rdb.HSet(ctx, roomKey, "turn", next)

		return c.JSON(fiber.Map{"ok": true, "from": req.From, "to": req.To, "turn": next})
	})

	log.Println("API Server: http://0.0.0.0:8080")
	log.Fatal(app.Listen(":8080"))
}