package main

import (
	"context"
	"fmt"
	"github.com/redis/go-redis/v9"
	"log"
	"net/http"
	"strconv"
	"time"
)

type QuotaConfig struct {
	Enabled bool   `yaml:"enabled"`
	Limit   int64  `yaml:"limit"`
	Period  string `yaml:"period"`
}

func quotaMiddleware(next http.Handler, cfg QuotaConfig, rdb *redis.Client) http.Handler {
	period, err := time.ParseDuration(cfg.Period)
	if err != nil {
		log.Fatalf("Invalid quota period '%s': %v", cfg.Period, err)
	}
	periodMillis := period.Milliseconds()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get the User ID from the context (set by jwtAuthMiddleware)
		userID, ok := r.Context().Value(userIDKey).(string)
		if !ok || userID == "" {
			log.Println("Quota check failed: User ID not found in context.")
			http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
			return
		}

		ctx := context.Background()
		now := time.Now().UnixNano() / int64(time.Millisecond) // Score
		minTime := now - periodMillis
		key := fmt.Sprintf("quota:%s", userID) // Redis key per user
		member := strconv.FormatInt(now, 10)

		var count int64
		pipe := rdb.TxPipeline()
		// Remove all old requests (timestamps) from the set
		pipe.ZRemRangeByScore(ctx, key, "0", strconv.FormatInt(minTime, 10))
		// Add the new request (timestamp) to the set
		pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: member})
		// Count how many requests are in the window [minTime-now]
		countCmd := pipe.ZCard(ctx, key)
		// Set the set to expire to clean up old users
		pipe.PExpire(ctx, key, period)

		if _, err := pipe.Exec(ctx); err != nil {
			log.Printf("Redis transaction failed: %v", err)
			http.Error(w, "500 Internal Server Error", http.StatusInternalServerError)
			return
		}
		count = countCmd.Val()

		if count > cfg.Limit {
			log.Printf("Quota exceeded for user %s: %d/%d", userID, count, cfg.Limit)
			http.Error(w, "429 Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
