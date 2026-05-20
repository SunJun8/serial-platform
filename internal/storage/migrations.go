package storage

import (
	"context"
	"database/sql"
	"strings"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  status TEXT NOT NULL,
  hostname TEXT NOT NULL,
  os TEXT NOT NULL,
  arch TEXT NOT NULL,
  machine_id TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS channels (
  id TEXT PRIMARY KEY,
  agent_id TEXT NOT NULL,
  auto_name TEXT NOT NULL,
  alias TEXT NOT NULL,
  role TEXT NOT NULL,
  dev_name TEXT NOT NULL,
  id_path TEXT NOT NULL,
  id_path_tag TEXT NOT NULL,
  sysfs_devpath TEXT NOT NULL,
  rfc2217_port INTEGER NOT NULL UNIQUE,
  status TEXT NOT NULL,
  error_message TEXT NOT NULL,
  default_baud INTEGER NOT NULL,
  default_data_bits INTEGER NOT NULL,
  default_parity TEXT NOT NULL,
  default_stop_bits INTEGER NOT NULL,
  default_flow TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS candidates (
  id TEXT PRIMARY KEY,
  agent_id TEXT NOT NULL,
  dev_name TEXT NOT NULL,
  id_path TEXT NOT NULL,
  id_path_tag TEXT NOT NULL,
  sysfs_devpath TEXT NOT NULL,
  interface TEXT NOT NULL,
  vid TEXT NOT NULL,
  pid TEXT NOT NULL,
  serial TEXT NOT NULL,
  driver TEXT NOT NULL,
  manufacturer TEXT NOT NULL,
  product TEXT NOT NULL,
  first_seen TEXT NOT NULL,
  last_seen TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS log_segments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id TEXT NOT NULL,
  path TEXT NOT NULL,
  start_time TEXT NOT NULL,
  end_time TEXT NOT NULL,
  size_bytes INTEGER NOT NULL,
  frame_count INTEGER NOT NULL,
  status TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS quota_config (
  scope TEXT PRIMARY KEY,
  global_max_storage_bytes INTEGER NOT NULL,
  default_retention_days INTEGER NOT NULL,
  default_channel_max_storage_bytes INTEGER NOT NULL,
  warning_threshold_percent INTEGER NOT NULL,
  critical_threshold_percent INTEGER NOT NULL,
  cleanup_interval_seconds INTEGER NOT NULL
);
`

func ensureSchema(ctx context.Context, db *sql.DB) error {
	for _, statement := range []string{
		`ALTER TABLE channels ADD COLUMN dev_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE channels ADD COLUMN default_flow TEXT NOT NULL DEFAULT 'none'`,
		`ALTER TABLE channels ADD COLUMN error_message TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil && !isDuplicateColumnError(err) {
			return err
		}
	}
	return nil
}

func isDuplicateColumnError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "duplicate column name")
}
