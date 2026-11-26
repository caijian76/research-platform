package kube

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	"research-platform/backend/internal/models"
)

const (
	EnvLabel       = "research-env"
	PlatformLabel  = "research-platform"
	UnitLabel      = "research-unit"
	UserLabel      = "research-user"
	QuotaName      = "research-unit-quota"
	LimitRangeName = "research-unit-limits"
	HeadlessLabel  = "research-headless"
	rootPVNodeKey  = "openfuyao.cn/persistent-rootfs"
)

type Client struct {
	cs           kubernetes.Interface
	dyn          dynamic.Interface
	cfg          *rest.Config
	storageClass string
	publicHost   string
	unitNSPrefix string
}

var (
	snapshotGVR      = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshots"}
	snapshotClassGVR = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshotclasses"}
)

func New(storageClass, publicHost, unitNSPrefix string) (*Client, error) {
	var cfg *rest.Config
	var err error
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		cfg, err = rest.InClusterConfig()
	} else {
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			home, _ := os.UserHomeDir()
			kubeconfig = filepath.Join(home, ".kube", "config")
		}
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if err != nil {
		return nil, err
	}
	cfg.QPS = 50
	cfg.Burst = 100
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{cs: cs, dyn: dyn, cfg: cfg, storageClass: storageClass, publicHost: publicHost, unitNSPrefix: unitNSPrefix}, nil
}

func (c *Client) PublicHost(ctx context.Context) string {
	if c.publicHost != "" {
		return c.publicHost
	}
	if ip, err := c.NodeAddress(ctx); err == nil && ip != "" {
		c.publicHost = ip
		return ip
	}
	return "127.0.0.1"
}

func (c *Client) UnitNamespace(slug string) string {
	return c.unitNSPrefix + slug
}

func (c *Client) NodeAddress(ctx context.Context) (string, error) {
	nodes, err := c.cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	for _, n := range nodes.Items {
		for _, addr := range n.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				return addr.Address, nil
			}
		}
	}
	if len(nodes.Items) > 0 {
		for _, addr := range nodes.Items[0].Status.Addresses {
			if addr.Type == corev1.NodeExternalIP {
				return addr.Address, nil
			}
		}
	}
	return "", fmt.Errorf("no node address found")
}

func (c *Client) NodeCapacity(ctx context.Context) ([]models.NodeInfo, int64, int64, error) {
	nodes, err := c.cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, 0, 0, err
	}
	out := make([]models.NodeInfo, 0, len(nodes.Items))
	var totalCPU, totalMem int64
	for _, n := range nodes.Items {
		info := models.NodeInfo{Name: n.Name}
		if cpu, ok := n.Status.Allocatable[corev1.ResourceCPU]; ok {
			info.CPUMilli = cpu.MilliValue()
			totalCPU += info.CPUMilli
		}
		if mem, ok := n.Status.Allocatable[corev1.ResourceMemory]; ok {
			info.MemMi = mem.Value() / (1024 * 1024)
			totalMem += info.MemMi
		}
		for _, cond := range n.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				info.Ready = cond.Status == corev1.ConditionTrue
			}
		}
		if _, ok := n.Labels["node-role.kubernetes.io/control-plane"]; ok {
			info.Role = "control-plane"
		} else {
			info.Role = "worker"
		}
		out = append(out, info)
	}
	return out, totalCPU, totalMem, nil
}

// EnsureUnitNamespace creates the unit namespace and enforces its ResourceQuota/LimitRange.
func (c *Client) EnsureUnitNamespace(ctx context.Context, name, slug, namespace string, cpuMilli, memMi, storageGi int64) error {
	if !strings.HasPrefix(namespace, c.unitNSPrefix) {
		return fmt.Errorf("invalid unit namespace %q", namespace)
	}
	labels := map[string]string{
		PlatformLabel:               "true",
		UnitLabel:                   slug,
		"app.kubernetes.io/part-of": "research-platform",
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace, Labels: labels}}
	if _, err := c.cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: QuotaName, Namespace: namespace, Labels: labels},
		Spec: corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{
			corev1.ResourceRequestsCPU:            *resource.NewMilliQuantity(cpuMilli, resource.DecimalSI),
			corev1.ResourceRequestsMemory:         *resource.NewQuantity(memMi*1024*1024, resource.BinarySI),
			corev1.ResourceLimitsCPU:              *resource.NewMilliQuantity(cpuMilli, resource.DecimalSI),
			corev1.ResourceLimitsMemory:           *resource.NewQuantity(memMi*1024*1024, resource.BinarySI),
			corev1.ResourceRequestsStorage:        *resource.NewQuantity(storageGi*1024*1024*1024, resource.BinarySI),
			corev1.ResourcePersistentVolumeClaims: *resource.NewQuantity(50, resource.DecimalSI),
			corev1.ResourcePods:                   *resource.NewQuantity(50, resource.DecimalSI),
		}},
	}
	if err := c.applyQuota(ctx, quota); err != nil {
		return err
	}

	defReqCPU, defReqMem, defCPU, defMem := int64(200), int64(256), int64(500), int64(512)
	if defReqCPU > cpuMilli {
		defReqCPU = cpuMilli
	}
	if defCPU > cpuMilli {
		defCPU = cpuMilli
	}
	if defReqMem > memMi {
		defReqMem = memMi
	}
	if defMem > memMi {
		defMem = memMi
	}
	limitRange := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: LimitRangeName, Namespace: namespace, Labels: labels},
		Spec: corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{{
			Type: corev1.LimitTypeContainer,
			Max: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(cpuMilli, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(memMi*1024*1024, resource.BinarySI),
			},
			DefaultRequest: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(defReqCPU, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(defReqMem*1024*1024, resource.BinarySI),
			},
			Default: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(defCPU, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(defMem*1024*1024, resource.BinarySI),
			},
		}}},
	}
	if _, err := c.cs.CoreV1().LimitRanges(namespace).Create(ctx, limitRange, metav1.CreateOptions{}); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		existing, getErr := c.cs.CoreV1().LimitRanges(namespace).Get(ctx, LimitRangeName, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}
		limitRange.ResourceVersion = existing.ResourceVersion
		if _, err := c.cs.CoreV1().LimitRanges(namespace).Update(ctx, limitRange, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	_ = name
	return nil
}

func (c *Client) applyQuota(ctx context.Context, quota *corev1.ResourceQuota) error {
	existing, err := c.cs.CoreV1().ResourceQuotas(quota.Namespace).Get(ctx, quota.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = c.cs.CoreV1().ResourceQuotas(quota.Namespace).Create(ctx, quota, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	quota.ResourceVersion = existing.ResourceVersion
	_, err = c.cs.CoreV1().ResourceQuotas(quota.Namespace).Update(ctx, quota, metav1.UpdateOptions{})
	return err
}

func (c *Client) DeleteUnitNamespace(ctx context.Context, namespace string) error {
	if !strings.HasPrefix(namespace, c.unitNSPrefix) {
		return fmt.Errorf("refusing to delete non-unit namespace %q", namespace)
	}
	err := c.cs.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (c *Client) buildPod(e *models.Environment, sshPassword, vncPassword string) *corev1.Pod {
	if vncPassword == "" {
		vncPassword = "changeme"
	}
	labels := map[string]string{
		"app":         "research-workspace",
		PlatformLabel: "true",
		UnitLabel:     e.Namespace,
		UserLabel:     strings.ReplaceAll(e.Username, "_", "-"),
		EnvLabel:      e.PodName,
	}
	annotations := map[string]string{}
	var volumes []corev1.Volume
	var mounts []corev1.VolumeMount
	if e.RootPV {
		annotations["openfuyao.cn/persistent-rootfs"] = "true"
		annotations["openfuyao.cn/rootfs-container"] = "workspace"
		annotations["openfuyao.cn/rootfs-volume"] = "rootfs-data"
		annotations["openfuyao.cn/rootfs-path"] = "containers/" + e.PodName
		annotations["openfuyao.cn/rootfs-base-image-policy"] = "Strict"
		volumes = append(volumes, corev1.Volume{
			Name:         "rootfs-data",
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: e.PVCName}},
		})
	}
	if e.ShmEnabled && e.ShmMi > 0 {
		shmLimit := *resource.NewQuantity(e.ShmMi*1024*1024, resource.BinarySI)
		volumes = append(volumes, corev1.Volume{
			Name: "dev-shm",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium:    corev1.StorageMediumMemory,
					SizeLimit: &shmLimit,
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: "dev-shm", MountPath: "/dev/shm"})
	}
	cpu := *resource.NewMilliQuantity(e.CPUMilli, resource.DecimalSI)
	mem := *resource.NewQuantity(e.MemMi*1024*1024, resource.BinarySI)
	services := "sshd"
	envVars := []corev1.EnvVar{{Name: "ROOT_PW", Value: sshPassword}}
	ports := []corev1.ContainerPort{{Name: "ssh", ContainerPort: e.SSHPort, Protocol: corev1.ProtocolTCP}}
	if e.Graphical {
		services = "sshd,vnc,websockify"
		envVars = append(envVars,
			corev1.EnvVar{Name: "VNC_PW", Value: vncPassword},
			corev1.EnvVar{Name: "VNC_GEOMETRY", Value: "1440x900"},
		)
		ports = append(ports, corev1.ContainerPort{Name: "novnc", ContainerPort: e.VNCPort, Protocol: corev1.ProtocolTCP})
	}
	envVars = append(envVars,
		corev1.EnvVar{Name: "SERVICES", Value: services},
		corev1.EnvVar{Name: "WORKSPACE_NAME", Value: e.Name},
	)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        e.PodName,
			Namespace:   e.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                 corev1.RestartPolicyAlways,
			TerminationGracePeriodSeconds: ptrInt64(15),
			EnableServiceLinks:            ptrBool(false),
			Containers: []corev1.Container{{
				Name:            "workspace",
				Image:           e.Image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Env:             envVars,
				Ports:           ports,
				VolumeMounts:    mounts,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: mem},
					Limits:   corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: mem},
				},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString("ssh")}},
					InitialDelaySeconds: 5,
					PeriodSeconds:       10,
					FailureThreshold:    30,
				},
			}},
			Volumes: volumes,
		},
	}
	if e.RootPV {
		pod.Spec.NodeSelector = map[string]string{rootPVNodeKey: "true"}
	}
	return pod
}

// EnvironmentNameTaken 检查命名空间里是否已有同名 Pod、RootPV 的 PVC 或 Service。
// 用于在创建环境前拦下「主机名在集群中已被占用」的情况，避免直接抛 AlreadyExists。
//
// Service 也要查：主机名同时是 headless Service 的名字，而其他环境会派生
// <名>-ssh / <名>-novnc，用户完全可以用「别人的服务名」当主机名。
func (c *Client) EnvironmentNameTaken(ctx context.Context, namespace, podName string) (bool, error) {
	if _, err := c.cs.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{}); err == nil {
		return true, nil
	} else if !apierrors.IsNotFound(err) {
		return false, err
	}
	if _, err := c.cs.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, podName+"-rootfs", metav1.GetOptions{}); err == nil {
		return true, nil
	} else if !apierrors.IsNotFound(err) {
		return false, err
	}
	for _, svcName := range []string{podName, podName + "-ssh", podName + "-novnc"} {
		if _, err := c.cs.CoreV1().Services(namespace).Get(ctx, svcName, metav1.GetOptions{}); err == nil {
			return true, nil
		} else if !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	return false, nil
}

// CreateEnvironment provisions the persistent volume for a new environment.
// The Pod and Services are only created when the environment is started.
func (c *Client) CreateEnvironment(ctx context.Context, e *models.Environment, sshPassword, vncPassword string) (int32, int32, error) {
	if e.RootPV && e.PVCName != "" {
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      e.PVCName,
				Namespace: e.Namespace,
				Labels:    map[string]string{EnvLabel: e.PodName, PlatformLabel: "true"},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				StorageClassName: ptrString(c.storageClass),
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: *resource.NewQuantity(e.StorageGi*1024*1024*1024, resource.BinarySI)},
				},
			},
		}
		if _, err := c.cs.CoreV1().PersistentVolumeClaims(e.Namespace).Create(ctx, pvc, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return 0, 0, fmt.Errorf("create pvc: %w", err)
		}
	}
	return 0, 0, nil
}

func (c *Client) ensureService(ctx context.Context, e *models.Environment, name string, targetPort, port int32) (*corev1.Service, error) {
	svcName := e.PodName + "-" + name
	existing, err := c.cs.CoreV1().Services(e.Namespace).Get(ctx, svcName, metav1.GetOptions{})
	if err == nil {
		return existing, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: e.Namespace,
			Labels:    map[string]string{EnvLabel: e.PodName, PlatformLabel: "true"},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: map[string]string{EnvLabel: e.PodName},
			Ports: []corev1.ServicePort{{
				Name:       name,
				Port:       port,
				TargetPort: intstr.FromInt32(targetPort),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
	return c.cs.CoreV1().Services(e.Namespace).Create(ctx, svc, metav1.CreateOptions{})
}

// ensureHeadlessService 为环境创建与主机名同名的 headless Service，使同一单位
// （命名空间）内的其他环境能直接用「主机名」解析到该 Pod 的 IP。
//
// 必须是 headless：普通 ClusterIP 由 kube-proxy 只在 TCP/UDP 上做 DNAT，
// 发往 ClusterIP 的 ICMP 会被内核回 port unreachable，ping 不通。
// clusterIP: None 时 DNS 直接返回 Pod IP，不经过 VIP。
//
// PublishNotReadyAddresses 必须开：否则环境刚启动、readiness 未通过的几十秒内
// 该 Pod 不在 endpoints 里，其他环境会解析失败，看起来像环境有问题。
func (c *Client) ensureHeadlessService(ctx context.Context, e *models.Environment) error {
	if _, err := c.cs.CoreV1().Services(e.Namespace).Get(ctx, e.PodName, metav1.GetOptions{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      e.PodName,
			Namespace: e.Namespace,
			Labels: map[string]string{
				EnvLabel:      e.PodName,
				PlatformLabel: "true",
				HeadlessLabel: "true",
			},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  map[string]string{EnvLabel: e.PodName},
			// 不声明 ports：headless Service 只负责让 DNS 返回 Pod IP，客户端连的是
			// Pod IP + 实际端口（22 / 6080 / 业务端口都能通），声明了反而让人误以为
			// 只有列出的端口可达。
			PublishNotReadyAddresses: true,
		},
	}
	if _, err := c.cs.CoreV1().Services(e.Namespace).Create(ctx, svc, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

// EnsureServicesAndPod brings a stopped environment back to running.
func (c *Client) EnsureServicesAndPod(ctx context.Context, e *models.Environment, sshPassword, vncPassword string) error {
	if err := c.ensureHeadlessService(ctx, e); err != nil {
		return err
	}
	sshSvc, err := c.ensureService(ctx, e, "ssh", e.SSHPort, 22)
	if err != nil {
		return err
	}
	var vncSvc *corev1.Service
	if e.Graphical {
		vncSvc, err = c.ensureService(ctx, e, "novnc", e.VNCPort, 6080)
		if err != nil {
			return err
		}
	}
	var sshNP, vncNP int32
	for _, p := range sshSvc.Spec.Ports {
		sshNP = p.NodePort
	}
	if vncSvc != nil {
		for _, p := range vncSvc.Spec.Ports {
			vncNP = p.NodePort
		}
	}
	if e.NodePortSSH != sshNP || e.NodePortVNC != vncNP {
		e.NodePortSSH, e.NodePortVNC = sshNP, vncNP
	}
	pod := c.buildPod(e, sshPassword, vncPassword)
	_, err = c.cs.CoreV1().Pods(e.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

// EnsurePortService creates (or reads) a NodePort Service exposing a custom
// container port. The NodePort is assigned automatically by Kubernetes and
// returned to the caller. It is idempotent by service name.
func (c *Client) EnsurePortService(ctx context.Context, e *models.Environment, svcName string, containerPort int32, protocol corev1.Protocol) (int32, error) {
	existing, err := c.cs.CoreV1().Services(e.Namespace).Get(ctx, svcName, metav1.GetOptions{})
	if err == nil {
		for _, p := range existing.Spec.Ports {
			if p.NodePort > 0 {
				return p.NodePort, nil
			}
		}
		return 0, fmt.Errorf("service %s 尚未分配 NodePort", svcName)
	}
	if !apierrors.IsNotFound(err) {
		return 0, err
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: e.Namespace,
			Labels:    map[string]string{EnvLabel: e.PodName, PlatformLabel: "true", "research-port": "custom"},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: map[string]string{EnvLabel: e.PodName},
			Ports: []corev1.ServicePort{{
				Name:       "port",
				Port:       containerPort,
				TargetPort: intstr.FromInt32(containerPort),
				Protocol:   protocol,
			}},
		},
	}
	created, err := c.cs.CoreV1().Services(e.Namespace).Create(ctx, svc, metav1.CreateOptions{})
	if err != nil {
		return 0, err
	}
	for _, p := range created.Spec.Ports {
		if p.NodePort > 0 {
			return p.NodePort, nil
		}
	}
	return 0, fmt.Errorf("service %s 创建后未分配 NodePort", svcName)
}

func (c *Client) StopEnvironment(ctx context.Context, e *models.Environment) error {
	err := c.cs.CoreV1().Pods(e.Namespace).Delete(ctx, e.PodName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (c *Client) ResizePVC(ctx context.Context, namespace, pvcName string, storageGi int64) error {
	if pvcName == "" || storageGi <= 0 {
		return nil
	}
	pvc, err := c.cs.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("获取 PVC 失败: %w", err)
	}
	want := resource.MustParse(fmt.Sprintf("%dGi", storageGi))
	current := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if current.Cmp(want) >= 0 {
		return nil
	}
	patch := fmt.Sprintf(`{"spec":{"resources":{"requests":{"storage":%q}}}}`, want.String())
	_, err = c.cs.CoreV1().PersistentVolumeClaims(namespace).Patch(ctx, pvcName, types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("扩容 PVC 失败: %w", err)
	}
	return nil
}

func (c *Client) DeleteService(ctx context.Context, namespace, name string) error {
	if name == "" {
		return nil
	}
	err := c.cs.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// RestartEnvironment force-deletes the pod, waits for it to disappear and recreates it.
func (c *Client) RestartEnvironment(ctx context.Context, e *models.Environment, sshPassword, vncPassword string) error {
	grace := int64(0)
	err := c.cs.CoreV1().Pods(e.Namespace).Delete(ctx, e.PodName, metav1.DeleteOptions{GracePeriodSeconds: &grace})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	deadline := time.Now().Add(25 * time.Second)
	for {
		_, getErr := c.cs.CoreV1().Pods(e.Namespace).Get(ctx, e.PodName, metav1.GetOptions{})
		if apierrors.IsNotFound(getErr) {
			break
		}
		if getErr != nil && !apierrors.IsNotFound(getErr) {
			return getErr
		}
		if time.Now().After(deadline) {
			return errors.New("等待旧 Pod 释放超时")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
	}
	// Give the RootPV single-writer lease a moment to be released.
	time.Sleep(2 * time.Second)
	return c.EnsureServicesAndPod(ctx, e, sshPassword, vncPassword)
}

func (c *Client) DeleteEnvironmentResources(ctx context.Context, e *models.Environment) error {
	var errs []string
	if err := c.cs.CoreV1().Pods(e.Namespace).Delete(ctx, e.PodName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		errs = append(errs, "pod: "+err.Error())
	}
	// e.PodName 是 headless Service（主机名解析），另外两个是 NodePort。
	for _, name := range []string{e.PodName, e.PodName + "-ssh", e.PodName + "-novnc"} {
		if err := c.cs.CoreV1().Services(e.Namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, "service "+name+": "+err.Error())
		}
	}
	// Delete all custom port services belonging to this environment.
	svcs, listErr := c.cs.CoreV1().Services(e.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: EnvLabel + "=" + e.PodName + ",research-port=custom",
	})
	if listErr != nil {
		errs = append(errs, "list custom port services: "+listErr.Error())
	} else {
		for i := range svcs.Items {
			name := svcs.Items[i].Name
			if err := c.cs.CoreV1().Services(e.Namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				errs = append(errs, "service "+name+": "+err.Error())
			}
		}
	}
	if e.PVCName != "" {
		if err := c.cs.CoreV1().PersistentVolumeClaims(e.Namespace).Delete(ctx, e.PVCName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, "pvc: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ---------- volume snapshots ----------

type SnapshotState struct {
	Ready        bool
	SizeBytes    int64
	Error        string
	CreationTime time.Time
}

func (c *Client) snapshotClassFor(ctx context.Context, pvc *corev1.PersistentVolumeClaim) (string, error) {
	scName := c.storageClass
	if pvc.Spec.StorageClassName != nil && *pvc.Spec.StorageClassName != "" {
		scName = *pvc.Spec.StorageClassName
	}
	driver := ""
	if scName != "" {
		if sc, err := c.cs.StorageV1().StorageClasses().Get(ctx, scName, metav1.GetOptions{}); err == nil {
			driver = sc.Provisioner
		} else if !apierrors.IsNotFound(err) {
			return "", err
		}
	}
	list, err := c.dyn.Resource(snapshotClassGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("读取卷快照类失败: %w", err)
	}
	var matched []string
	for _, item := range list.Items {
		d, _, _ := unstructured.NestedString(item.Object, "driver")
		if driver != "" && d != driver {
			continue
		}
		if item.GetAnnotations()["snapshot.storage.kubernetes.io/is-default-class"] == "true" {
			return item.GetName(), nil
		}
		matched = append(matched, item.GetName())
	}
	if len(matched) == 1 {
		return matched[0], nil
	}
	if len(matched) == 0 {
		if driver != "" {
			return "", fmt.Errorf("未找到与存储驱动 %s 匹配的卷快照类", driver)
		}
		return "", errors.New("集群未配置卷快照类")
	}
	return "", errors.New("存在多个卷快照类，请为存储类设置默认快照类")
}

func (c *Client) SyncEnvironmentFilesystem(ctx context.Context, e *models.Environment) error {
	pod, err := c.cs.CoreV1().Pods(e.Namespace).Get(ctx, e.PodName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if pod.Status.Phase != corev1.PodRunning {
		return nil
	}
	req := c.cs.CoreV1().RESTClient().Post().
		Resource("pods").Name(e.PodName).Namespace(e.Namespace).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "workspace",
			Command:   []string{"sync"},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(c.cfg, "POST", req.URL())
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: io.Discard, Stderr: &stderr}); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

func (c *Client) CreateVolumeSnapshot(ctx context.Context, namespace, pvcName, k8sName, envPodName string) error {
	pvc, err := c.cs.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, pvcName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return errors.New("根文件系统 PVC 不存在，无法创建快照")
		}
		return fmt.Errorf("读取根文件系统 PVC 失败: %w", err)
	}
	if pvc.Status.Phase != corev1.ClaimBound {
		return errors.New("根文件系统 PVC 尚未就绪，请稍后再试")
	}
	className, err := c.snapshotClassFor(ctx, pvc)
	if err != nil {
		return err
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "snapshot.storage.k8s.io/v1",
		"kind":       "VolumeSnapshot",
		"metadata": map[string]any{
			"name":      k8sName,
			"namespace": namespace,
			"labels": map[string]any{
				"research-platform": "true",
				EnvLabel:            envPodName,
				"research-snapshot": "true",
			},
		},
		"spec": map[string]any{
			"volumeSnapshotClassName": className,
			"source": map[string]any{
				"persistentVolumeClaimName": pvcName,
			},
		},
	}}
	if _, err := c.dyn.Resource(snapshotGVR).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("创建卷快照失败: %w", err)
	}
	return nil
}

func (c *Client) VolumeSnapshotState(ctx context.Context, namespace, k8sName string) (SnapshotState, error) {
	obj, err := c.dyn.Resource(snapshotGVR).Namespace(namespace).Get(ctx, k8sName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return SnapshotState{Error: "快照对象不存在"}, errors.New("快照对象不存在")
		}
		return SnapshotState{}, err
	}
	var st SnapshotState
	st.Ready, _, _ = unstructured.NestedBool(obj.Object, "status", "readyToUse")
	st.Error, _, _ = unstructured.NestedString(obj.Object, "status", "error", "message")
	if raw, ok, _ := unstructured.NestedString(obj.Object, "status", "restoreSize"); ok && raw != "" {
		if q, err := resource.ParseQuantity(raw); err == nil {
			st.SizeBytes = q.Value()
		}
	}
	if raw, ok, _ := unstructured.NestedString(obj.Object, "status", "creationTime"); ok && raw != "" {
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			st.CreationTime = t
		}
	}
	return st, nil
}

func (c *Client) DeleteVolumeSnapshot(ctx context.Context, namespace, k8sName string) error {
	err := c.dyn.Resource(snapshotGVR).Namespace(namespace).Delete(ctx, k8sName, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("删除卷快照失败: %w", err)
	}
	return nil
}

// WaitVolumeSnapshotsDeleted waits until the snapshot objects are fully gone so
// the PVC source-protection finalizer can be released before the PVC is deleted.
func (c *Client) WaitVolumeSnapshotsDeleted(ctx context.Context, namespace string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		remaining := 0
		for _, name := range names {
			if _, err := c.dyn.Resource(snapshotGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{}); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				return err
			}
			remaining++
		}
		if remaining == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待快照清理超时（剩余 %d 个）", remaining)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// RestoreVolumeSnapshot recreates the environment PVC in place from a snapshot,
// keeping the original PVC name.
func (c *Client) RestoreVolumeSnapshot(ctx context.Context, e *models.Environment, k8sName string) error {
	st, err := c.VolumeSnapshotState(ctx, e.Namespace, k8sName)
	if err != nil {
		return err
	}
	if st.Error != "" {
		return fmt.Errorf("快照不可用: %s", st.Error)
	}
	if !st.Ready {
		return errors.New("快照尚未就绪，暂不能恢复")
	}
	if _, err := c.cs.CoreV1().Pods(e.Namespace).Get(ctx, e.PodName, metav1.GetOptions{}); err == nil {
		return errors.New("请先停止环境后再恢复快照")
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	accessModes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	scName := c.storageClass
	sizeGi := e.StorageGi
	existing, err := c.cs.CoreV1().PersistentVolumeClaims(e.Namespace).Get(ctx, e.PVCName, metav1.GetOptions{})
	if err == nil {
		if existing.Spec.StorageClassName != nil && *existing.Spec.StorageClassName != "" {
			scName = *existing.Spec.StorageClassName
		}
		if len(existing.Spec.AccessModes) > 0 {
			accessModes = existing.Spec.AccessModes
		}
		if q, ok := existing.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			if gi := (q.Value() + 1024*1024*1024 - 1) / (1024 * 1024 * 1024); gi > sizeGi {
				sizeGi = gi
			}
		}
		if err := c.cs.CoreV1().PersistentVolumeClaims(e.Namespace).Delete(ctx, e.PVCName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("删除原 PVC 失败: %w", err)
		}
		deadline := time.Now().Add(60 * time.Second)
		for {
			_, getErr := c.cs.CoreV1().PersistentVolumeClaims(e.Namespace).Get(ctx, e.PVCName, metav1.GetOptions{})
			if apierrors.IsNotFound(getErr) {
				break
			}
			if getErr != nil && !apierrors.IsNotFound(getErr) {
				return getErr
			}
			if time.Now().After(deadline) {
				return errors.New("等待原 PVC 释放超时，请确认环境已停止")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("读取原 PVC 失败: %w", err)
	}
	if st.SizeBytes > 0 {
		if gi := (st.SizeBytes + 1024*1024*1024 - 1) / (1024 * 1024 * 1024); gi > sizeGi {
			sizeGi = gi
		}
	}
	if sizeGi <= 0 {
		sizeGi = 1
	}

	restored := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      e.PVCName,
			Namespace: e.Namespace,
			Labels:    map[string]string{EnvLabel: e.PodName, PlatformLabel: "true"},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      accessModes,
			StorageClassName: ptrString(scName),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: *resource.NewQuantity(sizeGi*1024*1024*1024, resource.BinarySI)},
			},
			DataSource: &corev1.TypedLocalObjectReference{
				APIGroup: ptrString("snapshot.storage.k8s.io"),
				Kind:     "VolumeSnapshot",
				Name:     k8sName,
			},
		},
	}
	if _, err := c.cs.CoreV1().PersistentVolumeClaims(e.Namespace).Create(ctx, restored, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("从快照重建 PVC 失败: %w", err)
	}

	deadline := time.Now().Add(120 * time.Second)
	for {
		pvc, getErr := c.cs.CoreV1().PersistentVolumeClaims(e.Namespace).Get(ctx, e.PVCName, metav1.GetOptions{})
		if getErr != nil {
			if apierrors.IsNotFound(getErr) {
				return errors.New("从快照重建的 PVC 不存在")
			}
			return getErr
		}
		if pvc.Status.Phase == corev1.ClaimBound {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("PVC 已重建，但等待快照数据恢复超时，请稍后刷新")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// PodState returns a normalized state and human readable message.
func (c *Client) PodState(ctx context.Context, e *models.Environment) (string, string) {
	pod, err := c.cs.CoreV1().Pods(e.Namespace).Get(ctx, e.PodName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if e.DesiredState == "stopped" {
			if e.RootPV {
				return "stopped", "环境已停止，根文件系统数据保留。"
			}
			return "stopped", "环境已停止，临时环境数据不保留。"
		}
		return "pending", "Pod 尚未创建"
	}
	if err != nil {
		return e.Status, err.Error()
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "ContainerCreating" {
			return "error", cs.State.Waiting.Reason + ": " + cs.State.Waiting.Message
		}
		if cs.State.Terminated != nil {
			return "stopped", fmt.Sprintf("容器已退出 (%s)", cs.State.Terminated.Reason)
		}
	}
	switch pod.Status.Phase {
	case corev1.PodRunning:
		ready := true
		for _, cs := range pod.Status.ContainerStatuses {
			if !cs.Ready {
				ready = false
			}
		}
		if ready {
			return "running", "环境运行中"
		}
		return "starting", "容器启动中"
	case corev1.PodPending:
		return "starting", "调度/启动中"
	case corev1.PodFailed:
		return "error", pod.Status.Message
	case corev1.PodSucceeded:
		return "stopped", "容器已结束"
	default:
		return "pending", string(pod.Status.Phase)
	}
}

// SyncNodeInfo refreshes environment status from the cluster.
func (c *Client) SyncEnvironment(ctx context.Context, e *models.Environment) (string, string) {
	status, msg := c.PodState(ctx, e)
	if e.NodePortSSH == 0 || (e.Graphical && e.NodePortVNC == 0) {
		if svc, err := c.cs.CoreV1().Services(e.Namespace).Get(ctx, e.PodName+"-ssh", metav1.GetOptions{}); err == nil {
			for _, p := range svc.Spec.Ports {
				e.NodePortSSH = p.NodePort
			}
		}
		if e.Graphical {
			if svc, err := c.cs.CoreV1().Services(e.Namespace).Get(ctx, e.PodName+"-novnc", metav1.GetOptions{}); err == nil {
				for _, p := range svc.Spec.Ports {
					e.NodePortVNC = p.NodePort
				}
			}
		}
	}
	return status, msg
}

func ptrInt64(v int64) *int64    { return &v }
func ptrBool(v bool) *bool       { return &v }
func ptrString(v string) *string { return &v }
