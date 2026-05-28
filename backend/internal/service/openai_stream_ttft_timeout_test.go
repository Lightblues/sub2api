package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
)

func TestGroupFirstTokenTimeout(t *testing.T) {
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			GroupFirstTokenTimeoutSeconds: map[string]int{
				"ian_private": 15,
				"other":       0,
			},
		},
	}
	if got := groupFirstTokenTimeout(cfg, "ian_private"); got != 15*time.Second {
		t.Fatalf("expected 15s, got %v", got)
	}
	if got := groupFirstTokenTimeout(cfg, "other"); got != 0 {
		t.Fatalf("expected disabled, got %v", got)
	}
	if got := groupFirstTokenTimeout(cfg, "missing"); got != 0 {
		t.Fatalf("expected disabled, got %v", got)
	}
}

func TestOpenAIStreamingFirstTokenTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			GroupFirstTokenTimeoutSeconds: map[string]int{
				"ian_private": 1,
			},
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := &OpenAIGatewayService{cfg: cfg}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Set("api_key", &APIKey{Group: &Group{Name: "ian_private"}})

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	go func() {
		_, _ = pw.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		time.Sleep(2 * time.Second)
		_, _ = pw.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}}\n\n"))
		_ = pw.Close()
	}()

	start := time.Now()
	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1}, start, "model", "model")
	_ = pr.Close()

	if err == nil || !strings.Contains(err.Error(), "first token timeout") {
		t.Fatalf("expected first token timeout error, got %v", err)
	}
	if !strings.Contains(rec.Body.String(), openAIFirstTokenTimeoutErrorCode) {
		t.Fatalf("expected OpenAI-compatible first_token_timeout SSE event, got %q", rec.Body.String())
	}
}

func TestOpenAIStreamingFirstTokenTimeoutDisabledForOtherGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			GroupFirstTokenTimeoutSeconds: map[string]int{
				"ian_private": 1,
			},
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
	svc := &OpenAIGatewayService{cfg: cfg}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Set("api_key", &APIKey{Group: &Group{Name: "public"}})

	pr, pw := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       pr,
		Header:     http.Header{},
	}

	go func() {
		_, _ = pw.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		time.Sleep(2 * time.Second)
		_, _ = pw.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}}\n\n"))
		_ = pw.Close()
	}()

	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1}, time.Now(), "model", "model")
	_ = pr.Close()

	if err != nil && strings.Contains(err.Error(), "first token timeout") {
		t.Fatalf("unexpected first token timeout for unrelated group: %v", err)
	}
}
