package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	MongoURI       string
	DBPrefix       string
	Port           string
	SecretKey      string
	System         string
	FrontendOrigin string
	UMApiURL       string
	GatewayHosts   string
}

func Load() (*Config, error) {
	_ = godotenv.Load()
	cfg := &Config{
		MongoURI:       os.Getenv("MONGO_URI"),
		DBPrefix:       getEnv("DB_PREFIX", "pharmacy"),
		Port:           getEnv("PORT", "8080"),
		SecretKey:      os.Getenv("SECRET_KEY"),
		System:         os.Getenv("SYSTEM"),
		FrontendOrigin: os.Getenv("FRONTEND_ORIGIN"),
		UMApiURL:       os.Getenv("UM_API_URL"),
		GatewayHosts:   os.Getenv("GATEWAY_HOSTS"),
	}
	if cfg.MongoURI == "" {
		return nil, fmt.Errorf("MONGO_URI is required")
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
