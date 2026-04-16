package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	MongoURI       string
	DBName         string
	Port           string
	FrontendOrigin string
	SecretKey      string
	UmApiURL       string
}

func Load() *Config {
	_ = godotenv.Load()
	return &Config{
		MongoURI:       getEnv("MONGO_URI", "mongodb://localhost:27017"),
		DBName:         getEnv("DB_NAME", "pharmacy"),
		Port:           getEnv("PORT", "8080"),
		FrontendOrigin: getEnv("FRONTEND_ORIGIN", "http://localhost:5173"),
		SecretKey:      getEnv("SECRET_KEY", ""),
		UmApiURL:       getEnv("UM_API_URL", "http://localhost:8585"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
