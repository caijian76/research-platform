package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"research-platform/backend/internal/auth"
	"research-platform/backend/internal/models"
)

func (s *Server) syncEnv(r *http.Request, e *models.Environment) {
	status, msg := s.kube.SyncEnvironment(r.Context(), e)
	if status != e.Status || msg != e.StatusMessage {
		_ = s.db.UpdateEnvStatus(r.Context(), e.ID, status, msg)
		e.Status, e.StatusMessage = status, msg
	}
	if e.NodePortSSH != 0 || e.NodePortVNC != 0 {
		_ = s.db.UpdateEnvNodePorts(r.Context(), e.ID, e.NodePortSSH, e.NodePortVNC)
	}
}

func (s *Server) envView(r *http.Request, e *models.Environment, includeSecrets bool) models.EnvironmentView {
	host := s.kube.PublicHost(r.Context())
	view := models.EnvironmentView{Environment: *e}
	ssh := &models.SSHConnection{
		Host: host,
		Port: e.NodePortSSH,
		User: e.SSHUser,
	}
	if e.NodePortSSH > 0 {
		ssh.Command = fmt.Sprintf("ssh -p %d %s@%s", e.NodePortSSH, e.SSHUser, host)
	}
	var vnc *models.VNCConnection
	if e.Graphical {
		vnc = &models.VNCConnection{
			Host: host,
			Port: e.NodePortVNC,
		}
		if e.NodePortVNC > 0 {
			vnc.URL = fmt.Sprintf("http://%s:%d/vnc.html?resize=remote&autoconnect=true", host, e.NodePortVNC)
		}
	}
	if includeSecrets {
		if pw, err := s.auth.Decrypt(e.SSHPasswordEnc); err == nil {
			ssh.Password = pw
		}
		if e.Graphical {
			if pw, err := s.auth.Decrypt(e.VNCPasswordEnc); err == nil {
				pw = effectiveVNCPassword(pw)
				vnc.Password = pw
				if e.NodePortVNC > 0 {
					vnc.URL = fmt.Sprintf("http://%s:%d/vnc.html?password=%s&resize=remote&autoconnect=true", host, e.NodePortVNC, url.QueryEscape(pw))
				}
			}
		}
	}
	view.SSHConnection = ssh
	view.VNCConnection = vnc
	return view
}

func (s *Server) handleListEnvironments(w http.ResponseWriter, r *http.Request, u *models.User) {
	all, err := s.db.ListEnvironmentsWithNames(r.Context(), 0, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := []models.Environment{}
	for i := range all {
		e := &all[i]
		if u.IsPlatformAdmin || e.UserID == u.ID {
			s.syncEnv(r, e)
			out = append(out, *e)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminListEnvironments(w http.ResponseWriter, r *http.Request, u *models.User) {
	all, err := s.db.ListEnvironmentsWithNames(r.Context(), 0, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range all {
		s.syncEnv(r, &all[i])
	}
	writeJSON(w, http.StatusOK, all)
}

func (s *Server) handleCreateEnvironment(w http.ResponseWriter, r *http.Request, u *models.User) {
	if u.IsPlatformAdmin {
		writeError(w, http.StatusForbidden, "平台管理员仅可查看与运维环境，新建环境请由二级单位成员操作")
		return
	}
	var req struct {
		UnitID       int64  `json:"unit_id"`
		TemplateID   int64  `json:"template_id"`
		Name         string `json:"name"`
		CPUMilli     int64  `json:"cpu_milli"`
		MemMi        int64  `json:"mem_mi"`
		StorageGi    int64  `json:"storage_gi"`
		RootPassword string `json:"root_password"`
		VNCPassword  string `json:"vnc_password"`
		Graphical    *bool  `json:"graphical"`
		ShmEnabled   bool   `json:"shm_enabled"`
		ShmMi        int64  `json:"shm_mi"`
		RootPV       *bool  `json:"rootpv"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.UnitID <= 0 || req.TemplateID <= 0 {
		writeError(w, http.StatusBadRequest, "请选择单位和容器模板")
		return
	}
	unit, err := s.db.GetUnit(r.Context(), req.UnitID)
	if err != nil {
		writeError(w, http.StatusNotFound, "单位不存在")
		return
	}
	if unit.Disabled {
		writeError(w, http.StatusBadRequest, "该单位已被禁用")
		return
	}
	tpl, err := s.db.GetTemplate(r.Context(), req.TemplateID)
	if err != nil || !tpl.Enabled {
		writeError(w, http.StatusBadRequest, "模板不存在或已停用")
		return
	}
	mode := tpl.RootPV
	if req.RootPV != nil {
		mode = *req.RootPV
	}
	if mode && !tpl.RootPV {
		writeError(w, http.StatusBadRequest, "该模板不支持持久化模式")
		return
	}
	if !mode && !tpl.Ephemeral {
		writeError(w, http.StatusBadRequest, "该模板不支持临时模式")
		return
	}
	var membership *models.Membership
	membership, err = s.db.GetMembership(r.Context(), unit.ID, u.ID)
	if err != nil {
		writeError(w, http.StatusForbidden, "您不属于该单位")
		return
	}

	cpu := req.CPUMilli
	if cpu <= 0 {
		cpu = tpl.CPUMilli
	}
	mem := req.MemMi
	if mem <= 0 {
		mem = tpl.MemMi
	}
	storage := req.StorageGi
	if storage <= 0 && mode {
		storage = tpl.StorageGi
	}
	if !mode {
		storage = 0
	}
	if cpu <= 0 || mem <= 0 || (mode && storage <= 0) {
		writeError(w, http.StatusBadRequest, "资源规格必须大于 0")
		return
	}
	if cpu > unit.CPUMilli || mem > unit.MemMi {
		writeError(w, http.StatusBadRequest, "单环境规格不能超过单位的资源上限")
		return
	}

	userLimitCPU, userLimitMem, userLimitStorage := unit.CPUMilli, unit.MemMi, unit.StorageGi
	if membership != nil {
		if membership.CPUMilli > 0 || membership.MemMi > 0 || membership.StorageGi > 0 {
			userLimitCPU, userLimitMem, userLimitStorage = membership.CPUMilli, membership.MemMi, membership.StorageGi
		} else {
			writeError(w, http.StatusForbidden, "二级管理员尚未为您分配资源上限")
			return
		}
	}
	userUsage, err := s.db.UsageByUser(r.Context(), unit.ID, u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cpu > userLimitCPU || mem > userLimitMem {
		writeError(w, http.StatusBadRequest, "单个环境的 CPU / 内存不能超过您的个人资源上限")
		return
	}
	if mode && userUsage.StorageGi+storage > userLimitStorage {
		writeError(w, http.StatusBadRequest, "全部环境的存储总量将超出您的个人存储上限")
		return
	}
	unitUsage, err := s.db.UsageByUnit(r.Context(), unit.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if mode && unitUsage.StorageGi+storage > unit.StorageGi {
		writeError(w, http.StatusBadRequest, "单位全部环境的存储总量将超出单位存储上限，请联系平台管理员")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = unit.Name + " 科研环境"
	}
	if len([]rune(name)) > 64 {
		writeError(w, http.StatusBadRequest, "环境名称过长")
		return
	}
	suffix := auth.RandomSuffix(8)
	podName := "ws-" + suffix
	graphical := true
	if req.Graphical != nil {
		graphical = *req.Graphical
	}
	if err := validateShm(req.ShmEnabled, req.ShmMi, mem); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	shmMi := int64(0)
	if req.ShmEnabled {
		shmMi = req.ShmMi
	}
	sshPass := strings.TrimSpace(req.RootPassword)
	if sshPass == "" {
		sshPass = auth.RandomPassword(14)
	} else if len(sshPass) < 6 || len(sshPass) > 64 {
		writeError(w, http.StatusBadRequest, "root 密码长度需为 6-64 位")
		return
	}
	if !printablePassword(sshPass) {
		writeError(w, http.StatusBadRequest, "密码只能包含可见 ASCII 字符")
		return
	}
	vncPass := ""
	if graphical {
		vncPass = strings.TrimSpace(req.VNCPassword)
		if vncPass == "" {
			vncPass = auth.RandomPassword(8)
		} else if len(vncPass) < 6 || len(vncPass) > 8 {
			writeError(w, http.StatusBadRequest, "VNC 密码长度需为 6-8 位")
			return
		}
		if !printablePassword(vncPass) {
			writeError(w, http.StatusBadRequest, "密码只能包含可见 ASCII 字符")
			return
		}
	}
	sshEnc, err := s.auth.Encrypt(sshPass)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成凭证失败")
		return
	}
	var vncEnc string
	if graphical {
		vncEnc, _ = s.auth.Encrypt(vncPass)
	}
	vncService := ""
	vncPort := int32(0)
	if graphical {
		vncService = podName + "-novnc"
		vncPort = tpl.VNCPort
	}

	env := &models.Environment{
		Name:           name,
		UnitID:         unit.ID,
		UserID:         u.ID,
		TemplateID:     tpl.ID,
		Namespace:      unit.Namespace,
		PodName:        podName,
		PVCName:        podName + "-rootfs",
		SSHService:     podName + "-ssh",
		VNCService:     vncService,
		Image:          tpl.Image,
		CPUMilli:       cpu,
		MemMi:          mem,
		StorageGi:      storage,
		RootPV:         mode,
		Graphical:      graphical,
		ShmEnabled:     req.ShmEnabled,
		ShmMi:          shmMi,
		SSHPort:        tpl.SSHPort,
		VNCPort:        vncPort,
		SSHUser:        "root",
		SSHPasswordEnc: sshEnc,
		VNCPasswordEnc: vncEnc,
		DesiredState:   "stopped",
		Status:         "stopped",
		StatusMessage:  "环境已创建，尚未启动",
		Username:       u.Username,
		UnitSlug:       unit.Slug,
		UnitName:       unit.Name,
	}
	env, err = s.db.CreateEnvironment(r.Context(), env)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "保存环境记录失败: "+err.Error())
		return
	}
	sshPort, vncPort, err := s.kube.CreateEnvironment(r.Context(), env, sshPass, vncPass)
	if err != nil {
		_ = s.kube.DeleteEnvironmentResources(r.Context(), env)
		_ = s.db.DeleteEnvironmentRow(r.Context(), env.ID)
		writeError(w, http.StatusInternalServerError, "创建科研环境失败: "+err.Error())
		return
	}
	if sshPort != 0 || vncPort != 0 {
		env.NodePortSSH, env.NodePortVNC = sshPort, vncPort
		_ = s.db.UpdateEnvNodePorts(r.Context(), env.ID, sshPort, vncPort)
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "create_environment", env.Name, env.Namespace+"/"+env.PodName)
	writeJSON(w, http.StatusCreated, s.envView(r, env, true))
}

func (s *Server) handleGetEnvironment(w http.ResponseWriter, r *http.Request, u *models.User) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	env, err := s.db.GetEnvironment(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "环境不存在")
		return
	}
	if !s.envAccess(r, u, env) {
		writeError(w, http.StatusForbidden, "无权访问该环境")
		return
	}
	if unit, err := s.db.GetUnit(r.Context(), env.UnitID); err == nil {
		env.UnitName, env.UnitSlug = unit.Name, unit.Slug
	}
	env.Username = u.Username
	if env.UserID != u.ID {
		if owner, err := s.db.GetUserByID(r.Context(), env.UserID); err == nil {
			env.Username = owner.Username
		}
	}
	s.syncEnv(r, env)
	writeJSON(w, http.StatusOK, s.envView(r, env, true))
}

func (s *Server) handleUpdateEnvironment(w http.ResponseWriter, r *http.Request, u *models.User) {
	env := s.getEnvForAction(w, r, u)
	if env == nil {
		return
	}
	if env.DesiredState != "stopped" {
		writeError(w, http.StatusBadRequest, "只能在环境停止后编辑")
		return
	}
	var req struct {
		Name         string `json:"name"`
		CPUMilli     int64  `json:"cpu_milli"`
		MemMi        int64  `json:"mem_mi"`
		StorageGi    int64  `json:"storage_gi"`
		RootPassword string `json:"root_password"`
		VNCPassword  string `json:"vnc_password"`
		Graphical    *bool  `json:"graphical"`
		ShmEnabled   *bool  `json:"shm_enabled"`
		ShmMi        int64  `json:"shm_mi"`
		RootPV       *bool  `json:"rootpv"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.RootPV != nil && *req.RootPV != env.RootPV {
		writeError(w, http.StatusBadRequest, "环境根文件系统模式创建后不可修改")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = env.Name
	}
	if len([]rune(name)) > 64 {
		writeError(w, http.StatusBadRequest, "环境名称过长")
		return
	}
	if req.CPUMilli <= 0 || req.MemMi <= 0 {
		writeError(w, http.StatusBadRequest, "CPU / 内存必须大于 0")
		return
	}
	storage := req.StorageGi
	if env.RootPV {
		if storage <= 0 {
			storage = env.StorageGi
		}
		if storage < env.StorageGi {
			writeError(w, http.StatusBadRequest, "存储只能增加，不能减少")
			return
		}
	} else {
		storage = 0
	}

	unit, err := s.db.GetUnit(r.Context(), env.UnitID)
	if err != nil {
		writeError(w, http.StatusNotFound, "单位不存在")
		return
	}
	owner, err := s.db.GetUserByID(r.Context(), env.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, "环境所属用户不存在")
		return
	}
	limitCPU, limitMem, limitStorage := unit.CPUMilli, unit.MemMi, unit.StorageGi
	if m, mErr := s.db.GetMembership(r.Context(), env.UnitID, env.UserID); mErr == nil {
		if m.CPUMilli > 0 || m.MemMi > 0 || m.StorageGi > 0 {
			limitCPU, limitMem, limitStorage = m.CPUMilli, m.MemMi, m.StorageGi
		} else if !owner.IsPlatformAdmin {
			writeError(w, http.StatusBadRequest, "二级管理员尚未为该用户分配资源上限")
			return
		}
	} else if !owner.IsPlatformAdmin {
		writeError(w, http.StatusBadRequest, "该用户不属于该单位")
		return
	}
	if req.CPUMilli > limitCPU || req.MemMi > limitMem {
		writeError(w, http.StatusBadRequest, "单个环境的 CPU / 内存不能超过所属用户的个人资源上限")
		return
	}
	userUsage, err := s.db.UsageByUser(r.Context(), env.UnitID, env.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	unitUsage, err := s.db.UsageByUnit(r.Context(), env.UnitID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	otherUserStorage := userUsage.StorageGi - env.StorageGi
	if otherUserStorage < 0 {
		otherUserStorage = 0
	}
	otherUnitStorage := unitUsage.StorageGi - env.StorageGi
	if otherUnitStorage < 0 {
		otherUnitStorage = 0
	}
	if env.RootPV && otherUserStorage+storage > limitStorage {
		writeError(w, http.StatusBadRequest, "全部环境的存储总量将超出所属用户的个人存储上限")
		return
	}
	if env.RootPV && otherUnitStorage+storage > unit.StorageGi {
		writeError(w, http.StatusBadRequest, "单位全部环境的存储总量将超出单位存储上限")
		return
	}

	graphical := env.Graphical
	if req.Graphical != nil {
		graphical = *req.Graphical
	}
	if req.ShmMi < 0 {
		writeError(w, http.StatusBadRequest, "大 SHM 大小必须大于 0 MiB")
		return
	}
	shmEnabled := env.ShmEnabled
	if req.ShmEnabled != nil {
		shmEnabled = *req.ShmEnabled
	}
	shmMi := env.ShmMi
	if req.ShmMi > 0 {
		shmMi = req.ShmMi
	}
	if !shmEnabled {
		shmMi = 0
	}
	if err := validateShm(shmEnabled, shmMi, req.MemMi); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sshEnc := env.SSHPasswordEnc
	if pw := strings.TrimSpace(req.RootPassword); pw != "" {
		if len(pw) < 6 || len(pw) > 64 {
			writeError(w, http.StatusBadRequest, "root 密码长度需为 6-64 位")
			return
		}
		if !printablePassword(pw) {
			writeError(w, http.StatusBadRequest, "密码只能包含可见 ASCII 字符")
			return
		}
		sshEnc, err = s.auth.Encrypt(pw)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "生成凭证失败")
			return
		}
	}
	vncEnc := env.VNCPasswordEnc
	if graphical {
		if pw := strings.TrimSpace(req.VNCPassword); pw != "" {
			if len(pw) < 6 || len(pw) > 8 {
				writeError(w, http.StatusBadRequest, "VNC 密码长度需为 6-8 位")
				return
			}
			if !printablePassword(pw) {
				writeError(w, http.StatusBadRequest, "密码只能包含可见 ASCII 字符")
				return
			}
			vncEnc, err = s.auth.Encrypt(pw)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "生成凭证失败")
				return
			}
		} else if vncEnc == "" {
			vncEnc, _ = s.auth.Encrypt(auth.RandomPassword(8))
		}
	}
	vncService, vncPort := env.VNCService, env.NodePortVNC
	if graphical {
		if vncService == "" {
			vncService = env.PodName + "-novnc"
		}
	} else {
		if err := s.kube.DeleteService(r.Context(), env.Namespace, env.PodName+"-novnc"); err != nil {
			writeError(w, http.StatusInternalServerError, "关闭图形界面失败: "+err.Error())
			return
		}
		vncService, vncPort = "", 0
	}

	if env.RootPV && storage > env.StorageGi {
		if err := s.kube.ResizePVC(r.Context(), env.Namespace, env.PVCName, storage); err != nil {
			writeError(w, http.StatusInternalServerError, "存储扩容失败: "+err.Error())
			return
		}
	}
	message := "环境配置已更新，启动后生效"
	if err := s.db.UpdateEnvironmentConfig(r.Context(), env.ID, name, req.CPUMilli, req.MemMi, storage, graphical, shmEnabled, shmMi, vncService, vncPort, sshEnc, vncEnc, message); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	env.Name = name
	env.CPUMilli = req.CPUMilli
	env.MemMi = req.MemMi
	env.StorageGi = storage
	env.Graphical = graphical
	env.ShmEnabled = shmEnabled
	env.ShmMi = shmMi
	env.VNCService = vncService
	env.NodePortVNC = vncPort
	env.SSHPasswordEnc = sshEnc
	env.VNCPasswordEnc = vncEnc
	env.StatusMessage = message
	s.db.Audit(r.Context(), u.ID, u.Username, "update_environment", env.Name, env.Namespace+"/"+env.PodName)
	writeJSON(w, http.StatusOK, s.envView(r, env, true))
}

func (s *Server) getEnvForAction(w http.ResponseWriter, r *http.Request, u *models.User) *models.Environment {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil
	}
	env, err := s.db.GetEnvironment(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "环境不存在")
		return nil
	}
	if !s.envAccess(r, u, env) {
		writeError(w, http.StatusForbidden, "无权操作该环境")
		return nil
	}
	if owner, err := s.db.GetUserByID(r.Context(), env.UserID); err == nil {
		env.Username = owner.Username
	}
	return env
}

func (s *Server) handleStopEnvironment(w http.ResponseWriter, r *http.Request, u *models.User) {
	env := s.getEnvForAction(w, r, u)
	if env == nil {
		return
	}
	if err := s.kube.StopEnvironment(r.Context(), env); err != nil {
		writeError(w, http.StatusInternalServerError, "停止环境失败: "+err.Error())
		return
	}
	stopMsg := "环境已停止，根文件系统数据保留。"
	if !env.RootPV {
		stopMsg = "环境已停止，临时环境数据不保留。"
	}
	_ = s.db.UpdateEnvDesired(r.Context(), env.ID, "stopped")
	_ = s.db.UpdateEnvStatus(r.Context(), env.ID, "stopped", stopMsg)
	env.DesiredState = "stopped"
	env.Status = "stopped"
	env.StatusMessage = stopMsg
	s.db.Audit(r.Context(), u.ID, u.Username, "stop_environment", env.Name, env.Namespace+"/"+env.PodName)
	writeJSON(w, http.StatusOK, s.envView(r, env, true))
}

func (s *Server) handleStartEnvironment(w http.ResponseWriter, r *http.Request, u *models.User) {
	env := s.getEnvForAction(w, r, u)
	if env == nil {
		return
	}
	if err := s.checkRunningCapacity(r.Context(), env); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sshPass, _ := s.auth.Decrypt(env.SSHPasswordEnc)
	vncPass := ""
	if env.Graphical {
		vncPass, _ = s.auth.Decrypt(env.VNCPasswordEnc)
		vncPass = effectiveVNCPassword(vncPass)
	}
	if err := s.kube.EnsureServicesAndPod(r.Context(), env, sshPass, vncPass); err != nil {
		writeError(w, http.StatusInternalServerError, "启动环境失败: "+err.Error())
		return
	}
	_ = s.db.UpdateEnvDesired(r.Context(), env.ID, "running")
	_ = s.db.UpdateEnvNodePorts(r.Context(), env.ID, env.NodePortSSH, env.NodePortVNC)
	_ = s.db.UpdateEnvStatus(r.Context(), env.ID, "starting", "环境启动中")
	s.db.Audit(r.Context(), u.ID, u.Username, "start_environment", env.Name, env.Namespace+"/"+env.PodName)
	status, msg := s.kube.PodState(r.Context(), env)
	_ = s.db.UpdateEnvStatus(r.Context(), env.ID, status, msg)
	env.Status, env.StatusMessage = status, msg
	writeJSON(w, http.StatusOK, s.envView(r, env, true))
}

func (s *Server) handleRestartEnvironment(w http.ResponseWriter, r *http.Request, u *models.User) {
	env := s.getEnvForAction(w, r, u)
	if env == nil {
		return
	}
	if err := s.checkRunningCapacity(r.Context(), env); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sshPass, _ := s.auth.Decrypt(env.SSHPasswordEnc)
	vncPass := ""
	if env.Graphical {
		vncPass, _ = s.auth.Decrypt(env.VNCPasswordEnc)
		vncPass = effectiveVNCPassword(vncPass)
	}
	ctx, cancel := ctxWithTimeout(r, 40*time.Second)
	defer cancel()
	if err := s.kube.RestartEnvironment(ctx, env, sshPass, vncPass); err != nil {
		_ = s.db.UpdateEnvStatus(r.Context(), env.ID, "error", "重启失败: "+err.Error())
		writeError(w, http.StatusInternalServerError, "重启环境失败: "+err.Error())
		return
	}
	_ = s.db.UpdateEnvDesired(r.Context(), env.ID, "running")
	_ = s.db.UpdateEnvNodePorts(r.Context(), env.ID, env.NodePortSSH, env.NodePortVNC)
	status, msg := s.kube.PodState(r.Context(), env)
	_ = s.db.UpdateEnvStatus(r.Context(), env.ID, status, msg)
	env.Status, env.StatusMessage = status, msg
	s.db.Audit(r.Context(), u.ID, u.Username, "restart_environment", env.Name, env.Namespace+"/"+env.PodName)
	writeJSON(w, http.StatusOK, s.envView(r, env, true))
}

// checkRunningCapacity ensures starting a stopped environment will not push the
// owner's or the unit's running pod requests over their limits.
func (s *Server) checkRunningCapacity(ctx context.Context, env *models.Environment) error {
	if env.DesiredState == "running" {
		return nil
	}
	unit, err := s.db.GetUnit(ctx, env.UnitID)
	if err != nil {
		return fmt.Errorf("单位不存在")
	}
	owner, err := s.db.GetUserByID(ctx, env.UserID)
	if err != nil {
		return fmt.Errorf("环境所属用户不存在")
	}
	membership, mErr := s.db.GetMembership(ctx, env.UnitID, env.UserID)
	limitCPU, limitMem := unit.CPUMilli, unit.MemMi
	if mErr == nil && (membership.CPUMilli > 0 || membership.MemMi > 0 || membership.StorageGi > 0) {
		limitCPU, limitMem = membership.CPUMilli, membership.MemMi
	} else if !owner.IsPlatformAdmin {
		return fmt.Errorf("二级管理员尚未为该用户分配资源上限")
	}
	if env.CPUMilli > limitCPU || env.MemMi > limitMem {
		return fmt.Errorf("该环境的 CPU / 内存已超过所属用户的个人资源上限，请联系二级管理员调整")
	}
	userUsage, err := s.db.UsageByUser(ctx, env.UnitID, env.UserID)
	if err != nil {
		return err
	}
	if userUsage.CPUMilli+env.CPUMilli > limitCPU || userUsage.MemMi+env.MemMi > limitMem {
		return fmt.Errorf("运行中的 CPU / 内存将超出该用户的个人资源上限，请先停止该用户的其他环境")
	}
	unitUsage, err := s.db.UsageByUnit(ctx, env.UnitID)
	if err != nil {
		return err
	}
	if unitUsage.CPUMilli+env.CPUMilli > unit.CPUMilli || unitUsage.MemMi+env.MemMi > unit.MemMi {
		return fmt.Errorf("运行中的 CPU / 内存将超出单位的资源上限，请先停止其他环境")
	}
	return nil
}

func (s *Server) handleDeleteEnvironment(w http.ResponseWriter, r *http.Request, u *models.User) {
	env := s.getEnvForAction(w, r, u)
	if env == nil {
		return
	}
	if err := s.deleteEnvSnapshots(r.Context(), env); err != nil {
		writeError(w, http.StatusInternalServerError, "删除环境快照失败: "+err.Error())
		return
	}
	if err := s.kube.DeleteEnvironmentResources(r.Context(), env); err != nil {
		writeError(w, http.StatusInternalServerError, "删除环境资源失败: "+err.Error())
		return
	}
	if err := s.db.DeleteEnvironmentRow(r.Context(), env.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "delete_environment", env.Name, env.Namespace+"/"+env.PodName)
	writeJSON(w, http.StatusOK, map[string]string{"message": "环境已删除，持久化数据已清理"})
}

const defaultVNCPassword = "changeme"

// VNC passwords are limited to 8 bytes by the VNC protocol.
func effectiveVNCPassword(p string) string {
	if p == "" {
		return defaultVNCPassword
	}
	if len(p) > 8 {
		return p[:8]
	}
	return p
}

func printablePassword(p string) bool {
	for _, c := range p {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func validateShm(enabled bool, mi, memMi int64) error {
	if !enabled {
		return nil
	}
	if mi <= 0 {
		return fmt.Errorf("大 SHM 大小必须大于 0 MiB")
	}
	if mi > 1048576 {
		return fmt.Errorf("大 SHM 大小不能超过 1048576 MiB")
	}
	if mi*2 > memMi {
		return fmt.Errorf("大 SHM 大小不能超过内存的二分之一")
	}
	return nil
}
