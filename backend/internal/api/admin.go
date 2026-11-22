package api

import (
	"fmt"
	"net/http"
	"strings"

	"research-platform/backend/internal/models"
)

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request, u *models.User) {
	users, err := s.db.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request, u *models.User) {
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Role        string `json:"role"`
		UnitID      int64  `json:"unit_id"`
		IsAdmin     bool   `json:"is_platform_admin"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if !validUsername(req.Username) {
		writeError(w, http.StatusBadRequest, "用户名只能包含字母、数字、下划线、点或短横线，长度 3-32")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "密码长度至少 8 位")
		return
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		if req.IsAdmin {
			role = "platform_admin"
		} else {
			role = "member"
		}
	}
	if role != "platform_admin" && role != "unit_admin" && role != "member" {
		writeError(w, http.StatusBadRequest, "无效的身份类型")
		return
	}
	isAdmin := role == "platform_admin"
	var unit *models.Unit
	if !isAdmin {
		if req.UnitID <= 0 {
			writeError(w, http.StatusBadRequest, "二级管理员和科研人员必须选择所属二级单位")
			return
		}
		var err error
		unit, err = s.db.GetUnit(r.Context(), req.UnitID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "所属二级单位不存在")
			return
		}
	}
	user, err := s.db.CreateUserWithMembership(r.Context(), req.Username, req.Password, req.DisplayName, req.Email, isAdmin, role, req.UnitID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "创建用户失败（用户名可能已存在）")
		return
	}
	detail := role
	if unit != nil {
		detail = unit.Name + "/" + role
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "create_user", user.Username, detail)
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request, u *models.User) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := s.db.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	var req struct {
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		IsAdmin     *bool  `json:"is_platform_admin"`
		Disabled    *bool  `json:"disabled"`
		Password    string `json:"password"`
		Role        string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	isAdmin := target.IsPlatformAdmin
	role := strings.TrimSpace(req.Role)
	syncRole := false
	if role != "" {
		if role != "platform_admin" && role != "unit_admin" && role != "member" {
			writeError(w, http.StatusBadRequest, "无效的身份类型")
			return
		}
		isAdmin = role == "platform_admin"
		if role != "platform_admin" {
			memberships, err := s.db.ListUserMemberships(r.Context(), id)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if len(memberships) == 0 {
				if target.IsPlatformAdmin || role == "unit_admin" {
					writeError(w, http.StatusBadRequest, "该用户未加入二级单位；编辑用户不能修改单位信息，请先在单位管理中为其添加单位")
					return
				}
			} else {
				for _, m := range memberships {
					if m.Role != "unit_admin" || role == "unit_admin" {
						continue
					}
					members, err := s.db.ListMembers(r.Context(), m.UnitID)
					if err != nil {
						writeError(w, http.StatusInternalServerError, err.Error())
						return
					}
					admins := 0
					for _, x := range members {
						if x.Role == "unit_admin" {
							admins++
						}
					}
					if admins <= 1 {
						writeError(w, http.StatusBadRequest, fmt.Sprintf("单位「%s」至少需要保留一名二级管理员", m.UnitName))
						return
					}
				}
				syncRole = true
			}
		}
	} else if req.IsAdmin != nil {
		isAdmin = *req.IsAdmin
	}
	disabled := target.Disabled
	if req.Disabled != nil {
		disabled = *req.Disabled
	}
	if target.ID == u.ID && (!isAdmin || disabled) {
		writeError(w, http.StatusBadRequest, "不能取消自己的平台管理员权限或禁用自己")
		return
	}
	if req.Password != "" && len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "密码长度至少 8 位")
		return
	}
	if err := s.db.UpdateUserAndRoles(r.Context(), id, req.DisplayName, req.Email, isAdmin, disabled, req.Password, role, syncRole); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "update_user", target.Username, role)
	updated, _ := s.db.GetUserByID(r.Context(), id)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request, u *models.User) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, err := s.db.GetUserByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if target.ID == u.ID {
		writeError(w, http.StatusBadRequest, "不能删除自己的账号，可选择禁用")
		return
	}
	if target.IsPlatformAdmin {
		admins, err := s.db.CountPlatformAdmins(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if admins <= 1 {
			writeError(w, http.StatusBadRequest, "平台至少需要保留一名平台管理员")
			return
		}
	}
	envs, err := s.db.ListEnvironments(r.Context(), 0, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(envs) > 0 {
		writeError(w, http.StatusConflict, fmt.Sprintf("该用户仍有 %d 个科研环境，请先在「科研环境」中删除后再删除用户", len(envs)))
		return
	}
	if err := s.db.DeleteUser(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "delete_user", target.Username, "")
	writeJSON(w, http.StatusOK, map[string]string{"message": "用户已删除"})
}

func validUsername(name string) bool {
	if len(name) < 3 || len(name) > 32 {
		return false
	}
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request, u *models.User) {
	templates, err := s.db.ListTemplates(r.Context(), false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

func (s *Server) handleAdminListTemplates(w http.ResponseWriter, r *http.Request, u *models.User) {
	templates, err := s.db.ListTemplates(r.Context(), true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

func (s *Server) handleCreateTemplate(w http.ResponseWriter, r *http.Request, u *models.User) {
	var req models.Template
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if msg := validateTemplate(req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	tpl, err := s.db.CreateTemplate(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "创建模板失败（名称可能已存在）: "+err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "create_template", tpl.Name, "")
	writeJSON(w, http.StatusCreated, tpl)
}

func (s *Server) handleUpdateTemplate(w http.ResponseWriter, r *http.Request, u *models.User) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, err := s.db.GetTemplate(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	req := *existing
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ID = id
	if msg := validateTemplate(req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.db.UpdateTemplate(r.Context(), req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "update_template", req.Name, "")
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request, u *models.User) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tpl, err := s.db.GetTemplate(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	if err := s.db.DeleteTemplate(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "delete_template", tpl.Name, "")
	writeJSON(w, http.StatusOK, map[string]string{"message": "模板已删除"})
}

func validateTemplate(t models.Template) string {
	if strings.TrimSpace(t.Name) == "" || strings.TrimSpace(t.Image) == "" {
		return "模板名称和镜像不能为空"
	}
	if t.CPUMilli <= 0 || t.MemMi <= 0 {
		return "默认 CPU / 内存必须大于 0"
	}
	if !t.RootPV && !t.Ephemeral {
		return "请至少选择一种根文件系统模式（持久化或临时）"
	}
	if t.RootPV && t.StorageGi <= 0 {
		return "支持持久化的模板必须配置根文件系统容量"
	}
	if t.SSHPort <= 0 || t.SSHPort > 65535 || t.VNCPort <= 0 || t.VNCPort > 65535 {
		return "端口必须在 1-65535 之间"
	}
	if !strings.Contains(t.Image, ":") && !strings.Contains(t.Image, "@") {
		return "镜像建议使用明确的 tag，例如 ubuntu:22.04"
	}
	return ""
}
