package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	MongoURI  string
	DBName    string
	Port      string
	SecretKey string
}

func Load() (*Config, error) {
	_ = godotenv.Load()
	cfg := &Config{
		MongoURI:  getEnv("MONGO_URI", "mongodb://localhost:27017"),
		DBName:    getEnv("DB_NAME", "pharmacy"),
		Port:      getEnv("PORT", "8080"),
		SecretKey: os.Getenv("SECRET_KEY"),
	}
	if cfg.SecretKey == "" {
		return nil, fmt.Errorf("SECRET_KEY is required")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
