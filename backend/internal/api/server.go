package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"research-platform/backend/internal/auth"
	"research-platform/backend/internal/config"
	"research-platform/backend/internal/db"
	"research-platform/backend/internal/kube"
	"research-platform/backend/internal/models"
)

type Server struct {
	db    *db.DB
	kube  *kube.Client
	auth  *auth.Manager
	cfg   config.Config
	webFS http.Handler
}

func New(database *db.DB, kc *kube.Client, am *auth.Manager, cfg config.Config) *Server {
	s := &Server{db: database, kube: kc, auth: am, cfg: cfg}
	webDir := os.Getenv("WEB_DIR")
	if webDir == "" {
		webDir = "./web"
	}
	s.webFS = spaHandler(webDir)
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.db.Ping(r.Context()); err != nil {
			writeError(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "time": time.Now()})
	})
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)

	mux.HandleFunc("GET /api/me", s.withAuth(s.handleMe))
	mux.HandleFunc("POST /api/me/password", s.withAuth(s.handleChangePassword))

	mux.HandleFunc("GET /api/templates", s.withAuth(s.handleListTemplates))

	mux.HandleFunc("GET /api/environments", s.withAuth(s.handleListEnvironments))
	mux.HandleFunc("POST /api/environments", s.withAuth(s.handleCreateEnvironment))
	mux.HandleFunc("GET /api/environments/{id}", s.withAuth(s.handleGetEnvironment))
	mux.HandleFunc("PATCH /api/environments/{id}", s.withAuth(s.handleUpdateEnvironment))
	mux.HandleFunc("DELETE /api/environments/{id}", s.withAuth(s.handleDeleteEnvironment))
	mux.HandleFunc("POST /api/environments/{id}/stop", s.withAuth(s.handleStopEnvironment))
	mux.HandleFunc("POST /api/environments/{id}/start", s.withAuth(s.handleStartEnvironment))
	mux.HandleFunc("POST /api/environments/{id}/restart", s.withAuth(s.handleRestartEnvironment))
	mux.HandleFunc("GET /api/environments/{id}/webssh", s.handleWebSSH)
	mux.HandleFunc("GET /api/environments/{id}/snapshots", s.withAuth(s.handleListSnapshots))
	mux.HandleFunc("POST /api/environments/{id}/snapshots", s.withAuth(s.handleCreateSnapshot))
	mux.HandleFunc("DELETE /api/environments/{id}/snapshots/{sid}", s.withAuth(s.handleDeleteSnapshot))
	mux.HandleFunc("POST /api/environments/{id}/snapshots/{sid}/restore", s.withAuth(s.handleRestoreSnapshot))

	mux.HandleFunc("GET /api/units/{id}", s.withAuth(s.handleGetUnit))
	mux.HandleFunc("GET /api/units/{id}/members", s.withAuth(s.handleListMembers))
	mux.HandleFunc("POST /api/units/{id}/members", s.withAuth(s.handleCreateMember))
	mux.HandleFunc("PATCH /api/units/{id}/members/{uid}", s.withAuth(s.handleUpdateMember))
	mux.HandleFunc("DELETE /api/units/{id}/members/{uid}", s.withAuth(s.handleDeleteMember))
	mux.HandleFunc("GET /api/units/{id}/environments", s.withAuth(s.handleListUnitEnvironments))

	mux.HandleFunc("GET /api/admin/overview", s.withAdmin(s.handleOverview))
	mux.HandleFunc("GET /api/admin/settings", s.withAdmin(s.handleGetSettings))
	mux.HandleFunc("PUT /api/admin/settings", s.withAdmin(s.handleUpdateSettings))
	mux.HandleFunc("GET /api/admin/units", s.withAdmin(s.handleListUnits))
	mux.HandleFunc("POST /api/admin/units", s.withAdmin(s.handleCreateUnit))
	mux.HandleFunc("PATCH /api/admin/units/{id}", s.withAdmin(s.handleUpdateUnit))
	mux.HandleFunc("DELETE /api/admin/units/{id}", s.withAdmin(s.handleDeleteUnit))
	mux.HandleFunc("GET /api/admin/users", s.withAdmin(s.handleListUsers))
	mux.HandleFunc("POST /api/admin/users", s.withAdmin(s.handleCreateUser))
	mux.HandleFunc("PATCH /api/admin/users/{id}", s.withAdmin(s.handleUpdateUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.withAdmin(s.handleDeleteUser))
	mux.HandleFunc("GET /api/admin/templates", s.withAdmin(s.handleAdminListTemplates))
	mux.HandleFunc("POST /api/admin/templates", s.withAdmin(s.handleCreateTemplate))
	mux.HandleFunc("PATCH /api/admin/templates/{id}", s.withAdmin(s.handleUpdateTemplate))
	mux.HandleFunc("DELETE /api/admin/templates/{id}", s.withAdmin(s.handleDeleteTemplate))
	mux.HandleFunc("GET /api/admin/environments", s.withAdmin(s.handleAdminListEnvironments))
	mux.HandleFunc("GET /api/admin/audit-logs", s.withAdmin(s.handleListAuditLogs))

	mux.Handle("/", s.webFS)
	return logMiddleware(mux)
}

// ---------- middleware ----------

type authedHandler func(http.ResponseWriter, *http.Request, *models.User)

func (s *Server) withAuth(h authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := auth.BearerToken(r.Header.Get("Authorization"))
		if token == "" {
			writeError(w, http.StatusUnauthorized, "未登录")
			return
		}
		claims, err := s.auth.Parse(token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "登录已过期，请重新登录")
			return
		}
		user, err := s.db.GetUserByID(r.Context(), claims.UserID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "用户不存在")
			return
		}
		if user.Disabled {
			writeError(w, http.StatusForbidden, "账号已被禁用")
			return
		}
		h(w, r, user)
	}
}

func (s *Server) withAdmin(h authedHandler) http.HandlerFunc {
	return s.withAuth(func(w http.ResponseWriter, r *http.Request, u *models.User) {
		if !u.IsPlatformAdmin {
			writeError(w, http.StatusForbidden, "需要平台管理员权限")
			return
		}
		h(w, r, u)
	})
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
		}
	})
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("请求格式错误: %w", err)
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("无效的 ID")
	}
	return id, nil
}

func (s *Server) unitAccess(r *http.Request, u *models.User, unitID int64) (*models.Membership, error) {
	if u.IsPlatformAdmin {
		return nil, nil
	}
	m, err := s.db.GetMembership(r.Context(), unitID, u.ID)
	if err != nil {
		return nil, errors.New("无权访问该单位")
	}
	return m, nil
}

func (s *Server) isUnitAdmin(r *http.Request, u *models.User, unitID int64) bool {
	if u.IsPlatformAdmin {
		return true
	}
	m, err := s.db.GetMembership(r.Context(), unitID, u.ID)
	return err == nil && m.Role == "unit_admin"
}

func (s *Server) envAccess(r *http.Request, u *models.User, env *models.Environment) bool {
	if u.IsPlatformAdmin || env.UserID == u.ID {
		return true
	}
	return s.isUnitAdmin(r, u, env.UnitID)
}

// ---------- auth handlers ----------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := s.db.GetUserByUsername(r.Context(), strings.TrimSpace(req.Username))
	if err != nil || !auth.CheckPassword(user.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if user.Disabled {
		writeError(w, http.StatusForbidden, "账号已被禁用")
		return
	}
	token, err := s.auth.Token(user.ID, user.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "签发令牌失败")
		return
	}
	s.db.Audit(r.Context(), user.ID, user.Username, "login", "", "")
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": user})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, u *models.User) {
	memberships, err := s.db.ListUserMemberships(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "memberships": memberships})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, u *models.User) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !auth.CheckPassword(u.PasswordHash, req.OldPassword) {
		writeError(w, http.StatusBadRequest, "原密码不正确")
		return
	}
	if len(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "新密码长度至少 8 位")
		return
	}
	if err := s.db.ChangePassword(r.Context(), u.ID, req.NewPassword); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "change_password", "", "")
	writeJSON(w, http.StatusOK, map[string]string{"message": "密码已更新"})
}

// ---------- static SPA ----------

func spaHandler(dir string) http.Handler {
	fs := http.Dir(dir)
	fileServer := http.FileServer(fs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Clean(r.URL.Path)
		if strings.HasPrefix(p, "/api/") {
			http.NotFound(w, r)
			return
		}
		if p != "/" {
			if f, err := fs.Open(p); err == nil {
				if st, err := f.Stat(); err == nil && !st.IsDir() {
					f.Close()
					fileServer.ServeHTTP(w, r)
					return
				}
				f.Close()
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

func ctxWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
