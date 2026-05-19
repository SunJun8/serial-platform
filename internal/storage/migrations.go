package storage

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
  id_path TEXT NOT NULL,
  id_path_tag TEXT NOT NULL,
  sysfs_devpath TEXT NOT NULL,
  rfc2217_port INTEGER NOT NULL UNIQUE,
  status TEXT NOT NULL,
  default_baud INTEGER NOT NULL,
  default_data_bits INTEGER NOT NULL,
  default_parity TEXT NOT NULL,
  default_stop_bits INTEGER NOT NULL,
  updated_at TEXT NOT NULL
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
