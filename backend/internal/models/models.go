package models

import "time"

type User struct {
	ID              int64     `json:"id"`
	Username        string    `json:"username"`
	PasswordHash    string    `json:"-"`
	DisplayName     string    `json:"display_name"`
	Email           string    `json:"email"`
	IsPlatformAdmin bool      `json:"is_platform_admin"`
	IsUnitAdmin     bool      `json:"is_unit_admin"`
	UnitNames       string    `json:"unit_names,omitempty"`
	Disabled        bool      `json:"disabled"`
	CreatedAt       time.Time `json:"created_at"`
}

type Unit struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Namespace   string    `json:"namespace"`
	Description string    `json:"description"`
	CPUMilli    int64     `json:"cpu_milli"`
	MemMi       int64     `json:"mem_mi"`
	StorageGi   int64     `json:"storage_gi"`
	Disabled    bool      `json:"disabled"`
	CreatedAt   time.Time `json:"created_at"`
}

type UnitView struct {
	Unit
	MemberCount        int   `json:"member_count"`
	EnvCount           int   `json:"env_count"`
	UsedCPUMilli       int64 `json:"used_cpu_milli"`
	UsedMemMi          int64 `json:"used_mem_mi"`
	UsedStorageGi      int64 `json:"used_storage_gi"`
	AllocatedStorageGi int64 `json:"allocated_storage_gi"`
}

type Membership struct {
	ID            int64     `json:"id"`
	UnitID        int64     `json:"unit_id"`
	UserID        int64     `json:"user_id"`
	Role          string    `json:"role"`
	CPUMilli      int64     `json:"cpu_milli"`
	MemMi         int64     `json:"mem_mi"`
	StorageGi     int64     `json:"storage_gi"`
	CreatedAt     time.Time `json:"created_at"`
	Username      string    `json:"username,omitempty"`
	DisplayName   string    `json:"display_name,omitempty"`
	UnitName      string    `json:"unit_name,omitempty"`
	UnitSlug      string    `json:"unit_slug,omitempty"`
	UsedCPUMilli  int64     `json:"used_cpu_milli,omitempty"`
	UsedMemMi     int64     `json:"used_mem_mi,omitempty"`
	UsedStorageGi int64     `json:"used_storage_gi,omitempty"`
	EnvCount      int       `json:"env_count,omitempty"`
}

type Template struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Image       string    `json:"image"`
	RootPV      bool      `json:"rootpv"`
	Ephemeral   bool      `json:"ephemeral"`
	CPUMilli    int64     `json:"cpu_milli"`
	MemMi       int64     `json:"mem_mi"`
	StorageGi   int64     `json:"storage_gi"`
	SSHPort     int32     `json:"ssh_port"`
	VNCPort     int32     `json:"vnc_port"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
}

type Environment struct {
	ID             int64         `json:"id"`
	Name           string        `json:"name"`
	UnitID         int64         `json:"unit_id"`
	UserID         int64         `json:"user_id"`
	TemplateID     int64         `json:"template_id"`
	Namespace      string        `json:"namespace"`
	PodName        string        `json:"pod_name"`
	PVCName        string        `json:"pvc_name"`
	SSHService     string        `json:"ssh_service"`
	VNCService     string        `json:"vnc_service"`
	Image          string        `json:"image"`
	CPUMilli       int64         `json:"cpu_milli"`
	MemMi          int64         `json:"mem_mi"`
	StorageGi      int64         `json:"storage_gi"`
	RootPV         bool          `json:"rootpv"`
	Graphical      bool          `json:"graphical"`
	ShmEnabled     bool          `json:"shm_enabled"`
	ShmMi          int64         `json:"shm_mi"`
	SSHPort        int32         `json:"ssh_port"`
	VNCPort        int32         `json:"vnc_port"`
	NodePortSSH    int32         `json:"node_port_ssh"`
	NodePortVNC    int32         `json:"node_port_vnc"`
	SSHUser        string        `json:"ssh_user"`
	SSHPasswordEnc string        `json:"-"`
	VNCPasswordEnc string        `json:"-"`
	DesiredState   string        `json:"desired_state"`
	Status         string        `json:"status"`
	StatusMessage  string        `json:"status_message"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	UnitName       string        `json:"unit_name,omitempty"`
	UnitSlug       string        `json:"unit_slug,omitempty"`
	Username       string        `json:"username,omitempty"`
	TemplateName   string        `json:"template_name,omitempty"`
	Ports          []PortMapping `json:"ports,omitempty"`
}

type EnvironmentView struct {
	Environment
	SSHConnection *SSHConnection `json:"ssh,omitempty"`
	VNCConnection *VNCConnection `json:"vnc,omitempty"`
}

// EnvPort is a user-defined container port exposed through a NodePort Service.
type EnvPort struct {
	ID            int64     `json:"id"`
	EnvID         int64     `json:"env_id"`
	ContainerPort int32     `json:"container_port"`
	NodePort      int32     `json:"node_port"`
	ServiceName   string    `json:"service_name"`
	Protocol      string    `json:"protocol"`
	CreatedAt     time.Time `json:"created_at"`
}

// PortMapping is a row in the environment port mapping view. Builtin mappings
// (SSH / VNC) are managed by the platform and cannot be deleted.
type PortMapping struct {
	ID            int64  `json:"id"`
	Kind          string `json:"kind"` // ssh | vnc | custom
	Name          string `json:"name"`
	ContainerPort int32  `json:"container_port"`
	NodePort      int32  `json:"node_port"`
	Protocol      string `json:"protocol"`
	Builtin       bool   `json:"builtin"`
	Address       string `json:"address"`
}

type PortListView struct {
	Items  []PortMapping `json:"items"`
	Max    int           `json:"max"`
	Host   string        `json:"host"`
	Count  int           `json:"count"`
	CanAdd bool          `json:"can_add"`
}

type Snapshot struct {
	ID          int64     `json:"id"`
	EnvID       int64     `json:"env_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	K8sName     string    `json:"k8s_name"`
	Status      string    `json:"status"`
	SizeBytes   int64     `json:"size_bytes"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type AuditLog struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

type SSHConnection struct {
	Host     string `json:"host"`
	Port     int32  `json:"port"`
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	Command  string `json:"command"`
}

type VNCConnection struct {
	URL      string `json:"url"`
	Host     string `json:"host"`
	Port     int32  `json:"port"`
	Password string `json:"password,omitempty"`
}

type Usage struct {
	CPUMilli           int64 `json:"cpu_milli"`
	MemMi              int64 `json:"mem_mi"`
	StorageGi          int64 `json:"storage_gi"`
	StorageAllocatedGi int64 `json:"storage_allocated_gi,omitempty"`
	EnvCount           int   `json:"env_count"`
}

type PlatformLimits struct {
	CPUMilli  int64 `json:"cpu_milli"`
	MemMi     int64 `json:"mem_mi"`
	StorageGi int64 `json:"storage_gi"`
}

type NodeInfo struct {
	Name     string `json:"name"`
	CPUMilli int64  `json:"cpu_milli"`
	MemMi    int64  `json:"mem_mi"`
	Ready    bool   `json:"ready"`
	Role     string `json:"role"`
}

type Overview struct {
	Nodes           []NodeInfo     `json:"nodes"`
	ClusterCPUMilli int64          `json:"cluster_cpu_milli"`
	ClusterMemMi    int64          `json:"cluster_mem_mi"`
	PlatformLimits  PlatformLimits `json:"platform_limits"`
	Allocated       PlatformLimits `json:"allocated"`
	UnitCount       int            `json:"unit_count"`
	UserCount       int            `json:"user_count"`
	EnvCount        int            `json:"env_count"`
	RunningEnvCount int            `json:"running_env_count"`
	StorageClass    string         `json:"storage_class"`
}
