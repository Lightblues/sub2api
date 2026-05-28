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

func newTTFTTestConfig(timeoutSec int) *config.Config {
	return &config.Config{
		Gateway: config.GatewayConfig{
			GroupFirstTokenTimeoutSeconds: map[string]int{
				"ian_private": timeoutSec,
			},
			StreamDataIntervalTimeout: 0,
			StreamKeepaliveInterval:   0,
			MaxLineSize:               defaultMaxLineSize,
		},
	}
}

func newTTFTTestContext(t *testing.T, groupName string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Set("api_key", &APIKey{Group: &Group{Name: groupName}})
	return c, rec
}

func TestOpenAIStreamingFirstTokenTimeout(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: newTTFTTestConfig(1)}
	c, rec := newTTFTTestContext(t, "ian_private")

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		_, _ = pw.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		time.Sleep(2 * time.Second)
		_, _ = pw.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}}\n\n"))
		_ = pw.Close()
	}()

	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1}, time.Now(), "model", "model")
	_ = pr.Close()

	if err == nil || !strings.Contains(err.Error(), "first token timeout") {
		t.Fatalf("expected first token timeout error, got %v", err)
	}
	if !strings.Contains(rec.Body.String(), openAIFirstTokenTimeoutErrorCode) {
		t.Fatalf("expected OpenAI-compatible first_token_timeout SSE event, got %q", rec.Body.String())
	}
}

func TestOpenAIStreamingFirstTokenTimeoutDisabledForOtherGroups(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: newTTFTTestConfig(1)}
	c, _ := newTTFTTestContext(t, "public")

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		_, _ = pw.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		time.Sleep(2 * time.Second)
		_, _ = pw.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		_, _ = pw.Write([]byte("data: [DONE]\n\n"))
		_ = pw.Close()
	}()

	_, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1}, time.Now(), "model", "model")
	_ = pr.Close()

	if err != nil && strings.Contains(err.Error(), "first token timeout") {
		t.Fatalf("unexpected first token timeout for unrelated group: %v", err)
	}
}

// TestOpenAIStreamingFirstTokenArrivedInTime 验证 TTFT 计时器在首 token 到达后被关闭，
// 后续即使 stream 持续超过 timeout 时长也不会被误杀。
func TestOpenAIStreamingFirstTokenArrivedInTime(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: newTTFTTestConfig(1)}
	c, rec := newTTFTTestContext(t, "ian_private")

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		// preamble 立刻发，首 token 在 timeout (1s) 内到达
		_, _ = pw.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"))
		// 之后再等 > timeout 才发后续/终止事件，验证 timer 已被关闭，不会误杀
		time.Sleep(1500 * time.Millisecond)
		_, _ = pw.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\" world\"}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		_, _ = pw.Write([]byte("data: [DONE]\n\n"))
		_ = pw.Close()
	}()

	res, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 1}, time.Now(), "model", "model")
	_ = pr.Close()

	if err != nil {
		t.Fatalf("expected no error when first token arrived within ttft window, got %v", err)
	}
	if res == nil || res.firstTokenMs == nil {
		t.Fatalf("expected firstTokenMs to be recorded, got result=%+v", res)
	}
	if strings.Contains(rec.Body.String(), openAIFirstTokenTimeoutErrorCode) {
		t.Fatalf("response body unexpectedly contains first_token_timeout error: %q", rec.Body.String())
	}
}

// TestOpenAIStreamingPassthroughFirstTokenTimeout 验证 passthrough 路径在 TTFT 内没有首 token 时
// 能写出 first_token_timeout SSE 事件并返回错误。
func TestOpenAIStreamingPassthroughFirstTokenTimeout(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: newTTFTTestConfig(1)}
	c, rec := newTTFTTestContext(t, "ian_private")

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		_, _ = pw.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		// 不再发任何 token，等待 timeout 触发
		time.Sleep(3 * time.Second)
		_ = pw.Close()
	}()

	_, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, &Account{ID: 1}, time.Now(), "model", "model")
	_ = pr.Close()

	if err == nil || !strings.Contains(err.Error(), "first token timeout") {
		t.Fatalf("expected passthrough first token timeout error, got %v", err)
	}
	if !strings.Contains(rec.Body.String(), openAIFirstTokenTimeoutErrorCode) {
		t.Fatalf("expected passthrough SSE error event with first_token_timeout, got %q", rec.Body.String())
	}
}

// TestOpenAIStreamingPassthroughFirstTokenArrivedInTime 验证 passthrough 路径在首 token 到达后
// 不会因后续生成时间长而被误杀。
func TestOpenAIStreamingPassthroughFirstTokenArrivedInTime(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: newTTFTTestConfig(1)}
	c, rec := newTTFTTestContext(t, "ian_private")

	pr, pw := io.Pipe()
	resp := &http.Response{StatusCode: http.StatusOK, Body: pr, Header: http.Header{}}

	go func() {
		_, _ = pw.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		_, _ = pw.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"))
		time.Sleep(1500 * time.Millisecond)
		_, _ = pw.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
		_, _ = pw.Write([]byte("data: [DONE]\n\n"))
		_ = pw.Close()
	}()

	res, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, &Account{ID: 1}, time.Now(), "model", "model")
	_ = pr.Close()

	if err != nil {
		t.Fatalf("expected no error when passthrough first token arrived within ttft window, got %v", err)
	}
	if res == nil || res.firstTokenMs == nil {
		t.Fatalf("expected passthrough firstTokenMs to be recorded, got result=%+v", res)
	}
	if strings.Contains(rec.Body.String(), openAIFirstTokenTimeoutErrorCode) {
		t.Fatalf("passthrough body unexpectedly contains first_token_timeout error: %q", rec.Body.String())
	}
}
