package api

import (
	"fmt"
	"regexp"
	"strings"

	"research-platform/backend/internal/auth"
)

// 主机名同时作为 K8s Pod 名称，必须满足 DNS-1123 label：
// 小写字母 / 数字 / 连字符，以字母或数字开头、结尾。
//
// 平台侧再收紧到 48 个字符：Pod 名还会派生出 <name>-rootfs（PVC）、
// <name>-ssh / <name>-novnc（Service），这些同样受 63 字符 label 上限约束。
const maxHostnameLen = 48

var hostnameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// 平台会为每个环境派生一批资源名：
//
//	<名>-ssh / <名>-novnc        Service（NodePort）
//	<名>-rootfs                  PVC
//	<名>-tcp<端口> / <名>-udp<端口>  自定义端口映射的 Service
//
// 主机名若以这些后缀结尾，就会与「另一个环境派生出来的名字」撞车：
// 比如已有环境 foo 时再建一个叫 foo-ssh 的环境，其 headless Service 名
// 正好等于 foo 的 SSH Service，启动时必然 AlreadyExists。这类冲突在创建
// 阶段无法靠集群查重发现（Service 要等启动才创建），所以直接禁止。
var reservedSuffixRe = regexp.MustCompile(`-(ssh|novnc|tcp[0-9]+|udp[0-9]+)$`)

// validateHostname 校验用户填写的主机名，空字符串表示交给平台自动生成。
func validateHostname(name string) error {
	if name == "" {
		return nil
	}
	if len(name) > maxHostnameLen {
		return fmt.Errorf("主机名称最长 %d 个字符", maxHostnameLen)
	}
	if !hostnameRe.MatchString(name) {
		return fmt.Errorf("主机名称只能使用小写字母、数字和连字符（-），且需以字母或数字开头结尾")
	}
	if reservedSuffixRe.MatchString(name) {
		return fmt.Errorf("主机名称不能以 -ssh、-novnc、-tcp<端口>、-udp<端口> 结尾，会与平台派生的服务名冲突")
	}
	return nil
}

// defaultHostname 按「用户名-5 位 hash」生成默认主机名。
func defaultHostname(username string) string {
	return hostnameBase(username) + "-" + auth.RandomSuffix(5)
}

// hostnameBase 把用户名规整成可用作主机名前缀的形式：转小写、非 [a-z0-9]
// 字符替换为连字符、合并连续连字符、去掉首尾连字符，并截断到给后缀留出余量。
func hostnameBase(username string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(username)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('-')
	}
	s := strings.Trim(b.String(), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	// 预留 "-" + 5 位 hash 的位置
	if limit := maxHostnameLen - 6; len(s) > limit {
		s = strings.TrimRight(s[:limit], "-")
	}
	if s == "" {
		s = "user"
	}
	return s
}
