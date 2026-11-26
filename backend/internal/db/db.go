package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"research-platform/backend/internal/models"
)

type DB struct {
	pool *pgxpool.Pool
}

var ErrNotFound = errors.New("not found")
var ErrSnapshotLimit = errors.New("snapshot limit reached")

const SnapshotLimitPerEnv = 5

func Open(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{pool: pool}, nil
}

func (d *DB) Close() { d.pool.Close() }

func (d *DB) Ping(ctx context.Context) error { return d.pool.Ping(ctx) }

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id BIGSERIAL PRIMARY KEY,
	username TEXT UNIQUE NOT NULL,
	password_hash TEXT NOT NULL,
	display_name TEXT NOT NULL DEFAULT '',
	email TEXT NOT NULL DEFAULT '',
	is_platform_admin BOOLEAN NOT NULL DEFAULT FALSE,
	disabled BOOLEAN NOT NULL DEFAULT FALSE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS units (
	id BIGSERIAL PRIMARY KEY,
	name TEXT NOT NULL,
	slug TEXT UNIQUE NOT NULL,
	namespace TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	cpu_milli BIGINT NOT NULL DEFAULT 0,
	mem_mi BIGINT NOT NULL DEFAULT 0,
	storage_gi BIGINT NOT NULL DEFAULT 0,
	disabled BOOLEAN NOT NULL DEFAULT FALSE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS memberships (
	id BIGSERIAL PRIMARY KEY,
	unit_id BIGINT NOT NULL REFERENCES units(id) ON DELETE CASCADE,
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role TEXT NOT NULL DEFAULT 'member',
	cpu_milli BIGINT NOT NULL DEFAULT 0,
	mem_mi BIGINT NOT NULL DEFAULT 0,
	storage_gi BIGINT NOT NULL DEFAULT 0,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE(unit_id, user_id)
);
CREATE TABLE IF NOT EXISTS templates (
	id BIGSERIAL PRIMARY KEY,
	name TEXT UNIQUE NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	image TEXT NOT NULL,
	rootpv BOOLEAN NOT NULL DEFAULT TRUE,
	ephemeral BOOLEAN NOT NULL DEFAULT FALSE,
	cpu_milli BIGINT NOT NULL,
	mem_mi BIGINT NOT NULL,
	storage_gi BIGINT NOT NULL,
	ssh_port INT NOT NULL DEFAULT 22,
	vnc_port INT NOT NULL DEFAULT 6080,
	enabled BOOLEAN NOT NULL DEFAULT TRUE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS environments (
	id BIGSERIAL PRIMARY KEY,
	name TEXT NOT NULL,
	unit_id BIGINT NOT NULL REFERENCES units(id) ON DELETE CASCADE,
	user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	template_id BIGINT NOT NULL DEFAULT 0,
	namespace TEXT NOT NULL,
	pod_name TEXT NOT NULL,
	pvc_name TEXT NOT NULL DEFAULT '',
	ssh_service TEXT NOT NULL DEFAULT '',
	vnc_service TEXT NOT NULL DEFAULT '',
	image TEXT NOT NULL DEFAULT '',
	cpu_milli BIGINT NOT NULL,
	mem_mi BIGINT NOT NULL,
	storage_gi BIGINT NOT NULL DEFAULT 0,
	rootpv BOOLEAN NOT NULL DEFAULT TRUE,
	graphical BOOLEAN NOT NULL DEFAULT TRUE,
	shm_enabled BOOLEAN NOT NULL DEFAULT FALSE,
	shm_mi BIGINT NOT NULL DEFAULT 0,
	ssh_port INT NOT NULL DEFAULT 22,
	vnc_port INT NOT NULL DEFAULT 6080,
	node_port_ssh INT NOT NULL DEFAULT 0,
	node_port_vnc INT NOT NULL DEFAULT 0,
	ssh_user TEXT NOT NULL DEFAULT 'root',
	ssh_password_enc TEXT NOT NULL DEFAULT '',
	vnc_password_enc TEXT NOT NULL DEFAULT '',
	desired_state TEXT NOT NULL DEFAULT 'running',
	status TEXT NOT NULL DEFAULT 'creating',
	status_message TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_env_unit ON environments(unit_id);
CREATE INDEX IF NOT EXISTS idx_env_user ON environments(user_id);
-- 主机名即 Pod 名称，同一命名空间（二级单位）内不允许重名
CREATE UNIQUE INDEX IF NOT EXISTS idx_env_pod_name ON environments(namespace, pod_name);
CREATE TABLE IF NOT EXISTS env_ports (
	id BIGSERIAL PRIMARY KEY,
	env_id BIGINT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
	container_port INT NOT NULL,
	node_port INT NOT NULL DEFAULT 0,
	service_name TEXT NOT NULL DEFAULT '',
	protocol TEXT NOT NULL DEFAULT 'TCP',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_env_port_env ON env_ports(env_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_env_port_unique ON env_ports(env_id, container_port, protocol);
CREATE TABLE IF NOT EXISTS snapshots (
	id BIGSERIAL PRIMARY KEY,
	env_id BIGINT NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	k8s_name TEXT UNIQUE NOT NULL,
	status TEXT NOT NULL DEFAULT 'creating',
	size_bytes BIGINT NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_snapshot_env ON snapshots(env_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_snapshot_env_name ON snapshots(env_id, lower(name));
CREATE TABLE IF NOT EXISTS settings (
	id INT PRIMARY KEY DEFAULT 1,
	cpu_milli BIGINT NOT NULL DEFAULT 0,
	mem_mi BIGINT NOT NULL DEFAULT 0,
	storage_gi BIGINT NOT NULL DEFAULT 0
);
INSERT INTO settings (id) VALUES (1) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS audit_logs (
	id BIGSERIAL PRIMARY KEY,
	user_id BIGINT NOT NULL DEFAULT 0,
	username TEXT NOT NULL DEFAULT '',
	action TEXT NOT NULL,
	target TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

func (d *DB) Migrate(ctx context.Context) error {
	if _, err := d.pool.Exec(ctx, schema); err != nil {
		return err
	}
	if _, err := d.pool.Exec(ctx, `ALTER TABLE templates ADD COLUMN IF NOT EXISTS ephemeral BOOLEAN NOT NULL DEFAULT FALSE`); err != nil {
		return err
	}
	if _, err := d.pool.Exec(ctx, `UPDATE templates SET ephemeral=TRUE WHERE rootpv=FALSE`); err != nil {
		return err
	}
	_, err := d.pool.Exec(ctx, `ALTER TABLE environments ADD COLUMN IF NOT EXISTS graphical BOOLEAN NOT NULL DEFAULT TRUE`)
	if err != nil {
		return err
	}
	_, err = d.pool.Exec(ctx, `ALTER TABLE environments ADD COLUMN IF NOT EXISTS shm_enabled BOOLEAN NOT NULL DEFAULT FALSE`)
	if err != nil {
		return err
	}
	_, err = d.pool.Exec(ctx, `ALTER TABLE environments ADD COLUMN IF NOT EXISTS shm_mi BIGINT NOT NULL DEFAULT 0`)
	return err
}

func (d *DB) Seed(ctx context.Context, adminUser, adminPass, templateImage string) error {
	var count int
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		hash, err := bcrypt.GenerateFromPassword([]byte(adminPass), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		if _, err := d.pool.Exec(ctx,
			`INSERT INTO users (username, password_hash, display_name, is_platform_admin) VALUES ($1,$2,$3,TRUE)`,
			adminUser, string(hash), "平台管理员"); err != nil {
			return err
		}
	}
	if err := d.pool.QueryRow(ctx, `SELECT count(*) FROM templates`).Scan(&count); err != nil {
		return err
	}
	if count == 0 && templateImage != "" {
		if _, err := d.pool.Exec(ctx, `INSERT INTO templates
			(name, description, image, rootpv, ephemeral, cpu_milli, mem_mi, storage_gi, ssh_port, vnc_port, enabled)
			VALUES ($1,$2,$3,TRUE,TRUE,2000,4096,10,22,6901,TRUE)`,
			"Ubuntu 24 一体化科研桌面 (RootPV)",
			"支持持久化或临时模式的 Ubuntu 24 桌面环境，内置 sshd、VNC 与 websockify(6901)。",
			templateImage); err != nil {
			return err
		}
		if _, err := d.pool.Exec(ctx, `INSERT INTO templates
			(name, description, image, rootpv, ephemeral, cpu_milli, mem_mi, storage_gi, ssh_port, vnc_port, enabled)
			VALUES ($1,$2,$3,FALSE,TRUE,1000,2048,0,22,6901,TRUE)`,
			"Ubuntu 24 临时环境",
			"非持久化 Ubuntu 24 桌面环境，适合临时计算任务。",
			templateImage); err != nil {
			return err
		}
	}
	return nil
}

// ---------- users ----------

const userCols = `id, username, password_hash, display_name, email, is_platform_admin, disabled, created_at`

func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Email, &u.IsPlatformAdmin, &u.Disabled, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (d *DB) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	return scanUser(d.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE username=$1`, username))
}

func (d *DB) GetUserByID(ctx context.Context, id int64) (*models.User, error) {
	return scanUser(d.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}

func (d *DB) ListUsers(ctx context.Context) ([]models.User, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT u.id, u.username, u.password_hash, u.display_name, u.email,
		       u.is_platform_admin, u.disabled, u.created_at,
		       COALESCE(bool_or(m.role = 'unit_admin'), false),
		       COALESCE(string_agg(DISTINCT un.name, '、'), '')
		FROM users u
		LEFT JOIN memberships m ON m.user_id = u.id
		LEFT JOIN units un ON un.id = m.unit_id
		GROUP BY u.id
		ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.User{}
	for rows.Next() {
		var u models.User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Email, &u.IsPlatformAdmin, &u.Disabled, &u.CreatedAt, &u.IsUnitAdmin, &u.UnitNames); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (d *DB) CreateUser(ctx context.Context, username, password, displayName, email string, isAdmin bool) (*models.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return scanUser(d.pool.QueryRow(ctx, `INSERT INTO users (username,password_hash,display_name,email,is_platform_admin)
		VALUES ($1,$2,$3,$4,$5) RETURNING `+userCols, username, string(hash), displayName, email, isAdmin))
}

func (d *DB) CreateUserWithMembership(ctx context.Context, username, password, displayName, email string, isAdmin bool, role string, unitID int64) (*models.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	user, err := scanUser(tx.QueryRow(ctx, `INSERT INTO users (username,password_hash,display_name,email,is_platform_admin)
		VALUES ($1,$2,$3,$4,$5) RETURNING `+userCols, username, string(hash), displayName, email, isAdmin))
	if err != nil {
		return nil, err
	}
	if unitID > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO memberships (unit_id,user_id,role,cpu_milli,mem_mi,storage_gi)
			VALUES ($1,$2,$3,0,0,0)`, unitID, user.ID, role); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return user, nil
}

func (d *DB) UpdateUser(ctx context.Context, id int64, displayName, email string, isAdmin, disabled bool, password string) error {
	var pw any
	if password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		pw = string(hash)
	}
	_, err := d.pool.Exec(ctx, `UPDATE users SET
		display_name=$2, email=$3, is_platform_admin=$4, disabled=$5,
		password_hash=COALESCE($6, password_hash) WHERE id=$1`,
		id, displayName, email, isAdmin, disabled, pw)
	return err
}

func (d *DB) UpdateUserAndRoles(ctx context.Context, id int64, displayName, email string, isAdmin, disabled bool, password, role string, syncRole bool) error {
	var pw any
	if password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		pw = string(hash)
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE users SET
		display_name=$2, email=$3, is_platform_admin=$4, disabled=$5,
		password_hash=COALESCE($6, password_hash) WHERE id=$1`,
		id, displayName, email, isAdmin, disabled, pw); err != nil {
		return err
	}
	if syncRole {
		if _, err := tx.Exec(ctx, `UPDATE memberships SET role=$2 WHERE user_id=$1`, id, role); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (d *DB) ChangePassword(ctx context.Context, id int64, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = d.pool.Exec(ctx, `UPDATE users SET password_hash=$2 WHERE id=$1`, id, string(hash))
	return err
}

func (d *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := d.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (d *DB) CountPlatformAdmins(ctx context.Context) (int, error) {
	var n int
	err := d.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE is_platform_admin`).Scan(&n)
	return n, err
}

func (d *DB) DeleteUser(ctx context.Context, id int64) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	return err
}

// ---------- units ----------

const unitCols = `id, name, slug, namespace, description, cpu_milli, mem_mi, storage_gi, disabled, created_at`

func scanUnit(row pgx.Row) (*models.Unit, error) {
	var u models.Unit
	err := row.Scan(&u.ID, &u.Name, &u.Slug, &u.Namespace, &u.Description, &u.CPUMilli, &u.MemMi, &u.StorageGi, &u.Disabled, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (d *DB) CreateUnit(ctx context.Context, name, slug, ns, desc string, cpu, mem, storage int64) (*models.Unit, error) {
	return scanUnit(d.pool.QueryRow(ctx, `INSERT INTO units (name,slug,namespace,description,cpu_milli,mem_mi,storage_gi)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+unitCols, name, slug, ns, desc, cpu, mem, storage))
}

func (d *DB) GetUnit(ctx context.Context, id int64) (*models.Unit, error) {
	return scanUnit(d.pool.QueryRow(ctx, `SELECT `+unitCols+` FROM units WHERE id=$1`, id))
}

func (d *DB) GetUnitBySlug(ctx context.Context, slug string) (*models.Unit, error) {
	return scanUnit(d.pool.QueryRow(ctx, `SELECT `+unitCols+` FROM units WHERE slug=$1`, slug))
}

func (d *DB) ListUnits(ctx context.Context) ([]models.UnitView, error) {
	rows, err := d.pool.Query(ctx, `SELECT u.id,u.name,u.slug,u.namespace,u.description,u.cpu_milli,u.mem_mi,u.storage_gi,u.disabled,u.created_at,
		(SELECT count(*) FROM memberships m WHERE m.unit_id=u.id),
		(SELECT count(*) FROM environments e WHERE e.unit_id=u.id),
		COALESCE((SELECT sum(e.cpu_milli) FROM environments e WHERE e.unit_id=u.id AND e.desired_state='running'),0),
		COALESCE((SELECT sum(e.mem_mi) FROM environments e WHERE e.unit_id=u.id AND e.desired_state='running'),0),
		COALESCE((SELECT sum(e.storage_gi) FROM environments e WHERE e.unit_id=u.id),0),
		COALESCE((SELECT sum(m.storage_gi) FROM memberships m WHERE m.unit_id=u.id),0)
		FROM units u ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.UnitView{}
	for rows.Next() {
		var v models.UnitView
		if err := rows.Scan(&v.ID, &v.Name, &v.Slug, &v.Namespace, &v.Description, &v.CPUMilli, &v.MemMi, &v.StorageGi, &v.Disabled, &v.CreatedAt,
			&v.MemberCount, &v.EnvCount, &v.UsedCPUMilli, &v.UsedMemMi, &v.UsedStorageGi, &v.AllocatedStorageGi); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (d *DB) UpdateUnit(ctx context.Context, id int64, name, desc string, cpu, mem, storage int64, disabled bool) error {
	_, err := d.pool.Exec(ctx, `UPDATE units SET name=$2, description=$3, cpu_milli=$4, mem_mi=$5, storage_gi=$6, disabled=$7 WHERE id=$1`,
		id, name, desc, cpu, mem, storage, disabled)
	return err
}

func (d *DB) DeleteUnit(ctx context.Context, id int64) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM units WHERE id=$1`, id)
	return err
}

func (d *DB) SumUnitLimits(ctx context.Context) (models.PlatformLimits, error) {
	var l models.PlatformLimits
	err := d.pool.QueryRow(ctx, `SELECT COALESCE(sum(cpu_milli),0),COALESCE(sum(mem_mi),0),COALESCE(sum(storage_gi),0) FROM units`).
		Scan(&l.CPUMilli, &l.MemMi, &l.StorageGi)
	return l, err
}

func (d *DB) CountUnits(ctx context.Context) (int, error) {
	var n int
	err := d.pool.QueryRow(ctx, `SELECT count(*) FROM units`).Scan(&n)
	return n, err
}

// ---------- memberships ----------

func (d *DB) ListMembers(ctx context.Context, unitID int64) ([]models.Membership, error) {
	rows, err := d.pool.Query(ctx, `SELECT m.id,m.unit_id,m.user_id,m.role,m.cpu_milli,m.mem_mi,m.storage_gi,m.created_at,
		u.username,u.display_name,u.disabled,
		COALESCE((SELECT sum(e.cpu_milli) FROM environments e WHERE e.unit_id=m.unit_id AND e.user_id=m.user_id AND e.desired_state='running'),0),
		COALESCE((SELECT sum(e.mem_mi) FROM environments e WHERE e.unit_id=m.unit_id AND e.user_id=m.user_id AND e.desired_state='running'),0),
		COALESCE((SELECT sum(e.storage_gi) FROM environments e WHERE e.unit_id=m.unit_id AND e.user_id=m.user_id),0),
		(SELECT count(*) FROM environments e WHERE e.unit_id=m.unit_id AND e.user_id=m.user_id)
		FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.unit_id=$1 ORDER BY m.id`, unitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Membership{}
	for rows.Next() {
		var m models.Membership
		var disabled bool
		if err := rows.Scan(&m.ID, &m.UnitID, &m.UserID, &m.Role, &m.CPUMilli, &m.MemMi, &m.StorageGi, &m.CreatedAt,
			&m.Username, &m.DisplayName, &disabled, &m.UsedCPUMilli, &m.UsedMemMi, &m.UsedStorageGi, &m.EnvCount); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *DB) GetMembership(ctx context.Context, unitID, userID int64) (*models.Membership, error) {
	var m models.Membership
	err := d.pool.QueryRow(ctx, `SELECT id,unit_id,user_id,role,cpu_milli,mem_mi,storage_gi,created_at
		FROM memberships WHERE unit_id=$1 AND user_id=$2`, unitID, userID).
		Scan(&m.ID, &m.UnitID, &m.UserID, &m.Role, &m.CPUMilli, &m.MemMi, &m.StorageGi, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (d *DB) SumMemberStorage(ctx context.Context, unitID, excludeUserID int64) (int64, error) {
	var n int64
	err := d.pool.QueryRow(ctx, `SELECT COALESCE(sum(storage_gi),0) FROM memberships
		WHERE unit_id=$1 AND ($2=0 OR user_id<>$2)`, unitID, excludeUserID).Scan(&n)
	return n, err
}

func (d *DB) UpsertMember(ctx context.Context, unitID, userID int64, role string, cpu, mem, storage int64) (*models.Membership, error) {
	var m models.Membership
	err := d.pool.QueryRow(ctx, `INSERT INTO memberships (unit_id,user_id,role,cpu_milli,mem_mi,storage_gi)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (unit_id,user_id) DO UPDATE SET role=EXCLUDED.role,cpu_milli=EXCLUDED.cpu_milli,mem_mi=EXCLUDED.mem_mi,storage_gi=EXCLUDED.storage_gi
		RETURNING id,unit_id,user_id,role,cpu_milli,mem_mi,storage_gi,created_at`,
		unitID, userID, role, cpu, mem, storage).
		Scan(&m.ID, &m.UnitID, &m.UserID, &m.Role, &m.CPUMilli, &m.MemMi, &m.StorageGi, &m.CreatedAt)
	return &m, err
}

func (d *DB) DeleteMember(ctx context.Context, unitID, userID int64) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM memberships WHERE unit_id=$1 AND user_id=$2`, unitID, userID)
	return err
}

func (d *DB) ListUserMemberships(ctx context.Context, userID int64) ([]models.Membership, error) {
	rows, err := d.pool.Query(ctx, `SELECT m.id,m.unit_id,m.user_id,m.role,m.cpu_milli,m.mem_mi,m.storage_gi,m.created_at,
		un.name,un.slug,
		COALESCE((SELECT sum(e.cpu_milli) FROM environments e WHERE e.unit_id=m.unit_id AND e.user_id=m.user_id AND e.desired_state='running'),0),
		COALESCE((SELECT sum(e.mem_mi) FROM environments e WHERE e.unit_id=m.unit_id AND e.user_id=m.user_id AND e.desired_state='running'),0),
		COALESCE((SELECT sum(e.storage_gi) FROM environments e WHERE e.unit_id=m.unit_id AND e.user_id=m.user_id),0),
		(SELECT count(*) FROM environments e WHERE e.unit_id=m.unit_id AND e.user_id=m.user_id)
		FROM memberships m JOIN units un ON un.id=m.unit_id WHERE m.user_id=$1 ORDER BY m.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Membership{}
	for rows.Next() {
		var m models.Membership
		if err := rows.Scan(&m.ID, &m.UnitID, &m.UserID, &m.Role, &m.CPUMilli, &m.MemMi, &m.StorageGi, &m.CreatedAt,
			&m.UnitName, &m.UnitSlug, &m.UsedCPUMilli, &m.UsedMemMi, &m.UsedStorageGi, &m.EnvCount); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ---------- templates ----------

const tplCols = `id,name,description,image,rootpv,ephemeral,cpu_milli,mem_mi,storage_gi,ssh_port,vnc_port,enabled,created_at`

func scanTemplate(row pgx.Row) (*models.Template, error) {
	var t models.Template
	err := row.Scan(&t.ID, &t.Name, &t.Description, &t.Image, &t.RootPV, &t.Ephemeral, &t.CPUMilli, &t.MemMi, &t.StorageGi, &t.SSHPort, &t.VNCPort, &t.Enabled, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (d *DB) ListTemplates(ctx context.Context, includeDisabled bool) ([]models.Template, error) {
	q := `SELECT ` + tplCols + ` FROM templates`
	if !includeDisabled {
		q += ` WHERE enabled=TRUE`
	}
	q += ` ORDER BY id`
	rows, err := d.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Template{}
	for rows.Next() {
		var t models.Template
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.Image, &t.RootPV, &t.Ephemeral, &t.CPUMilli, &t.MemMi, &t.StorageGi, &t.SSHPort, &t.VNCPort, &t.Enabled, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d *DB) GetTemplate(ctx context.Context, id int64) (*models.Template, error) {
	return scanTemplate(d.pool.QueryRow(ctx, `SELECT `+tplCols+` FROM templates WHERE id=$1`, id))
}

func (d *DB) CreateTemplate(ctx context.Context, t models.Template) (*models.Template, error) {
	return scanTemplate(d.pool.QueryRow(ctx, `INSERT INTO templates (name,description,image,rootpv,ephemeral,cpu_milli,mem_mi,storage_gi,ssh_port,vnc_port,enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING `+tplCols,
		t.Name, t.Description, t.Image, t.RootPV, t.Ephemeral, t.CPUMilli, t.MemMi, t.StorageGi, t.SSHPort, t.VNCPort, t.Enabled))
}

func (d *DB) UpdateTemplate(ctx context.Context, t models.Template) error {
	_, err := d.pool.Exec(ctx, `UPDATE templates SET name=$2,description=$3,image=$4,rootpv=$5,ephemeral=$6,cpu_milli=$7,mem_mi=$8,storage_gi=$9,ssh_port=$10,vnc_port=$11,enabled=$12 WHERE id=$1`,
		t.ID, t.Name, t.Description, t.Image, t.RootPV, t.Ephemeral, t.CPUMilli, t.MemMi, t.StorageGi, t.SSHPort, t.VNCPort, t.Enabled)
	return err
}

func (d *DB) DeleteTemplate(ctx context.Context, id int64) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM templates WHERE id=$1`, id)
	return err
}

// ---------- environments ----------

const envCols = `id,name,unit_id,user_id,template_id,namespace,pod_name,pvc_name,ssh_service,vnc_service,image,
	cpu_milli,mem_mi,storage_gi,rootpv,graphical,shm_enabled,shm_mi,ssh_port,vnc_port,node_port_ssh,node_port_vnc,ssh_user,
	ssh_password_enc,vnc_password_enc,desired_state,status,status_message,created_at,updated_at`

func scanEnv(row pgx.Row) (*models.Environment, error) {
	var e models.Environment
	err := row.Scan(&e.ID, &e.Name, &e.UnitID, &e.UserID, &e.TemplateID, &e.Namespace, &e.PodName, &e.PVCName, &e.SSHService, &e.VNCService, &e.Image,
		&e.CPUMilli, &e.MemMi, &e.StorageGi, &e.RootPV, &e.Graphical, &e.ShmEnabled, &e.ShmMi, &e.SSHPort, &e.VNCPort, &e.NodePortSSH, &e.NodePortVNC, &e.SSHUser,
		&e.SSHPasswordEnc, &e.VNCPasswordEnc, &e.DesiredState, &e.Status, &e.StatusMessage, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (d *DB) CreateEnvironment(ctx context.Context, e *models.Environment) (*models.Environment, error) {
	return scanEnv(d.pool.QueryRow(ctx, `INSERT INTO environments
		(name,unit_id,user_id,template_id,namespace,pod_name,pvc_name,ssh_service,vnc_service,image,
		cpu_milli,mem_mi,storage_gi,rootpv,graphical,shm_enabled,shm_mi,ssh_port,vnc_port,ssh_user,ssh_password_enc,vnc_password_enc,desired_state,status,status_message)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25)
		RETURNING `+envCols,
		e.Name, e.UnitID, e.UserID, e.TemplateID, e.Namespace, e.PodName, e.PVCName, e.SSHService, e.VNCService, e.Image,
		e.CPUMilli, e.MemMi, e.StorageGi, e.RootPV, e.Graphical, e.ShmEnabled, e.ShmMi, e.SSHPort, e.VNCPort, e.SSHUser, e.SSHPasswordEnc, e.VNCPasswordEnc,
		e.DesiredState, e.Status, e.StatusMessage))
}

func (d *DB) GetEnvironment(ctx context.Context, id int64) (*models.Environment, error) {
	return scanEnv(d.pool.QueryRow(ctx, `SELECT `+envCols+` FROM environments WHERE id=$1`, id))
}

// EnvironmentExistsByPodName 判断同一命名空间下是否已存在同名主机（Pod 名称）。
func (d *DB) EnvironmentExistsByPodName(ctx context.Context, namespace, podName string) (bool, error) {
	var exists bool
	err := d.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM environments WHERE namespace=$1 AND pod_name=$2)`,
		namespace, podName).Scan(&exists)
	return exists, err
}

func (d *DB) ListEnvironments(ctx context.Context, unitID, userID int64) ([]models.Environment, error) {
	q := `SELECT ` + envCols + ` FROM environments WHERE 1=1`
	args := []any{}
	if unitID > 0 {
		args = append(args, unitID)
		q += fmt.Sprintf(" AND unit_id=$%d", len(args))
	}
	if userID > 0 {
		args = append(args, userID)
		q += fmt.Sprintf(" AND user_id=$%d", len(args))
	}
	q += ` ORDER BY id DESC`
	rows, err := d.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Environment{}
	for rows.Next() {
		e, err := scanEnv(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

func (d *DB) ListEnvironmentsWithNames(ctx context.Context, unitID, userID int64) ([]models.Environment, error) {
	list, err := d.ListEnvironments(ctx, unitID, userID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		e := &list[i]
		if u, err := d.GetUnit(ctx, e.UnitID); err == nil {
			e.UnitName = u.Name
			e.UnitSlug = u.Slug
		}
		if usr, err := d.GetUserByID(ctx, e.UserID); err == nil {
			e.Username = usr.Username
		}
		if e.TemplateID > 0 {
			if t, err := d.GetTemplate(ctx, e.TemplateID); err == nil {
				e.TemplateName = t.Name
			}
		}
	}
	return list, nil
}

func (d *DB) UpdateEnvStatus(ctx context.Context, id int64, status, message string) error {
	_, err := d.pool.Exec(ctx, `UPDATE environments SET status=$2,status_message=$3,updated_at=now() WHERE id=$1`, id, status, message)
	return err
}

func (d *DB) UpdateEnvNodePorts(ctx context.Context, id int64, sshPort, vncPort int32) error {
	_, err := d.pool.Exec(ctx, `UPDATE environments SET node_port_ssh=$2,node_port_vnc=$3,updated_at=now() WHERE id=$1`, id, sshPort, vncPort)
	return err
}

func (d *DB) UpdateEnvDesired(ctx context.Context, id int64, desired string) error {
	_, err := d.pool.Exec(ctx, `UPDATE environments SET desired_state=$2,updated_at=now() WHERE id=$1`, id, desired)
	return err
}

func (d *DB) UpdateEnvironmentConfig(ctx context.Context, id int64, name string, cpu, mem, storage int64, graphical bool, shmEnabled bool, shmMi int64, vncService string, vncPort int32, sshEnc, vncEnc, message string) error {
	_, err := d.pool.Exec(ctx, `UPDATE environments SET
		name=$2,cpu_milli=$3,mem_mi=$4,storage_gi=$5,graphical=$6,shm_enabled=$7,shm_mi=$8,vnc_service=$9,node_port_vnc=$10,
		ssh_password_enc=$11,vnc_password_enc=$12,status_message=$13,updated_at=now()
		WHERE id=$1`,
		id, name, cpu, mem, storage, graphical, shmEnabled, shmMi, vncService, vncPort, sshEnc, vncEnc, message)
	return err
}

func (d *DB) DeleteEnvironmentRow(ctx context.Context, id int64) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM environments WHERE id=$1`, id)
	return err
}

// ---------- environment custom ports ----------

const envPortCols = `id,env_id,container_port,node_port,service_name,protocol,created_at`

func scanEnvPort(row pgx.Row) (*models.EnvPort, error) {
	var p models.EnvPort
	err := row.Scan(&p.ID, &p.EnvID, &p.ContainerPort, &p.NodePort, &p.ServiceName, &p.Protocol, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (d *DB) ListEnvPorts(ctx context.Context, envID int64) ([]models.EnvPort, error) {
	rows, err := d.pool.Query(ctx, `SELECT `+envPortCols+` FROM env_ports WHERE env_id=$1 ORDER BY container_port, id`, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.EnvPort{}
	for rows.Next() {
		p, err := scanEnvPort(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (d *DB) CountEnvPorts(ctx context.Context, envID int64) (int, error) {
	var n int
	err := d.pool.QueryRow(ctx, `SELECT count(*) FROM env_ports WHERE env_id=$1`, envID).Scan(&n)
	return n, err
}

func (d *DB) GetEnvPort(ctx context.Context, envID, id int64) (*models.EnvPort, error) {
	return scanEnvPort(d.pool.QueryRow(ctx, `SELECT `+envPortCols+` FROM env_ports WHERE env_id=$1 AND id=$2`, envID, id))
}

func (d *DB) GetEnvPortByContainer(ctx context.Context, envID int64, containerPort int32, protocol string) (*models.EnvPort, error) {
	return scanEnvPort(d.pool.QueryRow(ctx, `SELECT `+envPortCols+` FROM env_ports WHERE env_id=$1 AND container_port=$2 AND protocol=$3`, envID, containerPort, protocol))
}

func (d *DB) CreateEnvPort(ctx context.Context, envID int64, containerPort, nodePort int32, serviceName, protocol string) (*models.EnvPort, error) {
	return scanEnvPort(d.pool.QueryRow(ctx, `INSERT INTO env_ports (env_id,container_port,node_port,service_name,protocol)
		VALUES ($1,$2,$3,$4,$5) RETURNING `+envPortCols,
		envID, containerPort, nodePort, serviceName, protocol))
}

func (d *DB) UpdateEnvPortNodePort(ctx context.Context, id int64, nodePort int32) error {
	_, err := d.pool.Exec(ctx, `UPDATE env_ports SET node_port=$2 WHERE id=$1`, id, nodePort)
	return err
}

func (d *DB) DeleteEnvPort(ctx context.Context, envID, id int64) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM env_ports WHERE env_id=$1 AND id=$2`, envID, id)
	return err
}

func (d *DB) ListAllEnvPorts(ctx context.Context) ([]models.EnvPort, error) {
	rows, err := d.pool.Query(ctx, `SELECT `+envPortCols+` FROM env_ports ORDER BY env_id, container_port, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.EnvPort{}
	for rows.Next() {
		p, err := scanEnvPort(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ---------- snapshots ----------

const snapshotCols = `id,env_id,name,description,k8s_name,status,size_bytes,error,created_at`

func scanSnapshot(row pgx.Row) (*models.Snapshot, error) {
	var s models.Snapshot
	err := row.Scan(&s.ID, &s.EnvID, &s.Name, &s.Description, &s.K8sName, &s.Status, &s.SizeBytes, &s.Error, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (d *DB) SnapshotNameExists(ctx context.Context, envID int64, name string) (bool, error) {
	var exists bool
	err := d.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM snapshots WHERE env_id=$1 AND lower(name)=lower($2))`, envID, name).Scan(&exists)
	return exists, err
}

func (d *DB) CountSnapshots(ctx context.Context, envID int64) (int, error) {
	var n int
	err := d.pool.QueryRow(ctx, `SELECT count(*) FROM snapshots WHERE env_id=$1`, envID).Scan(&n)
	return n, err
}

func (d *DB) CreateSnapshot(ctx context.Context, envID int64, name, description, k8sName string) (*models.Snapshot, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked int64
	if err := tx.QueryRow(ctx, `SELECT id FROM environments WHERE id=$1 FOR UPDATE`, envID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM snapshots WHERE env_id=$1`, envID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= SnapshotLimitPerEnv {
		return nil, ErrSnapshotLimit
	}
	snap, err := scanSnapshot(tx.QueryRow(ctx, `INSERT INTO snapshots (env_id,name,description,k8s_name,status)
		VALUES ($1,$2,$3,$4,'creating') RETURNING `+snapshotCols, envID, name, description, k8sName))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return snap, nil
}

func (d *DB) ListSnapshots(ctx context.Context, envID int64) ([]models.Snapshot, error) {
	rows, err := d.pool.Query(ctx, `SELECT `+snapshotCols+` FROM snapshots WHERE env_id=$1 ORDER BY id DESC`, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.Snapshot{}
	for rows.Next() {
		s, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (d *DB) GetSnapshot(ctx context.Context, envID, id int64) (*models.Snapshot, error) {
	return scanSnapshot(d.pool.QueryRow(ctx, `SELECT `+snapshotCols+` FROM snapshots WHERE env_id=$1 AND id=$2`, envID, id))
}

func (d *DB) UpdateSnapshotStatus(ctx context.Context, id int64, status string, sizeBytes int64, errMsg string) error {
	_, err := d.pool.Exec(ctx, `UPDATE snapshots SET status=$2,size_bytes=$3,error=$4 WHERE id=$1`, id, status, sizeBytes, errMsg)
	return err
}

func (d *DB) DeleteSnapshotRow(ctx context.Context, envID, id int64) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM snapshots WHERE env_id=$1 AND id=$2`, envID, id)
	return err
}

// CPU/memory usage counts only environments whose pods are meant to be running,
// while storage usage counts every environment, including stopped ones.
func (d *DB) UsageByUnit(ctx context.Context, unitID int64) (models.Usage, error) {
	var u models.Usage
	err := d.pool.QueryRow(ctx, `SELECT
		COALESCE(sum(cpu_milli) FILTER (WHERE desired_state='running'),0),
		COALESCE(sum(mem_mi) FILTER (WHERE desired_state='running'),0),
		COALESCE(sum(storage_gi),0),count(*)
		FROM environments WHERE unit_id=$1`, unitID).Scan(&u.CPUMilli, &u.MemMi, &u.StorageGi, &u.EnvCount)
	return u, err
}

func (d *DB) UsageByUser(ctx context.Context, unitID, userID int64) (models.Usage, error) {
	var u models.Usage
	err := d.pool.QueryRow(ctx, `SELECT
		COALESCE(sum(cpu_milli) FILTER (WHERE desired_state='running'),0),
		COALESCE(sum(mem_mi) FILTER (WHERE desired_state='running'),0),
		COALESCE(sum(storage_gi),0),count(*)
		FROM environments WHERE unit_id=$1 AND user_id=$2`, unitID, userID).Scan(&u.CPUMilli, &u.MemMi, &u.StorageGi, &u.EnvCount)
	return u, err
}

func (d *DB) CountEnvironments(ctx context.Context) (int, int, error) {
	var total, running int
	err := d.pool.QueryRow(ctx, `SELECT count(*), COALESCE(sum(CASE WHEN status='running' THEN 1 ELSE 0 END),0) FROM environments`).Scan(&total, &running)
	return total, running, err
}

// ---------- settings ----------

func (d *DB) GetPlatformLimits(ctx context.Context) (models.PlatformLimits, error) {
	var l models.PlatformLimits
	err := d.pool.QueryRow(ctx, `SELECT cpu_milli,mem_mi,storage_gi FROM settings WHERE id=1`).Scan(&l.CPUMilli, &l.MemMi, &l.StorageGi)
	return l, err
}

func (d *DB) SetPlatformLimits(ctx context.Context, l models.PlatformLimits) error {
	_, err := d.pool.Exec(ctx, `UPDATE settings SET cpu_milli=$1,mem_mi=$2,storage_gi=$3 WHERE id=1`, l.CPUMilli, l.MemMi, l.StorageGi)
	return err
}

// ---------- audit ----------

func (d *DB) Audit(ctx context.Context, userID int64, username, action, target, detail string) {
	_, _ = d.pool.Exec(ctx, `INSERT INTO audit_logs (user_id,username,action,target,detail) VALUES ($1,$2,$3,$4,$5)`,
		userID, username, action, target, detail)
}

type AuditLogFilter struct {
	Username string
	Action   string
	Keyword  string
	From     *time.Time
	To       *time.Time
	Page     int
	PageSize int
}

func (d *DB) ListAuditLogs(ctx context.Context, f AuditLogFilter) ([]models.AuditLog, int64, error) {
	args := []any{}
	expand := func(expr string, values ...any) string {
		for _, v := range values {
			args = append(args, v)
			expr = strings.Replace(expr, "?", "$"+strconv.Itoa(len(args)), 1)
		}
		return expr
	}
	where := []string{"1=1"}
	if f.Username != "" {
		where = append(where, expand("username ILIKE ?", "%"+f.Username+"%"))
	}
	if f.Action != "" {
		where = append(where, expand("action = ?", f.Action))
	}
	if f.Keyword != "" {
		kw := "%" + f.Keyword + "%"
		where = append(where, expand("(username ILIKE ? OR action ILIKE ? OR target ILIKE ? OR detail ILIKE ?)", kw, kw, kw, kw))
	}
	if f.From != nil {
		where = append(where, expand("created_at >= ?", *f.From))
	}
	if f.To != nil {
		where = append(where, expand("created_at <= ?", *f.To))
	}
	clause := strings.Join(where, " AND ")

	var total int64
	if err := d.pool.QueryRow(ctx, "SELECT count(*) FROM audit_logs WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = 20
	}
	if f.PageSize > 500 {
		f.PageSize = 500
	}
	queryArgs := append(append([]any{}, args...), f.PageSize, (f.Page-1)*f.PageSize)
	limitIdx, offsetIdx := len(queryArgs)-1, len(queryArgs)
	rows, err := d.pool.Query(ctx,
		"SELECT id,user_id,username,action,target,detail,created_at FROM audit_logs WHERE "+clause+
			" ORDER BY created_at DESC, id DESC LIMIT $"+strconv.Itoa(limitIdx)+" OFFSET $"+strconv.Itoa(offsetIdx),
		queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []models.AuditLog{}
	for rows.Next() {
		var l models.AuditLog
		if err := rows.Scan(&l.ID, &l.UserID, &l.Username, &l.Action, &l.Target, &l.Detail, &l.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}

func (d *DB) CountAuditLogsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var n int64
	err := d.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE created_at < $1`, cutoff).Scan(&n)
	return n, err
}

func (d *DB) DeleteAuditLogsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := d.pool.Exec(ctx, `DELETE FROM audit_logs WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (d *DB) Now() time.Time { return time.Now() }
