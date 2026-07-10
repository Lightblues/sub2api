package service

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"

	_ "modernc.org/sqlite"
)

var (
	requestLogDB   *sql.DB
	requestLogOnce sync.Once
)

// [custom] Gin context key under which handleNonStreamingResponse stashes the raw upstream response body.
// The caller reads it out after handleNonStreamingResponse returns to feed into the async logger.
const requestLogRawResponseCtxKey = "request_log.raw_response"

// [custom] originalRequestBodyFromContext returns the pre-mutation request body stashed by Forward().
// May be nil if request_log wasn't enabled at the time or if the body was never captured.
func originalRequestBodyFromContext(c *gin.Context) []byte {
	if c == nil {
		return nil
	}
	if v, ok := c.Get("request_log.original_body"); ok {
		if b, ok := v.([]byte); ok {
			return b
		}
	}
	return nil
}

// [custom] buildAccumulatorSnapshot returns the reconstructed final response JSON,
// or nil if the accumulator was disabled (request_log off / not enabled for this request).
func buildAccumulatorSnapshot(a *anthropicResponseAccumulator) []byte {
	if a == nil {
		return nil
	}
	return a.Build()
}

// [custom] stashedRawResponseFromContext returns the raw upstream response body stashed by
// handleNonStreamingResponse (or its Anthropic passthrough / OpenAI counterparts).
// Returned slice may alias into gin.Context and should not be mutated.
func stashedRawResponseFromContext(c *gin.Context) []byte {
	if c == nil {
		return nil
	}
	if v, ok := c.Get(requestLogRawResponseCtxKey); ok {
		if b, ok := v.([]byte); ok {
			return b
		}
	}
	return nil
}

// requestLogRecord is the in-memory representation before DB insertion.
type requestLogRecord struct {
	Ts               int64             `json:"ts"`
	UserID           int64             `json:"user_id,omitempty"`
	APIKeyID         int64             `json:"api_key_id,omitempty"`
	RequestHeaders   map[string]string `json:"request_headers,omitempty"`
	RequestBody      json.RawMessage   `json:"request_body"`
	ResponseComplete json.RawMessage   `json:"response_complete,omitempty"`
	ResponseBody     *json.RawMessage  `json:"response_body,omitempty"`
}

// headersExcludeFromLog are headers that must NOT be logged (sensitive auth data).
var headersExcludeFromLog = map[string]bool{
	"x-api-key":     true,
	"authorization": true,
	"cookie":        true,
	"set-cookie":    true,
}

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

const requestLogSchema = `
CREATE TABLE IF NOT EXISTS records (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    date             TEXT    NOT NULL,
    ts               INTEGER NOT NULL,
    api_key_id       INTEGER,
    model            TEXT,
    session_type     TEXT,
    session_id       TEXT,
    status           TEXT,
    input_tokens     INTEGER DEFAULT 0,
    output_tokens    INTEGER DEFAULT 0,
    cached_tokens    INTEGER DEFAULT 0,
    reasoning_tokens INTEGER DEFAULT 0,
    total_tokens     INTEGER DEFAULT 0,
    raw_json         TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_records_date_ts    ON records(date, ts);
CREATE INDEX IF NOT EXISTS idx_records_date_model ON records(date, model);
CREATE INDEX IF NOT EXISTS idx_records_date_key   ON records(date, api_key_id);
CREATE INDEX IF NOT EXISTS idx_records_session    ON records(session_id);
`

// ---------------------------------------------------------------------------
// DB initialization
// ---------------------------------------------------------------------------

func getRequestLogDB(cfg *config.Config) *sql.DB {
	requestLogOnce.Do(func() {
		dbPath := cfg.Gateway.RequestLog.DbPath
		if dbPath == "" {
			dir := cfg.Gateway.RequestLog.Dir
			if dir == "" {
				dir = "data/request_logs"
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				zap.L().Error("request_log: mkdir failed", zap.Error(err))
				return
			}
			dbPath = filepath.Join(dir, "request_log.db")
		}

		db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=30000&_synchronous=NORMAL")
		if err != nil {
			zap.L().Error("request_log: open db failed", zap.String("path", dbPath), zap.Error(err))
			return
		}
		// WAL mode allows concurrent reads from inspector
		db.SetMaxOpenConns(2)
		if _, err := db.Exec(requestLogSchema); err != nil {
			zap.L().Error("request_log: create schema failed", zap.Error(err))
			db.Close()
			return
		}
		requestLogDB = db
		zap.L().Info("request_log: SQLite initialized", zap.String("path", dbPath))
	})
	return requestLogDB
}

// ---------------------------------------------------------------------------
// Field extraction helpers
// ---------------------------------------------------------------------------

// extractRequestHeaders captures all request headers except sensitive auth ones.
func extractRequestHeaders(c *gin.Context) map[string]string {
	if c == nil || c.Request == nil {
		return nil
	}
	h := make(map[string]string, len(c.Request.Header))
	for k, vals := range c.Request.Header {
		lk := strings.ToLower(k)
		if headersExcludeFromLog[lk] || len(vals) == 0 {
			continue
		}
		h[lk] = vals[0]
	}
	if len(h) == 0 {
		return nil
	}
	return h
}

// extractSessionFromHeaders extracts session info from request headers and body.
// Returns (sessionType, sessionID).
func extractSessionFromRecord(rec *requestLogRecord) (string, string) {
	h := rec.RequestHeaders
	if h != nil {
		// Codex: direct header
		if sid, ok := h["session_id"]; ok && sid != "" {
			return "codex", sid
		}
		// Claude Code: dedicated header
		if sid, ok := h["x-claude-code-session-id"]; ok && sid != "" {
			return "claude-code", sid
		}
	}
	// Claude Code: from metadata.user_id JSON string in request body
	if len(rec.RequestBody) > 0 {
		var body struct {
			Metadata struct {
				UserID string `json:"user_id"`
			} `json:"metadata"`
		}
		if json.Unmarshal(rec.RequestBody, &body) == nil && body.Metadata.UserID != "" {
			uid := body.Metadata.UserID
			if strings.Contains(uid, "{") {
				var parsed struct {
					SessionID string `json:"session_id"`
				}
				if json.Unmarshal([]byte(uid), &parsed) == nil && parsed.SessionID != "" {
					return "claude-code", parsed.SessionID
				}
			}
		}
	}
	return "", ""
}

// extractModelFromRecord gets the model from request body or response.
func extractModelFromRecord(rec *requestLogRecord) string {
	if len(rec.RequestBody) > 0 {
		var body struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(rec.RequestBody, &body) == nil && body.Model != "" {
			return body.Model
		}
	}
	if len(rec.ResponseComplete) > 0 {
		// OpenAI format: response_complete.response.model
		var resp struct {
			Response struct {
				Model string `json:"model"`
			} `json:"response"`
		}
		if json.Unmarshal(rec.ResponseComplete, &resp) == nil && resp.Response.Model != "" {
			return resp.Response.Model
		}
	}
	if rec.ResponseBody != nil && len(*rec.ResponseBody) > 0 {
		var resp struct {
			Model string `json:"model"`
		}
		if json.Unmarshal(*rec.ResponseBody, &resp) == nil && resp.Model != "" {
			return resp.Model
		}
	}
	return ""
}

// extractUsageFromRecord gets token usage from the response.
func extractUsageFromRecord(rec *requestLogRecord) (inputTokens, outputTokens, cachedTokens, reasoningTokens, totalTokens int64) {
	var data json.RawMessage
	if len(rec.ResponseComplete) > 0 {
		data = rec.ResponseComplete
	} else if rec.ResponseBody != nil {
		data = *rec.ResponseBody
	}
	if len(data) == 0 {
		return
	}

	// Try OpenAI format: response_complete.response.usage
	var openai struct {
		Response struct {
			Usage struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
				TotalTokens  int64 `json:"total_tokens"`
				InputTokensDetails struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"input_tokens_details"`
				OutputTokensDetails struct {
					ReasoningTokens int64 `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &openai) == nil && openai.Response.Usage.TotalTokens > 0 {
		return openai.Response.Usage.InputTokens,
			openai.Response.Usage.OutputTokens,
			openai.Response.Usage.InputTokensDetails.CachedTokens,
			openai.Response.Usage.OutputTokensDetails.ReasoningTokens,
			openai.Response.Usage.TotalTokens
	}

	// Try Anthropic format: usage at top level
	var anthropic struct {
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
			CacheRead    int64 `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(data, &anthropic) == nil && (anthropic.Usage.InputTokens > 0 || anthropic.Usage.OutputTokens > 0) {
		total := anthropic.Usage.InputTokens + anthropic.Usage.OutputTokens
		return anthropic.Usage.InputTokens, anthropic.Usage.OutputTokens, anthropic.Usage.CacheRead, 0, total
	}

	return
}

// extractStatusFromRecord gets the response status.
func extractStatusFromRecord(rec *requestLogRecord) string {
	if len(rec.ResponseComplete) > 0 {
		var resp struct {
			Response struct {
				Status string `json:"status"`
			} `json:"response"`
		}
		if json.Unmarshal(rec.ResponseComplete, &resp) == nil && resp.Response.Status != "" {
			return resp.Response.Status
		}
	}
	if rec.ResponseBody != nil && len(*rec.ResponseBody) > 0 {
		var resp struct {
			StopReason string `json:"stop_reason"`
		}
		if json.Unmarshal(*rec.ResponseBody, &resp) == nil && resp.StopReason != "" {
			return resp.StopReason
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Config checks
// ---------------------------------------------------------------------------

func requestLogCfgEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.Gateway.RequestLog.Enabled
}

func isGroupExcludedFromLog(c *gin.Context, cfg *config.Config) bool {
	if c == nil || cfg == nil {
		return false
	}
	excluded := cfg.Gateway.RequestLog.ExcludedGroups
	if len(excluded) == 0 {
		return false
	}
	group, ok := c.Request.Context().Value(ctxkey.Group).(*Group)
	if !ok || group == nil {
		return false
	}
	for _, name := range excluded {
		if name == group.Name {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// DB write
// ---------------------------------------------------------------------------

const insertRecordSQL = `
INSERT INTO records (date, ts, api_key_id, model, session_type, session_id,
                     status, input_tokens, output_tokens, cached_tokens,
                     reasoning_tokens, total_tokens, raw_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`

// writeRequestLogRecord writes a pre-built requestLogRecord to the SQLite database.
func writeRequestLogRecord(c *gin.Context, cfg *config.Config, rec requestLogRecord) {
	db := getRequestLogDB(cfg)
	if db == nil {
		return
	}

	rawJSON, err := json.Marshal(rec)
	if err != nil {
		logger.FromContext(c.Request.Context()).Warn("request_log: marshal failed", zap.Error(err))
		return
	}

	date := time.Unix(rec.Ts, 0).Format("2006-01-02")
	model := extractModelFromRecord(&rec)
	sessionType, sessionID := extractSessionFromRecord(&rec)
	status := extractStatusFromRecord(&rec)
	inputTokens, outputTokens, cachedTokens, reasoningTokens, totalTokens := extractUsageFromRecord(&rec)

	_, err = db.Exec(insertRecordSQL,
		date, rec.Ts, rec.APIKeyID, model, sessionType, sessionID,
		status, inputTokens, outputTokens, cachedTokens, reasoningTokens, totalTokens,
		string(rawJSON),
	)
	if err != nil {
		logger.FromContext(c.Request.Context()).Warn("request_log: db insert failed", zap.Error(err))
	}
}

// ---------------------------------------------------------------------------
// OpenAIGatewayService methods
// ---------------------------------------------------------------------------

func (s *OpenAIGatewayService) requestLogEnabled() bool {
	return requestLogCfgEnabled(s.cfg)
}

func (s *OpenAIGatewayService) shouldLogRequest(c *gin.Context) bool {
	return s.requestLogEnabled() && !isGroupExcludedFromLog(c, s.cfg)
}

func (s *OpenAIGatewayService) writeRequestLog(c *gin.Context, requestBody, responseData []byte) {
	if len(requestBody) == 0 && len(responseData) == 0 {
		return
	}
	rec := requestLogRecord{
		Ts:             time.Now().Unix(),
		APIKeyID:       getAPIKeyIDFromContext(c),
		RequestHeaders: extractRequestHeaders(c),
		RequestBody:    json.RawMessage(requestBody),
	}
	if len(responseData) > 0 {
		rec.ResponseComplete = json.RawMessage(responseData)
	}
	writeRequestLogRecord(c, s.cfg, rec)
}

func (s *OpenAIGatewayService) writeRequestLogNonStreaming(c *gin.Context, requestBody, responseBody []byte) {
	if len(requestBody) == 0 && len(responseBody) == 0 {
		return
	}
	resp := json.RawMessage(responseBody)
	rec := requestLogRecord{
		Ts:             time.Now().Unix(),
		APIKeyID:       getAPIKeyIDFromContext(c),
		RequestHeaders: extractRequestHeaders(c),
		RequestBody:    json.RawMessage(requestBody),
		ResponseBody:   &resp,
	}
	writeRequestLogRecord(c, s.cfg, rec)
}

// ---------------------------------------------------------------------------
// GatewayService methods (Anthropic /v1/messages)
// ---------------------------------------------------------------------------

func (s *GatewayService) requestLogEnabled() bool {
	return requestLogCfgEnabled(s.cfg)
}

func (s *GatewayService) shouldLogRequest(c *gin.Context) bool {
	return s.requestLogEnabled() && !isGroupExcludedFromLog(c, s.cfg)
}

func (s *GatewayService) writeAnthropicRequestLog(c *gin.Context, requestBody, responseData []byte) {
	if len(requestBody) == 0 && len(responseData) == 0 {
		return
	}
	rec := requestLogRecord{
		Ts:             time.Now().Unix(),
		APIKeyID:       getAPIKeyIDFromContext(c),
		RequestHeaders: extractRequestHeaders(c),
		RequestBody:    json.RawMessage(requestBody),
	}
	if len(responseData) > 0 {
		rec.ResponseComplete = json.RawMessage(responseData)
	}
	writeRequestLogRecord(c, s.cfg, rec)
}

func (s *GatewayService) writeAnthropicRequestLogNonStreaming(c *gin.Context, requestBody, responseBody []byte) {
	if len(requestBody) == 0 && len(responseBody) == 0 {
		return
	}
	resp := json.RawMessage(responseBody)
	rec := requestLogRecord{
		Ts:             time.Now().Unix(),
		APIKeyID:       getAPIKeyIDFromContext(c),
		RequestHeaders: extractRequestHeaders(c),
		RequestBody:    json.RawMessage(requestBody),
		ResponseBody:   &resp,
	}
	writeRequestLogRecord(c, s.cfg, rec)
}
