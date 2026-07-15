package service

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Inspector-specific types
// ---------------------------------------------------------------------------

type InspectorDateEntry struct {
	Date     string `json:"date"`
	Records  int64  `json:"records"`
	Archived bool   `json:"archived"`
}

type InspectorDateStats struct {
	Date          string                    `json:"date"`
	TotalRequests int64                     `json:"total_requests"`
	ByModel       map[string]int64          `json:"by_model"`
	ByAPIKeyID    map[string]int64          `json:"by_api_key_id"`
	Sessions      []InspectorSessionSummary `json:"sessions"`
	Tokens        InspectorTokenSummary     `json:"tokens"`
}

type InspectorSessionSummary struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Count int64  `json:"count"`
}

type InspectorTokenSummary struct {
	TotalInput    int64 `json:"total_input"`
	TotalOutput   int64 `json:"total_output"`
	TotalCached   int64 `json:"total_cached"`
	TotalReasoning int64 `json:"total_reasoning"`
	Total         int64 `json:"total"`
}

type InspectorLogEntry struct {
	ID              int64                   `json:"id"`
	Ts              int64                   `json:"ts"`
	Time            string                  `json:"time"`
	APIKeyID        int64                   `json:"api_key_id"`
	Model           string                  `json:"model"`
	Session         *InspectorSessionRef    `json:"session"`
	Status          string                  `json:"status"`
	InputTokens     int64                   `json:"input_tokens"`
	OutputTokens    int64                   `json:"output_tokens"`
	CachedTokens    int64                   `json:"cached_tokens"`
	ReasoningTokens int64                   `json:"reasoning_tokens"`
	TotalTokens     int64                   `json:"total_tokens"`
}

type InspectorSessionRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type InspectorLogFilter struct {
	SessionID string
	APIKeyID  int64
	Model     string
	Query     string
	// Order controls result ordering by ts. Accepts "asc" or "desc"; empty
	// defaults to "desc" (newest first) — the common inspection case.
	Order string
}

// sortDirection returns the SQL direction fragment (ASC / DESC) for f.Order.
// Any value other than an explicit "asc" is treated as DESC so the default and
// unrecognised inputs both surface newest-first.
func (f *InspectorLogFilter) sortDirection() string {
	if f != nil && strings.EqualFold(strings.TrimSpace(f.Order), "asc") {
		return "ASC"
	}
	return "DESC"
}

type InspectorLogResult struct {
	Date         string               `json:"date"`
	TotalMatched int64                `json:"total_matched"`
	Offset       int                  `json:"offset"`
	Limit        int                  `json:"limit"`
	Data         []InspectorLogEntry  `json:"data"`
}

type InspectorRecordDetail struct {
	Record  json.RawMessage         `json:"record"`
	Summary InspectorRecordSummary  `json:"summary"`
}

type InspectorRecordSummary struct {
	ID              int64                `json:"id"`
	Ts              int64                `json:"ts"`
	Time            string               `json:"time"`
	APIKeyID        int64                `json:"api_key_id"`
	Model           string               `json:"model"`
	Session         *InspectorSessionRef `json:"session"`
	UserAgent       string               `json:"user_agent"`
	Status          string               `json:"status"`
	InputTokens     int64                `json:"input_tokens"`
	OutputTokens    int64                `json:"output_tokens"`
	CachedTokens    int64                `json:"cached_tokens"`
	ReasoningTokens int64                `json:"reasoning_tokens"`
	TotalTokens     int64                `json:"total_tokens"`
}

// ---------------------------------------------------------------------------
// Archive DB management
// ---------------------------------------------------------------------------

var (
	archiveConns   = make(map[string]*sql.DB)
	archiveConnsMu sync.Mutex
)

func (s *RequestLogReaderService) archiveDir() string {
	return s.cfg.Gateway.RequestLog.ArchiveDir
}

func (s *RequestLogReaderService) openArchiveDB(date string) *sql.DB {
	dir := s.archiveDir()
	if dir == "" {
		return nil
	}

	archiveConnsMu.Lock()
	defer archiveConnsMu.Unlock()

	if db, ok := archiveConns[date]; ok {
		return db
	}

	dbPath := filepath.Join(dir, date+".db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}

	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil
	}
	db.SetMaxOpenConns(1)
	db.Exec("PRAGMA busy_timeout=30000")
	archiveConns[date] = db
	return db
}

func (s *RequestLogReaderService) resolveDB(date string) *sql.DB {
	mainDB := s.db()
	if mainDB == nil {
		return nil
	}

	var cnt int64
	if err := mainDB.QueryRow("SELECT COUNT(*) FROM records WHERE date = ?", date).Scan(&cnt); err == nil && cnt > 0 {
		return mainDB
	}

	if archiveDB := s.openArchiveDB(date); archiveDB != nil {
		return archiveDB
	}
	return mainDB
}

func (s *RequestLogReaderService) listArchiveDates() []InspectorDateEntry {
	dir := s.archiveDir()
	if dir == "" {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var dates []InspectorDateEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		dateStr := strings.TrimSuffix(e.Name(), ".db")
		if _, err := time.Parse("2006-01-02", dateStr); err != nil {
			continue
		}
		adb := s.openArchiveDB(dateStr)
		if adb == nil {
			continue
		}
		var cnt int64
		if err := adb.QueryRow("SELECT COUNT(*) FROM records").Scan(&cnt); err == nil {
			dates = append(dates, InspectorDateEntry{
				Date:     dateStr,
				Records:  cnt,
				Archived: true,
			})
		}
	}
	return dates
}

// ---------------------------------------------------------------------------
// Inspector query methods
// ---------------------------------------------------------------------------

// GetInspectorDates returns available log dates with record counts (main + archive).
func (s *RequestLogReaderService) GetInspectorDates() ([]InspectorDateEntry, error) {
	mainDB := s.db()
	if mainDB == nil {
		return nil, fmt.Errorf("request log database not initialized")
	}

	rows, err := mainDB.Query("SELECT date, COUNT(*) as cnt FROM records GROUP BY date ORDER BY date DESC")
	if err != nil {
		return nil, fmt.Errorf("query dates: %w", err)
	}
	defer rows.Close()

	mainDates := make(map[string]bool)
	var results []InspectorDateEntry
	for rows.Next() {
		var date string
		var cnt int64
		if err := rows.Scan(&date, &cnt); err != nil {
			continue
		}
		mainDates[date] = true
		results = append(results, InspectorDateEntry{Date: date, Records: cnt, Archived: false})
	}

	for _, ad := range s.listArchiveDates() {
		if !mainDates[ad.Date] {
			results = append(results, ad)
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Date > results[j].Date })
	return results, nil
}

// GetInspectorDateStats returns aggregated stats for a single date.
func (s *RequestLogReaderService) GetInspectorDateStats(date string) (*InspectorDateStats, error) {
	db := s.resolveDB(date)
	if db == nil {
		return nil, fmt.Errorf("request log database not initialized")
	}

	rows, err := db.Query(
		`SELECT model, api_key_id, session_type, session_id,
		        input_tokens, output_tokens, cached_tokens, reasoning_tokens, total_tokens
		 FROM records WHERE date = ? ORDER BY ts`, date,
	)
	if err != nil {
		return nil, fmt.Errorf("query date stats: %w", err)
	}
	defer rows.Close()

	stats := &InspectorDateStats{
		Date:       date,
		ByModel:    make(map[string]int64),
		ByAPIKeyID: make(map[string]int64),
	}

	type sessionKey struct {
		typ string
		id  string
	}
	sessions := make(map[sessionKey]int64)

	for rows.Next() {
		var (
			model       sql.NullString
			apiKeyID    sql.NullInt64
			sessionType sql.NullString
			sessionID   sql.NullString
			inputTok    sql.NullInt64
			outputTok   sql.NullInt64
			cachedTok   sql.NullInt64
			reasonTok   sql.NullInt64
			totalTok    sql.NullInt64
		)
		if err := rows.Scan(&model, &apiKeyID, &sessionType, &sessionID,
			&inputTok, &outputTok, &cachedTok, &reasonTok, &totalTok); err != nil {
			continue
		}

		stats.TotalRequests++

		m := model.String
		if m == "" {
			m = "unknown"
		}
		stats.ByModel[m]++

		if apiKeyID.Valid {
			stats.ByAPIKeyID[fmt.Sprintf("%d", apiKeyID.Int64)]++
		}

		if sessionID.Valid && sessionID.String != "" {
			sk := sessionKey{typ: sessionType.String, id: sessionID.String}
			sessions[sk]++
		}

		stats.Tokens.TotalInput += inputTok.Int64
		stats.Tokens.TotalOutput += outputTok.Int64
		stats.Tokens.TotalCached += cachedTok.Int64
		stats.Tokens.TotalReasoning += reasonTok.Int64
		stats.Tokens.Total += totalTok.Int64
	}

	for sk, cnt := range sessions {
		stats.Sessions = append(stats.Sessions, InspectorSessionSummary{
			Type: sk.typ, ID: sk.id, Count: cnt,
		})
	}
	sort.Slice(stats.Sessions, func(i, j int) bool {
		return stats.Sessions[i].Count > stats.Sessions[j].Count
	})

	if stats.TotalRequests == 0 {
		return nil, nil
	}
	return stats, nil
}

func likeEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func buildInspectorWhere(date string, f *InspectorLogFilter) (string, []any) {
	clauses := []string{"date = ?"}
	params := []any{date}

	if f != nil {
		if f.SessionID != "" {
			clauses = append(clauses, `session_id LIKE ? ESCAPE '\'`)
			params = append(params, "%"+likeEscape(f.SessionID)+"%")
		}
		if f.APIKeyID > 0 {
			clauses = append(clauses, "api_key_id = ?")
			params = append(params, f.APIKeyID)
		}
		if f.Model != "" {
			clauses = append(clauses, "model = ?")
			params = append(params, f.Model)
		}
		if f.Query != "" {
			clauses = append(clauses, `raw_json LIKE ? ESCAPE '\'`)
			params = append(params, "%"+likeEscape(f.Query)+"%")
		}
	}

	return strings.Join(clauses, " AND "), params
}

// QueryInspectorLogs returns filtered, paginated log entries for a date.
func (s *RequestLogReaderService) QueryInspectorLogs(date string, f *InspectorLogFilter, offset, limit int) (*InspectorLogResult, error) {
	db := s.resolveDB(date)
	if db == nil {
		return nil, fmt.Errorf("request log database not initialized")
	}

	where, params := buildInspectorWhere(date, f)

	var total int64
	if err := db.QueryRow("SELECT COUNT(*) FROM records WHERE "+where, params...).Scan(&total); err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}

	queryParams := append(params, limit, offset)
	rows, err := db.Query(
		`SELECT id, ts, api_key_id, model, session_type, session_id,
		        status, input_tokens, output_tokens, cached_tokens,
		        reasoning_tokens, total_tokens
		 FROM records WHERE `+where+` ORDER BY ts `+f.sortDirection()+` LIMIT ? OFFSET ?`, queryParams...,
	)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer rows.Close()

	var data []InspectorLogEntry
	for rows.Next() {
		var (
			id          int64
			ts          int64
			apiKeyID    sql.NullInt64
			model       sql.NullString
			sessionType sql.NullString
			sessionID   sql.NullString
			status      sql.NullString
			inputTok    sql.NullInt64
			outputTok   sql.NullInt64
			cachedTok   sql.NullInt64
			reasonTok   sql.NullInt64
			totalTok    sql.NullInt64
		)
		if err := rows.Scan(&id, &ts, &apiKeyID, &model, &sessionType, &sessionID,
			&status, &inputTok, &outputTok, &cachedTok, &reasonTok, &totalTok); err != nil {
			continue
		}

		entry := InspectorLogEntry{
			ID:              id,
			Ts:              ts,
			Time:            time.Unix(ts, 0).Format("15:04:05"),
			APIKeyID:        apiKeyID.Int64,
			Model:           model.String,
			Status:          status.String,
			InputTokens:     inputTok.Int64,
			OutputTokens:    outputTok.Int64,
			CachedTokens:    cachedTok.Int64,
			ReasoningTokens: reasonTok.Int64,
			TotalTokens:     totalTok.Int64,
		}
		if sessionID.Valid && sessionID.String != "" {
			entry.Session = &InspectorSessionRef{Type: sessionType.String, ID: sessionID.String}
		}
		data = append(data, entry)
	}

	return &InspectorLogResult{
		Date:         date,
		TotalMatched: total,
		Offset:       offset,
		Limit:        limit,
		Data:         data,
	}, nil
}

// GetInspectorRecord returns the full raw_json record by SQLite PK.
func (s *RequestLogReaderService) GetInspectorRecord(recordID int64) (*InspectorRecordDetail, error) {
	mainDB := s.db()
	if mainDB == nil {
		return nil, fmt.Errorf("request log database not initialized")
	}

	// Try main DB first, then scan archive DBs
	detail, err := s.getRecordFromDB(mainDB, recordID)
	if err != nil {
		return nil, err
	}
	if detail != nil {
		return detail, nil
	}

	// Not found in main — try archive DBs (scan cached connections)
	archiveConnsMu.Lock()
	conns := make(map[string]*sql.DB, len(archiveConns))
	for k, v := range archiveConns {
		conns[k] = v
	}
	archiveConnsMu.Unlock()

	for _, adb := range conns {
		detail, err := s.getRecordFromDB(adb, recordID)
		if err != nil {
			continue
		}
		if detail != nil {
			return detail, nil
		}
	}

	return nil, nil
}

func (s *RequestLogReaderService) getRecordFromDB(db *sql.DB, recordID int64) (*InspectorRecordDetail, error) {
	var rawJSON string
	err := db.QueryRow("SELECT raw_json FROM records WHERE id = ?", recordID).Scan(&rawJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query record: %w", err)
	}

	// Parse for summary extraction
	var rec struct {
		Ts             int64             `json:"ts"`
		APIKeyID       int64             `json:"api_key_id"`
		RequestHeaders map[string]string `json:"request_headers"`
		RequestBody    json.RawMessage   `json:"request_body"`
		ResponseComplete json.RawMessage `json:"response_complete"`
	}
	json.Unmarshal([]byte(rawJSON), &rec)

	// Extract model from request_body or response
	var body struct {
		Model string `json:"model"`
	}
	json.Unmarshal(rec.RequestBody, &body)
	model := body.Model
	if model == "" {
		var resp struct {
			Response struct {
				Model string `json:"model"`
			} `json:"response"`
		}
		json.Unmarshal(rec.ResponseComplete, &resp)
		model = resp.Response.Model
	}

	// Extract usage from response
	var respData struct {
		Response struct {
			Status string `json:"status"`
			Usage  struct {
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
	json.Unmarshal(rec.ResponseComplete, &respData)

	// Extract session
	var session *InspectorSessionRef
	if sid, ok := rec.RequestHeaders["session_id"]; ok && sid != "" {
		session = &InspectorSessionRef{Type: "codex", ID: sid}
	} else if ccSid, ok := rec.RequestHeaders["x-claude-code-session-id"]; ok && ccSid != "" {
		session = &InspectorSessionRef{Type: "claude-code", ID: ccSid}
	}

	ua := rec.RequestHeaders["user-agent"]
	if len(ua) > 80 {
		ua = ua[:80]
	}

	timeStr := ""
	if rec.Ts > 0 {
		timeStr = time.Unix(rec.Ts, 0).Format("15:04:05")
	}

	return &InspectorRecordDetail{
		Record: json.RawMessage(rawJSON),
		Summary: InspectorRecordSummary{
			ID:              recordID,
			Ts:              rec.Ts,
			Time:            timeStr,
			APIKeyID:        rec.APIKeyID,
			Model:           model,
			Session:         session,
			UserAgent:       ua,
			Status:          respData.Response.Status,
			InputTokens:     respData.Response.Usage.InputTokens,
			OutputTokens:    respData.Response.Usage.OutputTokens,
			CachedTokens:    respData.Response.Usage.InputTokensDetails.CachedTokens,
			ReasoningTokens: respData.Response.Usage.OutputTokensDetails.ReasoningTokens,
			TotalTokens:     respData.Response.Usage.TotalTokens,
		},
	}, nil
}

// ExportInspectorLogs streams raw_json lines for a date with filters.
func (s *RequestLogReaderService) ExportInspectorLogs(date string, f *InspectorLogFilter) ([]string, error) {
	db := s.resolveDB(date)
	if db == nil {
		return nil, fmt.Errorf("request log database not initialized")
	}

	where, params := buildInspectorWhere(date, f)
	rows, err := db.Query("SELECT raw_json FROM records WHERE "+where+" ORDER BY ts "+f.sortDirection(), params...)
	if err != nil {
		return nil, fmt.Errorf("export: %w", err)
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var rawJSON string
		if err := rows.Scan(&rawJSON); err != nil {
			continue
		}
		lines = append(lines, rawJSON)
	}
	return lines, nil
}
