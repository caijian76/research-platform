package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr       string
	DatabaseURL      string
	JWTSecret        string
	SecretKey        string
	PublicHost       string
	UnitNSPrefix     string
	StorageClass     string
	AdminUsername    string
	AdminPassword    string
	SeedTemplateImage string
	PlatformStorageGi int64
}

func getenv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func Load() Config {
	storageGi, _ := strconv.ParseInt(getenv("PLATFORM_STORAGE_GI", "60"), 10, 64)
	if storageGi <= 0 {
		storageGi = 60
	}
	return Config{
		ListenAddr:        getenv("LISTEN_ADDR", ":8080"),
		DatabaseURL:       getenv("DATABASE_URL", "postgres://research:research@localhost:5432/research?sslmode=disable"),
		JWTSecret:         getenv("JWT_SECRET", "research-platform-dev-jwt-secret-change-me"),
		SecretKey:         getenv("SECRET_KEY", ""),
		PublicHost:        getenv("PUBLIC_HOST", ""),
		UnitNSPrefix:      getenv("UNIT_NS_PREFIX", "research-unit-"),
		StorageClass:      getenv("STORAGE_CLASS", "rook-ceph-block"),
		AdminUsername:     getenv("ADMIN_USERNAME", "admin"),
		AdminPassword:     getenv("ADMIN_PASSWORD", "Admin@12345"),
		SeedTemplateImage: getenv("SEED_TEMPLATE_IMAGE", "registry.cn-hangzhou.aliyuncs.com/jackc/ubuntu-24-allinone:v0.4.0"),
		PlatformStorageGi: storageGi,
	}
}