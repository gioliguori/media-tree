package config

import (
	"os"
	"strconv"
)

type Config struct {
	RedisHost     string
	RedisPort     int
	RedisPassword string
	RedisDB       int
}

func Load() (*Config, error) {
	cfg := &Config{
		RedisHost:     getEnv("REDIS_HOST", "redis"),
		RedisPort:     getEnvInt("REDIS_PORT", 6379),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvInt("REDIS_DB", 0),
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
