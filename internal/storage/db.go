package storage

import (
	"context"
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	sql *sql.DB
}

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(context.Background(), schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &DB{sql: db}, nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

func (db *DB) UpsertAgent(agent Agent) error {
	_, err := db.sql.Exec(`
INSERT INTO agents (id, name, status, hostname, os, arch, machine_id, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name,
  status=excluded.status,
  hostname=excluded.hostname,
  os=excluded.os,
  arch=excluded.arch,
  machine_id=excluded.machine_id,
  updated_at=excluded.updated_at
`, agent.ID, agent.Name, string(agent.Status), agent.Hostname, agent.OS, agent.Arch, agent.MachineID, agent.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (db *DB) ListAgents() ([]Agent, error) {
	rows, err := db.sql.Query(`SELECT id, name, status, hostname, os, arch, machine_id, updated_at FROM agents ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Agent
	for rows.Next() {
		var agent Agent
		var status string
		var updated string
		if err := rows.Scan(&agent.ID, &agent.Name, &status, &agent.Hostname, &agent.OS, &agent.Arch, &agent.MachineID, &updated); err != nil {
			return nil, err
		}
		agent.Status = AgentStatus(status)
		agent.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, err
		}
		out = append(out, agent)
	}
	return out, rows.Err()
}

func (db *DB) UpsertChannel(channel Channel) error {
	_, err := db.sql.Exec(`
INSERT INTO channels (
  id, agent_id, auto_name, alias, role, id_path, id_path_tag, sysfs_devpath,
  rfc2217_port, status, default_baud, default_data_bits, default_parity,
  default_stop_bits, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  agent_id=excluded.agent_id,
  auto_name=excluded.auto_name,
  alias=excluded.alias,
  role=excluded.role,
  id_path=excluded.id_path,
  id_path_tag=excluded.id_path_tag,
  sysfs_devpath=excluded.sysfs_devpath,
  rfc2217_port=excluded.rfc2217_port,
  status=excluded.status,
  default_baud=excluded.default_baud,
  default_data_bits=excluded.default_data_bits,
  default_parity=excluded.default_parity,
  default_stop_bits=excluded.default_stop_bits,
  updated_at=excluded.updated_at
`, channel.ID, channel.AgentID, channel.AutoName, channel.Alias, channel.Role,
		channel.IDPath, channel.IDPathTag, channel.SysfsDevpath, channel.RFC2217Port,
		string(channel.Status), channel.DefaultBaud, channel.DefaultDataBits,
		channel.DefaultParity, channel.DefaultStopBits, channel.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (db *DB) ListChannels() ([]Channel, error) {
	rows, err := db.sql.Query(`SELECT id, agent_id, auto_name, alias, role, id_path, id_path_tag, sysfs_devpath, rfc2217_port, status, default_baud, default_data_bits, default_parity, default_stop_bits, updated_at FROM channels ORDER BY alias`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		var channel Channel
		var status string
		var updated string
		if err := rows.Scan(&channel.ID, &channel.AgentID, &channel.AutoName, &channel.Alias, &channel.Role,
			&channel.IDPath, &channel.IDPathTag, &channel.SysfsDevpath, &channel.RFC2217Port,
			&status, &channel.DefaultBaud, &channel.DefaultDataBits, &channel.DefaultParity,
			&channel.DefaultStopBits, &updated); err != nil {
			return nil, err
		}
		channel.Status = ChannelStatus(status)
		channel.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, err
		}
		out = append(out, channel)
	}
	return out, rows.Err()
}
