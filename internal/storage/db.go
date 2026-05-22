package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type DB struct {
	sql *sql.DB
}

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("rfc2217_port already exists")

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureSchema(ctx, db); err != nil {
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

func (db *DB) GetAgent(id string) (Agent, error) {
	var agent Agent
	var status string
	var updated string
	err := db.sql.QueryRow(`SELECT id, name, status, hostname, os, arch, machine_id, updated_at FROM agents WHERE id = ?`, id).
		Scan(&agent.ID, &agent.Name, &status, &agent.Hostname, &agent.OS, &agent.Arch, &agent.MachineID, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	agent.Status = AgentStatus(status)
	agent.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return Agent{}, err
	}
	return agent, nil
}

func (db *DB) UpdateAgentStatus(id string, status AgentStatus, updatedAt time.Time) (Agent, error) {
	result, err := db.sql.Exec(`UPDATE agents SET status = ?, updated_at = ? WHERE id = ?`, string(status), updatedAt.Format(time.RFC3339Nano), id)
	if err != nil {
		return Agent{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Agent{}, err
	}
	if affected == 0 {
		return Agent{}, ErrNotFound
	}
	return db.GetAgent(id)
}

func (db *DB) ListAgents() ([]Agent, error) {
	rows, err := db.sql.Query(`SELECT id, name, status, hostname, os, arch, machine_id, updated_at FROM agents ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Agent, 0)
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
	return upsertChannel(db.sql, channel)
}

type sqlExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func upsertChannel(exec sqlExecer, channel Channel) error {
	_, err := exec.Exec(`
INSERT INTO channels (
  id, agent_id, auto_name, alias, role, dev_name, id_path, id_path_tag,
  sysfs_devpath, rfc2217_port, status, error_message, default_baud,
  default_data_bits, default_parity, default_stop_bits, default_flow, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  agent_id=excluded.agent_id,
  auto_name=excluded.auto_name,
  alias=excluded.alias,
  role=excluded.role,
  dev_name=excluded.dev_name,
  id_path=excluded.id_path,
  id_path_tag=excluded.id_path_tag,
  sysfs_devpath=excluded.sysfs_devpath,
  rfc2217_port=excluded.rfc2217_port,
  status=excluded.status,
  error_message=excluded.error_message,
  default_baud=excluded.default_baud,
  default_data_bits=excluded.default_data_bits,
  default_parity=excluded.default_parity,
  default_stop_bits=excluded.default_stop_bits,
  default_flow=excluded.default_flow,
  updated_at=excluded.updated_at
`, channel.ID, channel.AgentID, channel.AutoName, channel.Alias, channel.Role,
		channel.DevName, channel.IDPath, channel.IDPathTag, channel.SysfsDevpath,
		channel.RFC2217Port, string(channel.Status), channel.ErrorMessage,
		channel.DefaultBaud, channel.DefaultDataBits, channel.DefaultParity,
		channel.DefaultStopBits, channel.DefaultFlow, channel.UpdatedAt.Format(time.RFC3339Nano))
	return mapChannelWriteError(err)
}

func mapChannelWriteError(err error) error {
	if isRFC2217PortConflict(err) {
		return ErrConflict
	}
	return err
}

func isRFC2217PortConflict(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) &&
		sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE &&
		strings.Contains(err.Error(), "channels.rfc2217_port")
}

func (db *DB) GetChannel(id string) (Channel, error) {
	row := db.sql.QueryRow(`
SELECT id, agent_id, auto_name, alias, role, dev_name, id_path, id_path_tag,
  sysfs_devpath, rfc2217_port, status, error_message, default_baud,
  default_data_bits, default_parity, default_stop_bits, default_flow, updated_at
FROM channels
WHERE id = ?
`, id)
	channel, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	if err != nil {
		return Channel{}, err
	}
	return channel, nil
}

func (db *DB) ListChannels() ([]Channel, error) {
	rows, err := db.sql.Query(`
SELECT id, agent_id, auto_name, alias, role, dev_name, id_path, id_path_tag,
  sysfs_devpath, rfc2217_port, status, error_message, default_baud,
  default_data_bits, default_parity, default_stop_bits, default_flow, updated_at
FROM channels
ORDER BY alias
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Channel, 0)
	for rows.Next() {
		channel, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, channel)
	}
	return out, rows.Err()
}

func (db *DB) UpdateChannelStatus(id string, status ChannelStatus, devName, errorMessage string, updatedAt time.Time) error {
	result, err := db.sql.Exec(`
UPDATE channels
SET status = ?, dev_name = ?, error_message = ?, updated_at = ?
WHERE id = ?
`, string(status), devName, errorMessage, updatedAt.Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) UpdateChannelStatusForAgent(id, agentID string, status ChannelStatus, devName, errorMessage string, updatedAt time.Time) error {
	result, err := db.sql.Exec(`
UPDATE channels
SET status = ?, dev_name = ?, error_message = ?, updated_at = ?
WHERE id = ? AND agent_id = ?
`, string(status), devName, errorMessage, updatedAt.Format(time.RFC3339Nano), id, agentID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) DeleteChannel(id string) error {
	result, err := db.sql.Exec(`DELETE FROM channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) DeleteChannelWithLogSegments(id string) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.Exec(`DELETE FROM log_segments WHERE channel_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanChannel(row scanner) (Channel, error) {
	var channel Channel
	var status string
	var updated string
	if err := row.Scan(&channel.ID, &channel.AgentID, &channel.AutoName, &channel.Alias,
		&channel.Role, &channel.DevName, &channel.IDPath, &channel.IDPathTag,
		&channel.SysfsDevpath, &channel.RFC2217Port, &status, &channel.ErrorMessage,
		&channel.DefaultBaud, &channel.DefaultDataBits, &channel.DefaultParity,
		&channel.DefaultStopBits, &channel.DefaultFlow, &updated); err != nil {
		return Channel{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return Channel{}, err
	}
	channel.Status = ChannelStatus(status)
	channel.UpdatedAt = parsed
	return channel, nil
}

func (db *DB) UpsertCandidate(candidate Candidate) error {
	_, err := db.sql.Exec(`
INSERT INTO candidates (
  id, agent_id, dev_name, id_path, id_path_tag, sysfs_devpath, interface,
  vid, pid, serial, driver, manufacturer, product, first_seen, last_seen
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  agent_id=excluded.agent_id,
  dev_name=excluded.dev_name,
  id_path=excluded.id_path,
  id_path_tag=excluded.id_path_tag,
  sysfs_devpath=excluded.sysfs_devpath,
  interface=excluded.interface,
  vid=excluded.vid,
  pid=excluded.pid,
  serial=excluded.serial,
  driver=excluded.driver,
  manufacturer=excluded.manufacturer,
  product=excluded.product,
  last_seen=excluded.last_seen
`, candidate.ID, candidate.AgentID, candidate.DevName, candidate.IDPath, candidate.IDPathTag,
		candidate.SysfsDevpath, candidate.Interface, candidate.VID, candidate.PID,
		candidate.Serial, candidate.Driver, candidate.Manufacturer, candidate.Product,
		candidate.FirstSeen.Format(time.RFC3339Nano), candidate.LastSeen.Format(time.RFC3339Nano))
	return err
}

func (db *DB) ListCandidates() ([]Candidate, error) {
	rows, err := db.sql.Query(`
SELECT id, agent_id, dev_name, id_path, id_path_tag, sysfs_devpath, interface,
  vid, pid, serial, driver, manufacturer, product, first_seen, last_seen
FROM candidates
ORDER BY last_seen DESC, id
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Candidate, 0)
	for rows.Next() {
		candidate, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, rows.Err()
}

func (db *DB) GetCandidate(id string) (Candidate, error) {
	row := db.sql.QueryRow(`
SELECT id, agent_id, dev_name, id_path, id_path_tag, sysfs_devpath, interface,
  vid, pid, serial, driver, manufacturer, product, first_seen, last_seen
FROM candidates
WHERE id = ?
`, id)
	candidate, err := scanCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Candidate{}, ErrNotFound
	}
	if err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}

func (db *DB) DeleteCandidate(id string) error {
	result, err := db.sql.Exec(`DELETE FROM candidates WHERE id = ?`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) ConfirmCandidate(candidateID string, buildChannel func(Candidate) Channel) (Channel, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return Channel{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	row := tx.QueryRow(`
SELECT id, agent_id, dev_name, id_path, id_path_tag, sysfs_devpath, interface,
  vid, pid, serial, driver, manufacturer, product, first_seen, last_seen
FROM candidates
WHERE id = ?
`, candidateID)
	candidate, err := scanCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	if err != nil {
		return Channel{}, err
	}

	channel := buildChannel(candidate)
	if err := upsertChannel(tx, channel); err != nil {
		return Channel{}, err
	}

	result, err := tx.Exec(`DELETE FROM candidates WHERE id = ?`, candidateID)
	if err != nil {
		return Channel{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Channel{}, err
	}
	if affected == 0 {
		return Channel{}, ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return Channel{}, err
	}
	committed = true
	return channel, nil
}

func (db *DB) DeleteCandidatesByAgent(agentID string) error {
	_, err := db.sql.Exec(`DELETE FROM candidates WHERE agent_id = ?`, agentID)
	return err
}

func scanCandidate(row scanner) (Candidate, error) {
	var candidate Candidate
	var firstSeen string
	var lastSeen string
	if err := row.Scan(&candidate.ID, &candidate.AgentID, &candidate.DevName,
		&candidate.IDPath, &candidate.IDPathTag, &candidate.SysfsDevpath,
		&candidate.Interface, &candidate.VID, &candidate.PID, &candidate.Serial,
		&candidate.Driver, &candidate.Manufacturer, &candidate.Product,
		&firstSeen, &lastSeen); err != nil {
		return Candidate{}, err
	}
	first, err := time.Parse(time.RFC3339Nano, firstSeen)
	if err != nil {
		return Candidate{}, err
	}
	last, err := time.Parse(time.RFC3339Nano, lastSeen)
	if err != nil {
		return Candidate{}, err
	}
	candidate.FirstSeen = first
	candidate.LastSeen = last
	return candidate, nil
}

func (db *DB) InsertLogSegment(segment LogSegment) error {
	_, err := db.sql.Exec(`
INSERT INTO log_segments (
  channel_id, path, start_time, end_time, size_bytes, frame_count, status
) VALUES (?, ?, ?, ?, ?, ?, ?)
`, segment.ChannelID, segment.Path, segment.StartTime.Format(time.RFC3339Nano),
		segment.EndTime.Format(time.RFC3339Nano), segment.SizeBytes, segment.FrameCount,
		string(segment.Status))
	return err
}

func (db *DB) UpsertLogSegment(segment LogSegment) error {
	_, err := db.sql.Exec(`
INSERT INTO log_segments (
  channel_id, path, start_time, end_time, size_bytes, frame_count, status
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
  channel_id = excluded.channel_id,
  start_time = excluded.start_time,
  end_time = excluded.end_time,
  size_bytes = excluded.size_bytes,
  frame_count = excluded.frame_count,
  status = excluded.status
`, segment.ChannelID, segment.Path, segment.StartTime.Format(time.RFC3339Nano),
		segment.EndTime.Format(time.RFC3339Nano), segment.SizeBytes, segment.FrameCount,
		string(segment.Status))
	return err
}

func (db *DB) UpsertLogSegmentIfChannelExists(segment LogSegment) (bool, error) {
	result, err := db.sql.Exec(`
INSERT INTO log_segments (
  channel_id, path, start_time, end_time, size_bytes, frame_count, status
)
SELECT ?, ?, ?, ?, ?, ?, ?
WHERE EXISTS (SELECT 1 FROM channels WHERE id = ?)
ON CONFLICT(path) DO UPDATE SET
  channel_id = excluded.channel_id,
  start_time = excluded.start_time,
  end_time = excluded.end_time,
  size_bytes = excluded.size_bytes,
  frame_count = excluded.frame_count,
  status = excluded.status
WHERE EXISTS (SELECT 1 FROM channels WHERE id = excluded.channel_id)
`, segment.ChannelID, segment.Path, segment.StartTime.Format(time.RFC3339Nano),
		segment.EndTime.Format(time.RFC3339Nano), segment.SizeBytes, segment.FrameCount,
		string(segment.Status), segment.ChannelID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

func (db *DB) ListLogSegments(channelID string, start, end time.Time) ([]LogSegment, error) {
	rows, err := db.sql.Query(`
SELECT id, channel_id, path, start_time, end_time, size_bytes, frame_count, status
FROM log_segments
WHERE channel_id = ?
  AND start_time <= ?
  AND end_time >= ?
ORDER BY start_time
`, channelID, end.Format(time.RFC3339Nano), start.Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanLogSegments(rows)
}

func (db *DB) ListLogSegmentsForChannel(channelID string) ([]LogSegment, error) {
	rows, err := db.sql.Query(`
SELECT id, channel_id, path, start_time, end_time, size_bytes, frame_count, status
FROM log_segments
WHERE channel_id = ?
ORDER BY start_time
`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanLogSegments(rows)
}

func scanLogSegments(rows *sql.Rows) ([]LogSegment, error) {
	out := make([]LogSegment, 0)
	for rows.Next() {
		var segment LogSegment
		var status string
		var startText string
		var endText string
		if err := rows.Scan(&segment.ID, &segment.ChannelID, &segment.Path, &startText, &endText,
			&segment.SizeBytes, &segment.FrameCount, &status); err != nil {
			return nil, err
		}
		var err error
		segment.StartTime, err = time.Parse(time.RFC3339Nano, startText)
		if err != nil {
			return nil, err
		}
		segment.EndTime, err = time.Parse(time.RFC3339Nano, endText)
		if err != nil {
			return nil, err
		}
		segment.Status = LogSegmentStatus(status)
		out = append(out, segment)
	}
	return out, rows.Err()
}
