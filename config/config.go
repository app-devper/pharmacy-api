package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	MongoURI       string
	DBPrefix       string // DB name prefix; tenant DB = "<DBPrefix>_<clientId>"
	Port           string
	SecretKey      string
	System         string
	FrontendOrigin string // comma-separated origin allowlist for CORS; "" → Allow-Origin: * fallback
}

func Load() (*Config, error) {
	_ = godotenv.Load()
	cfg := &Config{
		MongoURI:       getEnv("MONGO_URI", "mongodb://localhost:27017"),
		DBPrefix:       getEnv("DB_PREFIX", "pharmacy"),
		Port:           getEnv("PORT", "8080"),
		SecretKey:      os.Getenv("SECRET_KEY"),
		System:         os.Getenv("SYSTEM"),
		FrontendOrigin: os.Getenv("FRONTEND_ORIGIN"),
	}
	if cfg.SecretKey == "" {
		return nil, fmt.Errorf("SECRET_KEY is required")
	}
	if cfg.System == "" {
		return nil, fmt.Errorf("SYSTEM is required")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
