package app

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DSN, Key, Addr, Origin, MediaDir, MetaID, MetaSecret, MetaConfig, MetaVersion, WebhookVerify, TelegramID, TelegramHash, InstagramID, InstagramSecret string
	SecureCookie, EnableUnofficial, StopSends                                                                                                            bool
	WAInterval                                                                                                                                           int
}

func ConfigFromEnv() (Config, error) {
	c := Config{DSN: os.Getenv("DATABASE_URL"), Key: os.Getenv("ENCRYPTION_KEY"), Addr: env("API_ADDR", ":8002"), Origin: strings.TrimRight(env("PUBLIC_URL", "http://localhost:5175"), "/"), MediaDir: env("MEDIA_DIR", "./data/media"), MetaID: os.Getenv("META_APP_ID"), MetaSecret: os.Getenv("META_APP_SECRET"), MetaConfig: os.Getenv("META_SIGNUP_CONFIG_ID"), MetaVersion: env("META_GRAPH_VERSION", "v25.0"), WebhookVerify: os.Getenv("META_WEBHOOK_VERIFY_TOKEN"), TelegramID: os.Getenv("TELEGRAM_API_ID"), TelegramHash: os.Getenv("TELEGRAM_API_HASH"), InstagramID: os.Getenv("INSTAGRAM_APP_ID"), InstagramSecret: os.Getenv("INSTAGRAM_APP_SECRET"), EnableUnofficial: os.Getenv("ENABLE_WHATSAPP_WEB") == "true", StopSends: os.Getenv("STOP_ALL_SENDS") == "true", WAInterval: 15}
	c.SecureCookie = strings.HasPrefix(c.Origin, "https://")
	if v, e := strconv.Atoi(env("WA_SEND_INTERVAL_SECONDS", "15")); e == nil && v >= 5 {
		c.WAInterval = v
	}
	if c.DSN == "" || c.Key == "" {
		return c, errors.New("DATABASE_URL and ENCRYPTION_KEY are required")
	}
	return c, nil
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
