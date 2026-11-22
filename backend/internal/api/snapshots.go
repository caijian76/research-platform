package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"research-platform/backend/internal/db"
	"research-platform/backend/internal/models"
)

func (s *Server) persistentEnvForSnapshot(w http.ResponseWriter, r *http.Request, u *models.User) *models.Environment {
	env := s.getEnvForAction(w, r, u)
	if env == nil {
		return nil
	}
	if !env.RootPV || env.PVCName == "" {
		writeError(w, http.StatusBadRequest, "临时环境没有持久化根文件系统，不支持快照")
		return nil
	}
	return env
}

func (s *Server) stoppedEnvForRestore(w http.ResponseWriter, r *http.Request, u *models.User) *models.Environment {
	env := s.persistentEnvForSnapshot(w, r, u)
	if env == nil {
		return nil
	}
	s.syncEnv(r, env)
	if env.DesiredState != "stopped" || env.Status != "stopped" {
		writeError(w, http.StatusBadRequest, "请先停止环境后再恢复快照")
		return nil
	}
	return env
}

func (s *Server) syncSnapshotRow(ctx context.Context, snap *models.Snapshot, namespace string) {
	if snap.Status != "creating" {
		return
	}
	st, err := s.kube.VolumeSnapshotState(ctx, namespace, snap.K8sName)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "不存在") {
			snap.Status = "error"
			snap.Error = "快照对象不存在"
			_ = s.db.UpdateSnapshotStatus(ctx, snap.ID, snap.Status, 0, snap.Error)
		}
		return
	}
	if st.Error != "" {
		snap.Status = "error"
		snap.Error = st.Error
		_ = s.db.UpdateSnapshotStatus(ctx, snap.ID, snap.Status, 0, st.Error)
		return
	}
	if st.Ready {
		snap.Status = "ready"
		snap.SizeBytes = st.SizeBytes
		snap.Error = ""
		_ = s.db.UpdateSnapshotStatus(ctx, snap.ID, snap.Status, st.SizeBytes, "")
	}
}

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request, u *models.User) {
	env := s.getEnvForAction(w, r, u)
	if env == nil {
		return
	}
	if !env.RootPV || env.PVCName == "" {
		writeError(w, http.StatusBadRequest, "临时环境没有持久化根文件系统，不支持快照")
		return
	}
	snaps, err := s.db.ListSnapshots(r.Context(), env.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range snaps {
		s.syncSnapshotRow(r.Context(), &snaps[i], env.Namespace)
	}
	writeJSON(w, http.StatusOK, snaps)
}

func (s *Server) handleCreateSnapshot(w http.ResponseWriter, r *http.Request, u *models.User) {
	env := s.persistentEnvForSnapshot(w, r, u)
	if env == nil {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	desc := strings.TrimSpace(req.Description)
	if name == "" {
		writeError(w, http.StatusBadRequest, "请填写快照名称")
		return
	}
	if len([]rune(name)) > 64 {
		writeError(w, http.StatusBadRequest, "快照名称不能超过 64 个字符")
		return
	}
	if len([]rune(desc)) > 500 {
		writeError(w, http.StatusBadRequest, "快照描述不能超过 500 个字符")
		return
	}
	exists, err := s.db.SnapshotNameExists(r.Context(), env.ID, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if exists {
		writeError(w, http.StatusConflict, "该环境已存在同名快照")
		return
	}
	count, err := s.db.CountSnapshots(r.Context(), env.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if count >= db.SnapshotLimitPerEnv {
		writeError(w, http.StatusConflict, "每个环境最多同时保存 5 个快照，请先删除旧快照")
		return
	}

	k8sName, err := newSnapshotObjectName()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成快照标识失败: "+err.Error())
		return
	}
	syncCtx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	err = s.kube.SyncEnvironmentFilesystem(syncCtx, env)
	cancel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "刷新环境文件系统失败，请确认环境运行正常后重试: "+err.Error())
		return
	}
	if err := s.kube.CreateVolumeSnapshot(r.Context(), env.Namespace, env.PVCName, k8sName, env.PodName); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	snap, err := s.db.CreateSnapshot(r.Context(), env.ID, name, desc, k8sName)
	if err != nil {
		_ = s.kube.DeleteVolumeSnapshot(r.Context(), env.Namespace, k8sName)
		if errors.Is(err, db.ErrSnapshotLimit) {
			writeError(w, http.StatusConflict, "每个环境最多同时保存 5 个快照，请先删除旧快照")
			return
		}
		writeError(w, http.StatusBadRequest, "创建快照失败（名称可能已存在）")
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "create_snapshot", name, env.Namespace+"/"+env.PodName)
	go s.pollSnapshot(snap.ID, env.Namespace, k8sName)
	writeJSON(w, http.StatusCreated, snap)
}

func (s *Server) pollSnapshot(snapID int64, namespace, k8sName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for {
		st, err := s.kube.VolumeSnapshotState(ctx, namespace, k8sName)
		if err == nil {
			if st.Error != "" {
				_ = s.db.UpdateSnapshotStatus(ctx, snapID, "error", 0, st.Error)
				return
			}
			if st.Ready {
				_ = s.db.UpdateSnapshotStatus(ctx, snapID, "ready", st.SizeBytes, "")
				return
			}
		} else if strings.Contains(err.Error(), "不存在") {
			_ = s.db.UpdateSnapshotStatus(ctx, snapID, "error", 0, "快照对象不存在")
			return
		}
		select {
		case <-ctx.Done():
			_ = s.db.UpdateSnapshotStatus(context.Background(), snapID, "error", 0, "快照创建超时，请检查集群存储状态")
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (s *Server) handleDeleteSnapshot(w http.ResponseWriter, r *http.Request, u *models.User) {
	env := s.persistentEnvForSnapshot(w, r, u)
	if env == nil {
		return
	}
	snapID, err := pathID(r, "sid")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	snap, err := s.db.GetSnapshot(r.Context(), env.ID, snapID)
	if err != nil {
		writeError(w, http.StatusNotFound, "快照不存在")
		return
	}
	if err := s.kube.DeleteVolumeSnapshot(r.Context(), env.Namespace, snap.K8sName); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.db.DeleteSnapshotRow(r.Context(), env.ID, snap.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), u.ID, u.Username, "delete_snapshot", snap.Name, env.Namespace+"/"+env.PodName)
	writeJSON(w, http.StatusOK, map[string]string{"message": "快照已删除"})
}

func (s *Server) handleRestoreSnapshot(w http.ResponseWriter, r *http.Request, u *models.User) {
	env := s.stoppedEnvForRestore(w, r, u)
	if env == nil {
		return
	}
	snapID, err := pathID(r, "sid")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	snap, err := s.db.GetSnapshot(r.Context(), env.ID, snapID)
	if err != nil {
		writeError(w, http.StatusNotFound, "快照不存在")
		return
	}
	if snap.Status != "ready" {
		writeError(w, http.StatusBadRequest, "快照尚未就绪或已失败，无法恢复")
		return
	}
	if err := s.kube.RestoreVolumeSnapshot(r.Context(), env, snap.K8sName); err != nil {
		writeError(w, http.StatusInternalServerError, "恢复快照失败: "+err.Error())
		return
	}
	msg := fmt.Sprintf("已恢复到快照「%s」，请启动环境查看数据。", snap.Name)
	_ = s.db.UpdateEnvStatus(r.Context(), env.ID, "stopped", msg)
	s.db.Audit(r.Context(), u.ID, u.Username, "restore_snapshot", snap.Name, env.Namespace+"/"+env.PodName)
	writeJSON(w, http.StatusOK, map[string]string{"message": msg})
}

func newSnapshotObjectName() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "snap-" + hex.EncodeToString(buf), nil
}

func (s *Server) deleteEnvSnapshots(ctx context.Context, env *models.Environment) error {
	snaps, err := s.db.ListSnapshots(ctx, env.ID)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(snaps))
	for i := range snaps {
		if err := s.kube.DeleteVolumeSnapshot(ctx, env.Namespace, snaps[i].K8sName); err != nil {
			return err
		}
		names = append(names, snaps[i].K8sName)
	}
	return s.kube.WaitVolumeSnapshotsDeleted(ctx, env.Namespace, names)
}
