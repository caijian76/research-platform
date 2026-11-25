package api

import (
	"fmt"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"research-platform/backend/internal/models"
)

const maxCustomPorts = 5

func (s *Server) handleListPorts(w http.ResponseWriter, r *http.Request, u *models.User) {
	env := s.getEnvForAction(w, r, u)
	if env == nil {
		return
	}
	host := s.kube.PublicHost(r.Context())
	custom, err := s.db.CountEnvPorts(r.Context(), env.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models.PortListView{
		Items:  s.buildPortMappings(r.Context(), env, host),
		Max:    maxCustomPorts,
		Host:   host,
		Count:  custom,
		CanAdd: custom < maxCustomPorts,
	})
}

func (s *Server) handleCreatePort(w http.ResponseWriter, r *http.Request, u *models.User) {
	env := s.getEnvForAction(w, r, u)
	if env == nil {
		return
	}
	var req struct {
		ContainerPort int32  `json:"container_port"`
		Protocol      string `json:"protocol"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ContainerPort < 1 || req.ContainerPort > 65535 {
		writeError(w, http.StatusBadRequest, "容器端口必须在 1 到 65535 之间")
		return
	}
	protocol := strings.ToUpper(strings.TrimSpace(req.Protocol))
	if protocol == "" {
		protocol = "TCP"
	}
	if protocol != "TCP" && protocol != "UDP" {
		writeError(w, http.StatusBadRequest, "协议只能是 TCP 或 UDP")
		return
	}
	count, err := s.db.CountEnvPorts(r.Context(), env.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if count >= maxCustomPorts {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("每个环境最多添加 %d 个自定义端口映射", maxCustomPorts))
		return
	}
	// Built-in SSH/VNC container ports cannot be duplicated.
	if (env.Graphical && req.ContainerPort == env.VNCPort) || req.ContainerPort == env.SSHPort {
		writeError(w, http.StatusBadRequest, "该容器端口已被 SSH 或 VNC 端口占用")
		return
	}
	if existing, _ := s.db.GetEnvPortByContainer(r.Context(), env.ID, req.ContainerPort, protocol); existing != nil {
		writeError(w, http.StatusBadRequest, "该容器端口映射已存在")
		return
	}

	proto := corev1.ProtocolTCP
	if protocol == "UDP" {
		proto = corev1.ProtocolUDP
	}
	suffix := strings.ToLower(protocol)
	svcName := fmt.Sprintf("%s-%s%d", env.PodName, suffix, req.ContainerPort)
	nodePort, err := s.kube.EnsurePortService(r.Context(), env, svcName, req.ContainerPort, proto)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建端口映射失败: "+err.Error())
		return
	}
	p, err := s.db.CreateEnvPort(r.Context(), env.ID, req.ContainerPort, nodePort, svcName, protocol)
	if err != nil {
		// Roll back the service if persistence failed.
		_ = s.kube.DeleteService(r.Context(), env.Namespace, svcName)
		writeError(w, http.StatusInternalServerError, "保存端口映射失败: "+err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "create_env_port", env.Name,
		fmt.Sprintf("容器端口 %d -> NodePort %d (%s)", req.ContainerPort, nodePort, protocol))
	writeJSON(w, http.StatusCreated, models.PortMapping{
		ID: p.ID, Kind: "custom", Name: "自定义端口", ContainerPort: p.ContainerPort,
		NodePort: p.NodePort, Protocol: p.Protocol, Builtin: false,
		Address: portAddress(s.kube.PublicHost(r.Context()), p.NodePort),
	})
}

func (s *Server) handleDeletePort(w http.ResponseWriter, r *http.Request, u *models.User) {
	env := s.getEnvForAction(w, r, u)
	if env == nil {
		return
	}
	pid, err := pathID(r, "pid")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := s.db.GetEnvPort(r.Context(), env.ID, pid)
	if err != nil {
		writeError(w, http.StatusNotFound, "端口映射不存在")
		return
	}
	if err := s.kube.DeleteService(r.Context(), env.Namespace, p.ServiceName); err != nil {
		writeError(w, http.StatusInternalServerError, "删除端口映射服务失败: "+err.Error())
		return
	}
	if err := s.db.DeleteEnvPort(r.Context(), env.ID, pid); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "delete_env_port", env.Name,
		fmt.Sprintf("容器端口 %d -> NodePort %d (%s)", p.ContainerPort, p.NodePort, p.Protocol))
	writeJSON(w, http.StatusOK, map[string]string{"message": "端口映射已删除"})
}
