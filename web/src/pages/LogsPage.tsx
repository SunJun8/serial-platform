import { useEffect, useMemo, useState } from 'react';
import { Download, HardDrive } from 'lucide-react';
import { downloadURL } from '../api';
import { Quota } from '../components/Quota';
import { ViewTitle } from '../components/ViewTitle';
import type { Channel } from '../types';

export function LogsPage({ channels }: { channels: Channel[] }) {
  const [channelID, setChannelID] = useState(channels[0]?.ID ?? '');
  const [direction, setDirection] = useState('both');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [format, setFormat] = useState('text');
  const [timestamp, setTimestamp] = useState(true);
  const [directionLabel, setDirectionLabel] = useState(true);
  const [stripANSI, setStripANSI] = useState(false);

  const channelByID = useMemo(() => new Map(channels.map((channel) => [channel.ID, channel])), [channels]);
  const activeChannelID = channelByID.has(channelID) ? channelID : channels[0]?.ID ?? '';

  useEffect(() => {
    if (activeChannelID !== channelID) {
      setChannelID(activeChannelID);
    }
  }, [activeChannelID, channelID]);

  const href = useMemo(
    () =>
      activeChannelID
        ? downloadURL('/api/logs/download', {
            channel_id: activeChannelID,
            direction,
            from: from ? new Date(from).toISOString() : undefined,
            to: to ? new Date(to).toISOString() : undefined,
            format,
            timestamp,
            direction_label: directionLabel,
            strip_ansi: stripANSI
          })
        : '#',
    [activeChannelID, direction, from, to, format, timestamp, directionLabel, stripANSI]
  );

  return (
    <section className="view">
      <ViewTitle icon={HardDrive} title="Logs" action="Download range" />
      <div className="panel">
        <div className="panel-head">
          <h2>Export log frames</h2>
          <Download size={16} aria-hidden="true" />
        </div>
        <form>
          <div className="log-export-form">
            <label className="field compact-field">
              <span>Channel</span>
              <select value={activeChannelID} onChange={(event) => setChannelID(event.target.value)}>
                {channels.length === 0 ? <option value="">No channels</option> : null}
                {channels.map((channel) => (
                  <option key={channel.ID} value={channel.ID}>
                    {channel.Alias || channel.AutoName}
                  </option>
                ))}
              </select>
            </label>
            <label className="field compact-field">
              <span>Direction</span>
              <select value={direction} onChange={(event) => setDirection(event.target.value)}>
                <option value="both">RX+TX</option>
                <option value="rx">RX only</option>
                <option value="tx">TX only</option>
              </select>
            </label>
            <label className="field compact-field">
              <span>From</span>
              <input type="datetime-local" value={from} onChange={(event) => setFrom(event.target.value)} />
            </label>
            <label className="field compact-field">
              <span>To</span>
              <input type="datetime-local" value={to} onChange={(event) => setTo(event.target.value)} />
            </label>
            <label className="field compact-field">
              <span>Format</span>
              <select value={format} onChange={(event) => setFormat(event.target.value)}>
                <option value="text">Text</option>
                <option value="raw">Raw</option>
              </select>
            </label>
            <label className="toggle">
              <input type="checkbox" checked={timestamp} onChange={(event) => setTimestamp(event.target.checked)} />
              Timestamp
            </label>
            <label className="toggle">
              <input
                type="checkbox"
                checked={directionLabel}
                onChange={(event) => setDirectionLabel(event.target.checked)}
              />
              Direction label
            </label>
            <label className="toggle">
              <input type="checkbox" checked={stripANSI} onChange={(event) => setStripANSI(event.target.checked)} />
              Strip ANSI
            </label>
          </div>
          <div className="form-actions">
            <a className={activeChannelID ? 'button-link' : 'button-link disabled'} download href={href}>
              <Download size={15} aria-hidden="true" />
              Download
            </a>
          </div>
        </form>
      </div>
      <div className="quota-grid flat">
        <Quota label="Global storage" value="0 B" limit="not configured" />
        <Quota label="Channel quota" value="0 B" limit="default" />
        <Quota label="Retention" value="0 days" limit="pending policy" />
      </div>
    </section>
  );
}
