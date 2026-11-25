package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"research-platform/backend/internal/db"
	"research-platform/backend/internal/models"
)

func (s *Server) handleListAuditLogs(w http.ResponseWriter, r *http.Request, u *models.User) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 500 {
		pageSize = 500
	}

	parseTime := func(key string) (*time.Time, error) {
		raw := strings.TrimSpace(q.Get(key))
		if raw == "" {
			return nil, nil
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, err
		}
		return &t, nil
	}
	from, err := parseTime("from")
	if err != nil {
		writeError(w, http.StatusBadRequest, "起始时间格式无效")
		return
	}
	to, err := parseTime("to")
	if err != nil {
		writeError(w, http.StatusBadRequest, "结束时间格式无效")
		return
	}

	clip := func(v string, n int) string {
		v = strings.TrimSpace(v)
		if len(v) > n {
			return v[:n]
		}
		return v
	}
	filter := db.AuditLogFilter{
		Username: clip(q.Get("username"), 120),
		Action:   clip(q.Get("action"), 120),
		Keyword:  clip(q.Get("keyword"), 200),
		From:     from,
		To:       to,
		Page:     page,
		PageSize: pageSize,
	}
	logs, total, err := s.db.ListAuditLogs(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":     logs,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (s *Server) handleDeleteAuditLogs(w http.ResponseWriter, r *http.Request, u *models.User) {
	q := r.URL.Query()
	days := 7
	if raw := strings.TrimSpace(q.Get("days")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "days 必须是整数")
			return
		}
		days = parsed
	}
	if days < 1 || days > 3650 {
		writeError(w, http.StatusBadRequest, "days 必须在 1 到 3650 之间")
		return
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	dryRun := strings.EqualFold(q.Get("dry_run"), "1") || strings.EqualFold(q.Get("dry_run"), "true")

	if dryRun {
		count, err := s.db.CountAuditLogsBefore(r.Context(), cutoff)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"days": days, "cutoff": cutoff, "count": count, "dry_run": true,
		})
		return
	}

	deleted, err := s.db.DeleteAuditLogsBefore(r.Context(), cutoff)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	detail := "删除 " + strconv.Itoa(int(deleted)) + " 条，截止 " + cutoff.Format("2006-01-02 15:04:05")
	s.db.Audit(r.Context(), u.ID, u.Username, "delete_audit_logs", strconv.Itoa(days)+" 天以前的日志", detail)
	writeJSON(w, http.StatusOK, map[string]any{
		"days": days, "cutoff": cutoff, "deleted": deleted,
	})
}
