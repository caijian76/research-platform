#!/usr/bin/env bash
set -euo pipefail

: "${ROOT_PASSWORD:?ROOT_PASSWORD env is required}"
: "${VNC_PASSWORD:?VNC_PASSWORD env is required}"
GEOMETRY="${VNC_GEOMETRY:-1440x900}"

echo "workspace: configuring root account"
echo "root:${ROOT_PASSWORD}" | chpasswd

mkdir -p /var/run/sshd /root/.vnc
ssh-keygen -A

sed -ri \
  -e 's/^#?PermitRootLogin.*/PermitRootLogin yes/' \
  -e 's/^#?PasswordAuthentication.*/PasswordAuthentication yes/' \
  -e 's/^#?UsePAM.*/UsePAM yes/' \
  -e 's/^#?X11Forwarding.*/X11Forwarding yes/' \
  /etc/ssh/sshd_config
grep -q '^PermitRootLogin' /etc/ssh/sshd_config || echo 'PermitRootLogin yes' >> /etc/ssh/sshd_config
grep -q '^PasswordAuthentication' /etc/ssh/sshd_config || echo 'PasswordAuthentication yes' >> /etc/ssh/sshd_config
/usr/sbin/sshd -e
echo "workspace: sshd listening on 22"

printf '%s\n' "${VNC_PASSWORD}" | vncpasswd -f > /root/.vnc/passwd
chmod 600 /root/.vnc/passwd

rm -f /tmp/.X1-lock /tmp/.X11-unix/X1 2>/dev/null || true
vncserver :1 -geometry "${GEOMETRY}" -depth 24 -localhost no \
  -SecurityTypes VncAuth -xstartup /usr/local/bin/vnc-xstartup
echo "workspace: VNC server started at 127.0.0.1:5901 (${GEOMETRY})"

for _ in $(seq 1 60); do
  if (echo > /dev/tcp/127.0.0.1/5901) >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "workspace: noVNC listening on 0.0.0.0:6080"
exec websockify --web=/usr/share/novnc 0.0.0.0:6080 127.0.0.1:5901