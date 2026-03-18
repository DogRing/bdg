package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/smtp"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	otpTTL     = 10 * time.Minute
	sessionTTL = 24 * time.Hour
)

// ── Mailer ────────────────────────────────────────────
type Mailer struct {
	host string
	port string
	from string
	auth smtp.Auth
}

func NewMailer(host, port, user, pass, from string) *Mailer {
	return &Mailer{
		host: host,
		port: port,
		from: from,
		auth: smtp.PlainAuth("", user, pass, host),
	}
}

func (m *Mailer) SendOTP(to, otp string) error {
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: 로그인 코드\r\n\r\n인증코드: %s\r\n(10분간 유효)",
		m.from, to, otp,
	)
	return smtp.SendMail(m.host+":"+m.port, m.auth, m.from, []string{to}, []byte(msg))
}

// ── OTP 생성 ──────────────────────────────────────────
func generateOTP() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	return fmt.Sprintf("%06d", n)
}

// ── 라우트 등록 ───────────────────────────────────────
func registerAuthRoutes(app *fiber.App, rdb *redis.Client, db *pgxpool.Pool, mailer *Mailer) {

	// POST /auth/otp - 이메일로 OTP 발송
	app.Post("/auth/otp", func(c *fiber.Ctx) error {
		var req struct {
			Email string `json:"email"`
		}
		if err := c.BodyParser(&req); err != nil || req.Email == "" {
			return c.Status(400).JSON(fiber.Map{"error": "이메일을 입력하세요"})
		}

		otp := generateOTP()
		if err := rdb.Set(ctx, "otp:"+req.Email, otp, otpTTL).Err(); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "서버 오류"})
		}
		if err := mailer.SendOTP(req.Email, otp); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "메일 발송 실패: " + err.Error()})
		}
		return c.JSON(fiber.Map{"ok": true})
	})

	// POST /auth/login - OTP 검증 후 세션 발급
	app.Post("/auth/login", func(c *fiber.Ctx) error {
		var req struct {
			Email string `json:"email"`
			OTP   string `json:"otp"`
		}
		if err := c.BodyParser(&req); err != nil || req.Email == "" || req.OTP == "" {
			return c.Status(400).JSON(fiber.Map{"error": "이메일과 인증코드를 입력하세요"})
		}

		saved, err := rdb.Get(ctx, "otp:"+req.Email).Result()
		if err != nil || saved != req.OTP {
			return c.Status(401).JSON(fiber.Map{"error": "인증코드가 올바르지 않습니다"})
		}
		rdb.Del(ctx, "otp:"+req.Email)

		var userID string
		if err := db.QueryRow(ctx,
			`WITH ins AS (
				INSERT INTO users (email)
				VALUES ($1)
				ON CONFLICT (email) DO NOTHING
				RETURNING id
			)
			SELECT id FROM ins
			UNION ALL
			SELECT id FROM users WHERE email = $1
			LIMIT 1`,
			req.Email,
		).Scan(&userID); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "DB 오류"})
		}

		token := uuid.NewString()
		if err := rdb.Set(ctx, "session:"+token, userID, sessionTTL).Err(); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "세션 생성 실패"})
		}

		return c.JSON(fiber.Map{"token": token})
	})
}
