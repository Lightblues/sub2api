package service

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
)

const openAIFirstTokenTimeoutErrorCode = "first_token_timeout"

// groupFirstTokenTimeout returns the configured max wait for the first non-preamble SSE event.
func groupFirstTokenTimeout(cfg *config.Config, groupName string) time.Duration {
	if cfg == nil || groupName == "" {
		return 0
	}
	secs, ok := cfg.Gateway.GroupFirstTokenTimeoutSeconds[groupName]
	if !ok || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

func (s *OpenAIGatewayService) openAIGroupFirstTokenTimeout(c *gin.Context) time.Duration {
	if s == nil {
		return 0
	}
	apiKey := getAPIKeyFromContext(c)
	if apiKey == nil || apiKey.Group == nil {
		return 0
	}
	return groupFirstTokenTimeout(s.cfg, apiKey.Group.Name)
}

func openAIFirstTokenTimeoutPending(firstTokenMs *int, timeout time.Duration, startTime time.Time) bool {
	return timeout > 0 && firstTokenMs == nil && time.Since(startTime) >= timeout
}
