package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// InspectorHandler handles inspector endpoints for request log visualization.
type InspectorHandler struct {
	readerService *service.RequestLogReaderService
	pgDB          *sql.DB
}

// NewInspectorHandler creates a new InspectorHandler.
func NewInspectorHandler(readerService *service.RequestLogReaderService, pgDB *sql.DB) *InspectorHandler {
	return &InspectorHandler{readerService: readerService, pgDB: pgDB}
}

// Dates returns available log dates with record counts.
// GET /api/v1/inspector/dates
func (h *InspectorHandler) Dates(c *gin.Context) {
	dates, err := h.readerService.GetInspectorDates()
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "Failed to get dates: "+err.Error())
		return
	}
	response.Success(c, gin.H{"dates": dates})
}

// DateStats returns aggregated stats for a single date.
// GET /api/v1/inspector/dates/:date/stats
func (h *InspectorHandler) DateStats(c *gin.Context) {
	date := c.Param("date")
	stats, err := h.readerService.GetInspectorDateStats(date)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "Failed to get stats: "+err.Error())
		return
	}
	if stats == nil {
		response.Error(c, http.StatusNotFound, fmt.Sprintf("No data for %s", date))
		return
	}
	response.Success(c, stats)
}

// Logs returns filtered, paginated log entries for a date.
// GET /api/v1/inspector/dates/:date/logs
func (h *InspectorHandler) Logs(c *gin.Context) {
	date := c.Param("date")

	var filter service.InspectorLogFilter
	filter.SessionID = c.Query("session_id")
	filter.Model = c.Query("model")
	filter.Query = c.Query("q")
	filter.Order = c.Query("order") // "asc" | "desc" (default: desc, newest first)
	if v := c.Query("api_key_id"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.APIKeyID = n
		}
	}

	offset := 0
	limit := 100
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	result, err := h.readerService.QueryInspectorLogs(date, &filter, offset, limit)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "Failed to query logs: "+err.Error())
		return
	}
	response.Success(c, result)
}

// Record returns the full raw_json record by ID.
// GET /api/v1/inspector/records/:id
func (h *InspectorHandler) Record(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "Invalid record ID")
		return
	}

	detail, err := h.readerService.GetInspectorRecord(id)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "Failed to get record: "+err.Error())
		return
	}
	if detail == nil {
		response.Error(c, http.StatusNotFound, "Record not found")
		return
	}
	response.Success(c, detail)
}

// Export streams filtered log records as JSONL.
// GET /api/v1/inspector/dates/:date/export
func (h *InspectorHandler) Export(c *gin.Context) {
	date := c.Param("date")

	var filter service.InspectorLogFilter
	filter.SessionID = c.Query("session_id")
	filter.Model = c.Query("model")
	filter.Query = c.Query("q")
	filter.Order = c.Query("order")
	if v := c.Query("api_key_id"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.APIKeyID = n
		}
	}

	lines, err := h.readerService.ExportInspectorLogs(date, &filter)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "Failed to export: "+err.Error())
		return
	}

	fname := date
	if filter.SessionID != "" {
		sid := filter.SessionID
		if len(sid) > 12 {
			sid = sid[:12]
		}
		fname += "_session-" + sanitizeFilename(sid)
	}
	if filter.APIKeyID > 0 {
		fname += fmt.Sprintf("_key-%d", filter.APIKeyID)
	}
	if filter.Model != "" {
		fname += "_" + sanitizeFilename(filter.Model)
	}
	fname += ".jsonl"

	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fname))
	c.Header("Content-Type", "application/x-ndjson")
	c.Status(http.StatusOK)

	for _, line := range lines {
		c.Writer.WriteString(line)
		c.Writer.WriteString("\n")
	}
}

// Keys returns API key id->name mapping from PostgreSQL.
// GET /api/v1/inspector/keys
func (h *InspectorHandler) Keys(c *gin.Context) {
	if h.pgDB == nil {
		response.Error(c, http.StatusServiceUnavailable, "Database not available")
		return
	}

	rows, err := h.pgDB.QueryContext(c.Request.Context(),
		"SELECT id, name, LEFT(key, 12) || '...' AS key_prefix FROM api_keys ORDER BY id")
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "Failed to query keys: "+err.Error())
		return
	}
	defer rows.Close()

	type keyInfo struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		KeyPrefix string `json:"key_prefix"`
	}
	var keys []keyInfo
	for rows.Next() {
		var k keyInfo
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyPrefix); err != nil {
			continue
		}
		keys = append(keys, k)
	}

	response.Success(c, gin.H{"keys": keys})
}

func sanitizeFilename(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}
