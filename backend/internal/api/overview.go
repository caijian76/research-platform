package api

import (
	"net/http"

	"research-platform/backend/internal/models"
)

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, u *models.User) {
	nodes, clusterCPU, clusterMem, err := s.kube.NodeCapacity(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取集群节点失败: "+err.Error())
		return
	}
	limits, err := s.db.GetPlatformLimits(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if limits.CPUMilli <= 0 && limits.MemMi <= 0 {
		limits.CPUMilli, limits.MemMi = clusterCPU, clusterMem
		if limits.StorageGi <= 0 {
			limits.StorageGi = s.cfg.PlatformStorageGi
		}
		if err := s.db.SetPlatformLimits(r.Context(), limits); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	allocated, err := s.db.SumUnitLimits(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	unitCount, _ := s.db.CountUnits(r.Context())
	userCount, _ := s.db.CountUsers(r.Context())
	envCount, runningCount, _ := s.db.CountEnvironments(r.Context())
	writeJSON(w, http.StatusOK, models.Overview{
		Nodes:           nodes,
		ClusterCPUMilli: clusterCPU,
		ClusterMemMi:    clusterMem,
		PlatformLimits:  limits,
		Allocated:       allocated,
		UnitCount:       unitCount,
		UserCount:       userCount,
		EnvCount:        envCount,
		RunningEnvCount: runningCount,
		StorageClass:    s.cfg.StorageClass,
	})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request, u *models.User) {
	limits, err := s.db.GetPlatformLimits(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, limits)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request, u *models.User) {
	var req models.PlatformLimits
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.CPUMilli <= 0 || req.MemMi <= 0 || req.StorageGi <= 0 {
		writeError(w, http.StatusBadRequest, "平台资源上限必须大于 0")
		return
	}
	allocated, err := s.db.SumUnitLimits(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if allocated.CPUMilli > req.CPUMilli || allocated.MemMi > req.MemMi || allocated.StorageGi > req.StorageGi {
		writeError(w, http.StatusBadRequest, "平台上限不能小于已分配给二级单位的资源总量")
		return
	}
	if err := s.db.SetPlatformLimits(r.Context(), req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "update_platform_limits", "", "")
	writeJSON(w, http.StatusOK, req)
}