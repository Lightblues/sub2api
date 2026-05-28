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

// openAIRequestGroupName returns the resolved group name from the request context, "" if absent.
// Used for observability (logs/metrics) so operators can filter by group.
func openAIRequestGroupName(c *gin.Context) string {
	apiKey := getAPIKeyFromContext(c)
	if apiKey == nil || apiKey.Group == nil {
		return ""
	}
	return apiKey.Group.Name
}
