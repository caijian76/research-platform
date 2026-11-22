package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"

	"research-platform/backend/internal/auth"
)

var sshUpgrader = websocket.Upgrader{
	ReadBufferSize:  32 * 1024,
	WriteBufferSize: 32 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	},
}

type wsWriter struct {
	conn *websocket.Conn
	mu   *sync.Mutex
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// handleWebSSH bridges a browser WebSocket to an SSH shell inside the environment pod.
func (s *Server) handleWebSSH(w http.ResponseWriter, r *http.Request) {
	token := auth.BearerToken(r.Header.Get("Authorization"))
	if token == "" {
		token = r.URL.Query().Get("token")
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
	if !s.envAccess(r, user, env) {
		writeError(w, http.StatusForbidden, "无权访问该环境")
		return
	}
	password, err := s.auth.Decrypt(env.SSHPasswordEnc)
	if err != nil || password == "" {
		writeError(w, http.StatusInternalServerError, "环境凭证不可用")
		return
	}

	conn, err := sshUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("webssh upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	var writeMu sync.Mutex
	sendJSON := func(v any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		if payload, err := json.Marshal(v); err == nil {
			_ = conn.WriteMessage(websocket.TextMessage, payload)
		}
	}
	fail := func(message string) {
		sendJSON(map[string]string{"type": "error", "message": message})
		writeMu.Lock()
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(3*time.Second))
		writeMu.Unlock()
	}

	addr := fmt.Sprintf("%s.%s.svc.cluster.local:%d", env.SSHService, env.Namespace, env.SSHPort)
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
		User: env.SSHUser,
		Auth: []ssh.AuthMethod{ssh.Password(password)},
		// The SSH service is an in-cluster NodePort target with ephemeral pods; no host key is published for it.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	})
	if err != nil {
		fail("SSH 连接失败：" + err.Error())
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		fail("SSH 会话创建失败：" + err.Error())
		return
	}
	defer session.Close()

	cols := queryInt(r, "cols", 120, 20, 500)
	rows := queryInt(r, "rows", 32, 5, 200)
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		fail("申请终端失败：" + err.Error())
		return
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		fail("获取标准输入失败：" + err.Error())
		return
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		fail("获取标准输出失败：" + err.Error())
		return
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		fail("获取标准错误失败：" + err.Error())
		return
	}
	if err := session.Shell(); err != nil {
		fail("启动 Shell 失败：" + err.Error())
		return
	}

	done := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = session.Close()
			_ = client.Close()
			_ = conn.Close()
			close(done)
		})
	}
	defer stop()

	writer := &wsWriter{conn: conn, mu: &writeMu}
	sendJSON(map[string]string{"type": "ready", "message": "WebSSH 已连接"})
	go func() {
		_, _ = io.Copy(writer, stdout)
		stop()
	}()
	go func() {
		_, _ = io.Copy(writer, stderr)
	}()

	go func() {
		defer stop()
		for {
			msgType, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if msgType == websocket.TextMessage {
				var control struct {
					Type string `json:"type"`
					Cols int    `json:"cols"`
					Rows int    `json:"rows"`
				}
				if json.Unmarshal(data, &control) == nil && control.Type == "resize" {
					if control.Cols > 0 && control.Rows > 0 {
						_ = session.WindowChange(control.Rows, control.Cols)
					}
					continue
				}
			}
			if _, err := stdin.Write(data); err != nil {
				return
			}
		}
	}()

	<-done
}

func queryInt(r *http.Request, key string, def, min, max int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
