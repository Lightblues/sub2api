package service

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// RequestLogReaderService reads and queries the request log SQLite database.
type RequestLogReaderService struct {
	cfg *config.Config
}

// NewRequestLogReaderService creates a new RequestLogReaderService.
func NewRequestLogReaderService(cfg *config.Config) *RequestLogReaderService {
	return &RequestLogReaderService{cfg: cfg}
}

func (s *RequestLogReaderService) db() *sql.DB {
	return getRequestLogDB(s.cfg)
}

// RequestLogRecord represents a single log entry.
type RequestLogRecord struct {
	Ts               int64            `json:"ts"`
	UserID           int64            `json:"user_id,omitempty"`
	APIKeyID         int64            `json:"api_key_id,omitempty"`
	RequestBody      json.RawMessage  `json:"request_body"`
	ResponseComplete json.RawMessage  `json:"response_complete,omitempty"`
	ResponseBody     *json.RawMessage `json:"response_body,omitempty"`
}

// RequestLogStats holds aggregated statistics about stored logs.
type RequestLogStats struct {
	TotalRecords int64             `json:"total_records"`
	DateRange    *DateRange        `json:"date_range,omitempty"`
	PerDate      []DateRecordCount `json:"per_date"`
	DiskUsage    int64             `json:"disk_usage_bytes"`
}

// DateRange represents the earliest and latest log dates.
type DateRange struct {
	Earliest string `json:"earliest"`
	Latest   string `json:"latest"`
}

// DateRecordCount holds the record count for a single date.
type DateRecordCount struct {
	Date      string `json:"date"`
	Records   int64  `json:"records"`
	SizeBytes int64  `json:"size_bytes"` // kept for API compat, approximated
}

// RequestLogSummary is a lightweight record summary for list views.
type RequestLogSummary struct {
	ID         int64  `json:"id"`
	ResponseID string `json:"response_id"`
	Ts         int64  `json:"ts"`
	APIKeyID   int64  `json:"api_key_id"`
	Model      string `json:"model"`
}

// RequestLogListResult holds paginated list results.
type RequestLogListResult struct {
	Records []RequestLogSummary `json:"records"`
	Total   int64               `json:"total"`
	Offset  int                 `json:"offset"`
	Limit   int                 `json:"limit"`
}

// FindByResponseID finds a record whose response contains the given response ID.
func (s *RequestLogReaderService) FindByResponseID(responseID string) (*RequestLogRecord, error) {
	db := s.db()
	if db == nil {
		return nil, fmt.Errorf("request log database not initialized")
	}

	// Use LIKE with the response ID as a substring match on raw_json.
	// This is fast enough for the typical dataset size and avoids a separate column.
	needle := fmt.Sprintf(`%%"id":"%s"%%`, responseID)
	row := db.QueryRow(
		`SELECT raw_json FROM records WHERE raw_json LIKE ? ORDER BY ts DESC LIMIT 1`,
		needle,
	)

	var rawJSON string
	if err := row.Scan(&rawJSON); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query by response_id: %w", err)
	}

	var rec RequestLogRecord
	if err := json.Unmarshal([]byte(rawJSON), &rec); err != nil {
		return nil, fmt.Errorf("unmarshal record: %w", err)
	}
	return &rec, nil
}

// ListByDate returns lightweight record summaries for a given date with pagination.
// Results are ordered newest-first (DESC).
func (s *RequestLogReaderService) ListByDate(date string, offset, limit int) (*RequestLogListResult, error) {
	db := s.db()
	if db == nil {
		return nil, fmt.Errorf("request log database not initialized")
	}

	// Get total count
	var total int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM records WHERE date = ?`, date).Scan(&total); err != nil {
		return nil, fmt.Errorf("count records: %w", err)
	}

	// Query summaries
	rows, err := db.Query(
		`SELECT id, ts, api_key_id, model, raw_json
		 FROM records WHERE date = ? ORDER BY ts DESC LIMIT ? OFFSET ?`,
		date, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("query records: %w", err)
	}
	defer rows.Close()

	records := make([]RequestLogSummary, 0, limit)
	for rows.Next() {
		var (
			id       int64
			ts       int64
			apiKeyID sql.NullInt64
			model    sql.NullString
			rawJSON  string
		)
		if err := rows.Scan(&id, &ts, &apiKeyID, &model, &rawJSON); err != nil {
			continue
		}
		summary := RequestLogSummary{
			ID:    id,
			Ts:    ts,
			Model: model.String,
		}
		if apiKeyID.Valid {
			summary.APIKeyID = apiKeyID.Int64
		}
		// Extract response_id from raw_json (lightweight parse)
		summary.ResponseID = extractResponseIDFromJSON(rawJSON)
		records = append(records, summary)
	}

	return &RequestLogListResult{
		Records: records,
		Total:   total,
		Offset:  offset,
		Limit:   limit,
	}, nil
}

// GetStats returns aggregated statistics for all log records.
func (s *RequestLogReaderService) GetStats() (*RequestLogStats, error) {
	db := s.db()
	if db == nil {
		return nil, fmt.Errorf("request log database not initialized")
	}

	rows, err := db.Query(
		`SELECT date, COUNT(*) as cnt FROM records GROUP BY date ORDER BY date`,
	)
	if err != nil {
		return nil, fmt.Errorf("query stats: %w", err)
	}
	defer rows.Close()

	stats := &RequestLogStats{
		PerDate: make([]DateRecordCount, 0),
	}

	for rows.Next() {
		var date string
		var count int64
		if err := rows.Scan(&date, &count); err != nil {
			continue
		}
		stats.TotalRecords += count
		stats.PerDate = append(stats.PerDate, DateRecordCount{
			Date:    date,
			Records: count,
		})
	}

	if len(stats.PerDate) > 0 {
		stats.DateRange = &DateRange{
			Earliest: stats.PerDate[0].Date,
			Latest:   stats.PerDate[len(stats.PerDate)-1].Date,
		}
	}

	return stats, nil
}

// extractResponseIDFromJSON extracts the response ID from raw JSON without full parsing.
func extractResponseIDFromJSON(rawJSON string) string {
	// Try OpenAI format: response_complete.response.id
	var wrapper struct {
		ResponseComplete struct {
			Response struct {
				ID string `json:"id"`
			} `json:"response"`
		} `json:"response_complete"`
	}
	if json.Unmarshal([]byte(rawJSON), &wrapper) == nil && wrapper.ResponseComplete.Response.ID != "" {
		return wrapper.ResponseComplete.Response.ID
	}
	// Try response_body.id (non-streaming)
	var direct struct {
		ResponseBody struct {
			ID string `json:"id"`
		} `json:"response_body"`
	}
	if json.Unmarshal([]byte(rawJSON), &direct) == nil && direct.ResponseBody.ID != "" {
		return direct.ResponseBody.ID
	}
	return ""
}
