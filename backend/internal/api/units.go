package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"research-platform/backend/internal/db"
	"research-platform/backend/internal/models"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

func (s *Server) handleListUnits(w http.ResponseWriter, r *http.Request, u *models.User) {
	units, err := s.db.ListUnits(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, units)
}

type unitRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	CPUMilli    int64  `json:"cpu_milli"`
	MemMi       int64  `json:"mem_mi"`
	StorageGi   int64  `json:"storage_gi"`
	Disabled    bool   `json:"disabled"`
}

func (s *Server) platformLimitsOrInit(r *http.Request) (models.PlatformLimits, error) {
	limits, err := s.db.GetPlatformLimits(r.Context())
	if err != nil {
		return limits, err
	}
	if limits.CPUMilli <= 0 && limits.MemMi <= 0 {
		_, cpu, mem, err := s.kube.NodeCapacity(r.Context())
		if err != nil {
			return limits, err
		}
		limits.CPUMilli, limits.MemMi, limits.StorageGi = cpu, mem, s.cfg.PlatformStorageGi
		if err := s.db.SetPlatformLimits(r.Context(), limits); err != nil {
			return limits, err
		}
	}
	return limits, nil
}

func (s *Server) handleCreateUnit(w http.ResponseWriter, r *http.Request, u *models.User) {
	var req unitRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.TrimSpace(strings.ToLower(req.Slug))
	if req.Name == "" || !slugRe.MatchString(req.Slug) {
		writeError(w, http.StatusBadRequest, "单位名称不能为空；标识只能包含小写字母、数字和短横线（2-32 位）")
		return
	}
	if req.CPUMilli <= 0 || req.MemMi <= 0 || req.StorageGi <= 0 {
		writeError(w, http.StatusBadRequest, "CPU / 内存 / 存储上限必须大于 0")
		return
	}
	limits, err := s.platformLimitsOrInit(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	allocated, err := s.db.SumUnitLimits(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if allocated.CPUMilli+req.CPUMilli > limits.CPUMilli || allocated.MemMi+req.MemMi > limits.MemMi || allocated.StorageGi+req.StorageGi > limits.StorageGi {
		writeError(w, http.StatusBadRequest, "超出平台剩余可分配资源，请调整上限或先修改平台总资源")
		return
	}
	if _, err := s.db.GetUnitBySlug(r.Context(), req.Slug); err == nil {
		writeError(w, http.StatusBadRequest, "单位标识已存在")
		return
	}
	ns := s.kube.UnitNamespace(req.Slug)
	if err := s.kube.EnsureUnitNamespace(r.Context(), req.Name, req.Slug, ns, req.CPUMilli, req.MemMi, req.StorageGi); err != nil {
		_ = s.kube.DeleteUnitNamespace(r.Context(), ns)
		writeError(w, http.StatusInternalServerError, "创建单位命名空间失败: "+err.Error())
		return
	}
	unit, err := s.db.CreateUnit(r.Context(), req.Name, req.Slug, ns, req.Description, req.CPUMilli, req.MemMi, req.StorageGi)
	if err != nil {
		_ = s.kube.DeleteUnitNamespace(r.Context(), ns)
		writeError(w, http.StatusInternalServerError, "创建单位失败: "+err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "create_unit", unit.Name, ns)
	writeJSON(w, http.StatusCreated, unit)
}

func (s *Server) handleUpdateUnit(w http.ResponseWriter, r *http.Request, u *models.User) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	unit, err := s.db.GetUnit(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "单位不存在")
		return
	}
	var req unitRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "单位名称不能为空")
		return
	}
	if req.CPUMilli <= 0 || req.MemMi <= 0 || req.StorageGi <= 0 {
		writeError(w, http.StatusBadRequest, "CPU / 内存 / 存储上限必须大于 0")
		return
	}
	usage, err := s.db.UsageByUnit(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.CPUMilli < usage.CPUMilli || req.MemMi < usage.MemMi || req.StorageGi < usage.StorageGi {
		writeError(w, http.StatusBadRequest, "新的上限不能低于该单位当前运行中的 CPU/内存用量及全部环境存储总量")
		return
	}
	allocatedStorage, err := s.db.SumMemberStorage(r.Context(), id, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.StorageGi < allocatedStorage {
		writeError(w, http.StatusBadRequest, "存储上限不能低于已分配给成员的存储配额合计（存储配额不可超分）")
		return
	}
	limits, err := s.platformLimitsOrInit(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	allocated, err := s.db.SumUnitLimits(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	diffCPU := req.CPUMilli - unit.CPUMilli
	diffMem := req.MemMi - unit.MemMi
	diffStorage := req.StorageGi - unit.StorageGi
	if allocated.CPUMilli+diffCPU > limits.CPUMilli || allocated.MemMi+diffMem > limits.MemMi || allocated.StorageGi+diffStorage > limits.StorageGi {
		writeError(w, http.StatusBadRequest, "超出平台剩余可分配资源")
		return
	}
	if err := s.kube.EnsureUnitNamespace(r.Context(), req.Name, unit.Slug, unit.Namespace, req.CPUMilli, req.MemMi, req.StorageGi); err != nil {
		writeError(w, http.StatusInternalServerError, "更新单位资源配额失败: "+err.Error())
		return
	}
	if err := s.db.UpdateUnit(r.Context(), id, req.Name, req.Description, req.CPUMilli, req.MemMi, req.StorageGi, req.Disabled); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "update_unit", unit.Name, "")
	updated, _ := s.db.GetUnit(r.Context(), id)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteUnit(w http.ResponseWriter, r *http.Request, u *models.User) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	unit, err := s.db.GetUnit(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "单位不存在")
		return
	}
	usage, err := s.db.UsageByUnit(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if usage.EnvCount > 0 {
		writeError(w, http.StatusConflict, "该单位下仍有科研环境，请先删除全部环境再删除单位")
		return
	}
	if err := s.kube.DeleteUnitNamespace(r.Context(), unit.Namespace); err != nil {
		writeError(w, http.StatusInternalServerError, "删除单位命名空间失败: "+err.Error())
		return
	}
	if err := s.db.DeleteUnit(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "delete_unit", unit.Name, unit.Namespace)
	writeJSON(w, http.StatusOK, map[string]string{"message": "单位已删除"})
}

func (s *Server) handleGetUnit(w http.ResponseWriter, r *http.Request, u *models.User) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	m, err := s.unitAccess(r, u, id)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	unit, err := s.db.GetUnit(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "单位不存在")
		return
	}
	unitUsage, _ := s.db.UsageByUnit(r.Context(), id)
	unitUsage.StorageAllocatedGi, _ = s.db.SumMemberStorage(r.Context(), id, 0)
	resp := map[string]any{
		"unit":          unit,
		"unit_usage":    unitUsage,
		"is_unit_admin": u.IsPlatformAdmin || (m != nil && m.Role == "unit_admin"),
	}
	if !u.IsPlatformAdmin {
		self, _ := s.db.UsageByUser(r.Context(), id, u.ID)
		resp["membership"] = m
		resp["my_usage"] = self
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request, u *models.User) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.isUnitAdmin(r, u, id) {
		writeError(w, http.StatusForbidden, "需要该单位的二级管理员权限")
		return
	}
	members, err := s.db.ListMembers(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, members)
}

func (s *Server) handleCreateMember(w http.ResponseWriter, r *http.Request, u *models.User) {
	unitID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.isUnitAdmin(r, u, unitID) {
		writeError(w, http.StatusForbidden, "需要该单位的二级管理员权限")
		return
	}
	unit, err := s.db.GetUnit(r.Context(), unitID)
	if err != nil {
		writeError(w, http.StatusNotFound, "单位不存在")
		return
	}
	var req struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
		Password    string `json:"password"`
		Role        string `json:"role"`
		CPUMilli    int64  `json:"cpu_milli"`
		MemMi       int64  `json:"mem_mi"`
		StorageGi   int64  `json:"storage_gi"`
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
	if req.Role != "unit_admin" && req.Role != "member" {
		req.Role = "member"
	}
	if req.CPUMilli < 0 || req.MemMi < 0 || req.StorageGi < 0 {
		writeError(w, http.StatusBadRequest, "资源上限不能为负数")
		return
	}
	if req.CPUMilli > unit.CPUMilli || req.MemMi > unit.MemMi || req.StorageGi > unit.StorageGi {
		writeError(w, http.StatusBadRequest, "成员资源上限不能超过单位资源上限")
		return
	}
	user, err := s.db.GetUserByUsername(r.Context(), req.Username)
	var existingMember *models.Membership
	var excludeID int64
	if err == nil {
		excludeID = user.ID
		existingMember, _ = s.db.GetMembership(r.Context(), unitID, user.ID)
	} else if !errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	allocatedStorage, aErr := s.db.SumMemberStorage(r.Context(), unitID, excludeID)
	if aErr != nil {
		writeError(w, http.StatusInternalServerError, aErr.Error())
		return
	}
	if allocatedStorage+req.StorageGi > unit.StorageGi {
		writeError(w, http.StatusBadRequest, "成员存储配额合计将超出单位存储上限（存储配额不可超分）")
		return
	}
	if errors.Is(err, db.ErrNotFound) {
		if len(req.Password) < 8 {
			writeError(w, http.StatusBadRequest, "新用户初始密码长度至少 8 位")
			return
		}
		user, err = s.db.CreateUser(r.Context(), req.Username, req.Password, req.DisplayName, req.Email, false)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "创建用户失败: "+err.Error())
			return
		}
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if user.Disabled {
		writeError(w, http.StatusBadRequest, "该用户已被禁用")
		return
	}
	if existingMember != nil && existingMember.Role == "unit_admin" && req.Role != "unit_admin" {
		members, _ := s.db.ListMembers(r.Context(), unitID)
		admins := 0
		for _, x := range members {
			if x.Role == "unit_admin" {
				admins++
			}
		}
		if admins <= 1 {
			writeError(w, http.StatusBadRequest, "单位至少需要保留一名二级管理员")
			return
		}
	}
	m, err := s.db.UpsertMember(r.Context(), unitID, user.ID, req.Role, req.CPUMilli, req.MemMi, req.StorageGi)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "upsert_member", unit.Name+"/"+user.Username, req.Role)
	m.Username = user.Username
	m.DisplayName = user.DisplayName
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleUpdateMember(w http.ResponseWriter, r *http.Request, u *models.User) {
	unitID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID, err := pathID(r, "uid")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.isUnitAdmin(r, u, unitID) {
		writeError(w, http.StatusForbidden, "需要该单位的二级管理员权限")
		return
	}
	target, err := s.db.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	m, err := s.db.GetMembership(r.Context(), unitID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "该用户不在本单位")
		return
	}
	var req struct {
		Role        string `json:"role"`
		CPUMilli    int64  `json:"cpu_milli"`
		MemMi       int64  `json:"mem_mi"`
		StorageGi   int64  `json:"storage_gi"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Role != "unit_admin" && req.Role != "member" {
		writeError(w, http.StatusBadRequest, "角色必须为 unit_admin 或 member")
		return
	}
	if req.CPUMilli < 0 || req.MemMi < 0 || req.StorageGi < 0 {
		writeError(w, http.StatusBadRequest, "资源上限不能为负数")
		return
	}
	unit, err := s.db.GetUnit(r.Context(), unitID)
	if err != nil {
		writeError(w, http.StatusNotFound, "单位不存在")
		return
	}
	if req.CPUMilli > unit.CPUMilli || req.MemMi > unit.MemMi || req.StorageGi > unit.StorageGi {
		writeError(w, http.StatusBadRequest, "成员资源上限不能超过单位资源上限")
		return
	}
	allocatedStorage, err := s.db.SumMemberStorage(r.Context(), unitID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if allocatedStorage+req.StorageGi > unit.StorageGi {
		writeError(w, http.StatusBadRequest, "成员存储配额合计将超出单位存储上限（存储配额不可超分）")
		return
	}
	usage, err := s.db.UsageByUser(r.Context(), unitID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.CPUMilli < usage.CPUMilli || req.MemMi < usage.MemMi || req.StorageGi < usage.StorageGi {
		writeError(w, http.StatusBadRequest, "新的用户上限不能低于该用户当前运行中的 CPU/内存用量及全部环境存储总量")
		return
	}
	if m.Role == "unit_admin" && req.Role != "unit_admin" {
		members, _ := s.db.ListMembers(r.Context(), unitID)
		admins := 0
		for _, x := range members {
			if x.Role == "unit_admin" {
				admins++
			}
		}
		if admins <= 1 {
			writeError(w, http.StatusBadRequest, "单位至少需要保留一名二级管理员")
			return
		}
	}
	if req.Password != "" {
		if !u.IsPlatformAdmin && target.IsPlatformAdmin {
			writeError(w, http.StatusForbidden, "二级管理员不能重置平台管理员密码")
			return
		}
		if len(req.Password) < 8 {
			writeError(w, http.StatusBadRequest, "密码长度至少 8 位")
			return
		}
		if err := s.db.ChangePassword(r.Context(), userID, req.Password); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if req.DisplayName != "" && !target.IsPlatformAdmin {
		if err := s.db.UpdateUser(r.Context(), userID, req.DisplayName, target.Email, target.IsPlatformAdmin, target.Disabled, ""); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	updated, err := s.db.UpsertMember(r.Context(), unitID, userID, req.Role, req.CPUMilli, req.MemMi, req.StorageGi)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "update_member", target.Username, req.Role)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteMember(w http.ResponseWriter, r *http.Request, u *models.User) {
	unitID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID, err := pathID(r, "uid")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.isUnitAdmin(r, u, unitID) {
		writeError(w, http.StatusForbidden, "需要该单位的二级管理员权限")
		return
	}
	if userID == u.ID {
		writeError(w, http.StatusBadRequest, "不能移除自己")
		return
	}
	target, err := s.db.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if target.IsPlatformAdmin && !u.IsPlatformAdmin {
		writeError(w, http.StatusForbidden, "二级管理员不能移除平台管理员")
		return
	}
	m, err := s.db.GetMembership(r.Context(), unitID, userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "该用户不在本单位")
		return
	}
	usage, _ := s.db.UsageByUser(r.Context(), unitID, userID)
	if usage.EnvCount > 0 {
		writeError(w, http.StatusConflict, "该用户在本单位仍有科研环境，请先删除其环境")
		return
	}
	if m.Role == "unit_admin" {
		members, _ := s.db.ListMembers(r.Context(), unitID)
		admins := 0
		for _, x := range members {
			if x.Role == "unit_admin" {
				admins++
			}
		}
		if admins <= 1 {
			writeError(w, http.StatusBadRequest, "单位至少需要保留一名二级管理员")
			return
		}
	}
	if err := s.db.DeleteMember(r.Context(), unitID, userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "remove_member", target.Username, "")
	writeJSON(w, http.StatusOK, map[string]string{"message": "成员已移除"})
}

func (s *Server) handleListUnitEnvironments(w http.ResponseWriter, r *http.Request, u *models.User) {
	unitID, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.isUnitAdmin(r, u, unitID) {
		writeError(w, http.StatusForbidden, "需要该单位的二级管理员权限")
		return
	}
	list, err := s.db.ListEnvironmentsWithNames(r.Context(), unitID, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range list {
		s.syncEnv(r, &list[i])
	}
	writeJSON(w, http.StatusOK, list)
}
