package network

import (
	"time"
)

// RetryConfig holds configuration for connection retries
type RetryConfig struct {
	MaxAttempts   int
	InitialDelay  time.Duration
	MaxDelay      time.Duration
	BackoffFactor float64
}

// DefaultRetryConfig returns the default retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts:   5,
		InitialDelay:  time.Second,
		MaxDelay:      time.Second * 30,
		BackoffFactor: 2.0,
	}
}

// calculateDelay calculates the next delay using exponential backoff
func (c *RetryConfig) calculateDelay(attempt int) time.Duration {
	delay := float64(c.InitialDelay) * pow(c.BackoffFactor, float64(attempt))
	if delay > float64(c.MaxDelay) {
		delay = float64(c.MaxDelay)
	}
	return time.Duration(delay)
}

func pow(x, y float64) float64 {
	result := 1.0
	for i := 0; i < int(y); i++ {
		result *= x
	}
	return result
}
