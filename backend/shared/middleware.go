package shared

import (
	"context"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

func AuthMiddleware(rdb *redis.Client) fiber.Handler {
	return func(c *fiber.Ctx) error {
		token := c.Get("Authorization")
		if len(token) > 7 && token[:7] == "Bearer " {
			token = token[7:]
		}
		if token == "" {
			return c.Status(401).JSON(fiber.Map{"error": "인증 필요"})
		}
		userID, err := rdb.Get(context.Background(), "session:"+token).Result()
		if err != nil {
			return c.Status(401).JSON(fiber.Map{"error": "세션 만료"})
		}
		c.Locals("userID", userID)
		return c.Next()
	}
}
