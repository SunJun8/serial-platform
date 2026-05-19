import { useEffect, useMemo, useState } from 'react';
import {
  Activity,
  Cable,
  Download,
  Gauge,
  HardDrive,
  ListFilter,
  Monitor,
  PlugZap,
  RefreshCw,
  Router,
  Search,
  Settings2,
  TerminalSquare
} from 'lucide-react';
import { getJSON } from './api';

type Agent = {
  ID: string;
  Name: string;
  Status: 'pending' | 'active' | 'offline' | string;
  Hostname: string;
  OS: string;
  Arch: string;
  MachineID: string;
  UpdatedAt: string;
};

type Channel = {
  ID: string;
  AgentID: string;
  AutoName: string;
  Alias: string;
  Role: string;
  IDPath: string;
  IDPathTag: string;
  SysfsDevpath: string;
  RFC2217Port: number;
  Status: 'online' | 'offline' | 'busy' | 'disabled' | string;
  DefaultBaud: number;
  DefaultDataBits: number;
  DefaultParity: string;
  DefaultStopBits: number;
  UpdatedAt: string;
};

type ViewKey = 'hosts' | 'channels' | 'calibration' | 'terminal' | 'logs';

type NavItem = {
  key: ViewKey;
  label: string;
  icon: typeof Monitor;
};

const navItems: NavItem[] = [
  { key: 'hosts', label: 'Hosts', icon: Monitor },
  { key: 'channels', label: 'Channels', icon: Cable },
  { key: 'calibration', label: 'Calibration', icon: Gauge },
  { key: 'terminal', label: 'Live Log / Terminal', icon: TerminalSquare },
  { key: 'logs', label: 'Logs', icon: HardDrive }
];

const sampleLogLines = [
  { ts: '+0.000', dir: 'RX', text: 'Boot ROM: BL616 serial console attached' },
  { ts: '+0.184', dir: 'TX', text: 'help\\r\\n' },
  { ts: '+0.196', dir: 'RX', text: 'nsh> help' },
  { ts: '+0.420', dir: 'RX', text: 'Builtin apps: serial_test miot_diag reboot' }
];

export function App() {
  const [activeView, setActiveView] = useState<ViewKey>('hosts');
  const [agents, setAgents] = useState<Agent[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState('');

  async function refresh() {
    setLoading(true);
    setError(null);
    try {
      const [nextAgents, nextChannels] = await Promise.all([
        getJSON<Agent[]>('/api/agents'),
        getJSON<Channel[]>('/api/channels')
      ]);
      setAgents(nextAgents);
      setChannels(nextChannels);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Request failed');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void refresh();
  }, []);

  const visibleChannels = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) {
      return channels;
    }
    return channels.filter((channel) =>
      [
        channel.Alias,
        channel.AutoName,
        channel.Role,
        channel.Status,
        channel.IDPath,
        String(channel.RFC2217Port)
      ].some((value) => value.toLowerCase().includes(needle))
    );
  }, [channels, query]);

  const onlineChannels = channels.filter((channel) => channel.Status === 'online').length;
  const pendingAgents = agents.filter((agent) => agent.Status === 'pending').length;
  const busyChannels = channels.filter((channel) => channel.Status === 'busy').length;
  const apiStatus = error ? 'API unavailable' : loading ? 'Loading API' : 'API connected';
  const apiStatusClass = error ? 'status-dot error' : loading ? 'status-dot' : 'status-dot online';

  return (
    <div className="shell">
      <aside className="sidebar" aria-label="Primary navigation">
        <div className="brand">
          <Router size={20} aria-hidden="true" />
          <div>
            <strong>Serial Platform</strong>
            <span>central-server</span>
          </div>
        </div>
        <nav className="nav-list">
          {navItems.map((item) => {
            const Icon = item.icon;
            return (
              <button
                key={item.key}
                type="button"
                className={item.key === activeView ? 'nav-item active' : 'nav-item'}
                onClick={() => setActiveView(item.key)}
              >
                <Icon size={17} aria-hidden="true" />
                <span>{item.label}</span>
              </button>
            );
          })}
        </nav>
        <div className="sidebar-footer">
          <span className={apiStatusClass} />
          {apiStatus}
        </div>
      </aside>

      <main className="workspace">
        <header className="topbar">
          <div className="metrics" aria-label="Platform summary">
            <Metric label="Hosts" value={agents.length} tone="neutral" />
            <Metric label="Pending" value={pendingAgents} tone={pendingAgents > 0 ? 'warn' : 'neutral'} />
            <Metric label="Online channels" value={onlineChannels} tone="good" />
            <Metric label="Busy" value={busyChannels} tone={busyChannels > 0 ? 'warn' : 'neutral'} />
          </div>
          <div className="toolbar">
            <label className="search-box">
              <Search size={15} aria-hidden="true" />
              <input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Filter channels"
              />
            </label>
            <button type="button" className="icon-button" onClick={() => void refresh()} title="Refresh">
              <RefreshCw size={16} aria-hidden="true" />
            </button>
          </div>
        </header>

        {error ? <div className="error-strip">API error: {error}</div> : null}

        {activeView === 'hosts' ? <HostsView agents={agents} channels={channels} loading={loading} /> : null}
        {activeView === 'channels' ? (
          <ChannelsView channels={visibleChannels} loading={loading} query={query} />
        ) : null}
        {activeView === 'calibration' ? <CalibrationView agents={agents} /> : null}
        {activeView === 'terminal' ? <TerminalView channels={visibleChannels} /> : null}
        {activeView === 'logs' ? <LogsView channels={visibleChannels} /> : null}
      </main>
    </div>
  );
}

function Metric({ label, value, tone }: { label: string; value: number; tone: 'neutral' | 'good' | 'warn' }) {
  return (
    <div className={`metric ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function HostsView({
  agents,
  channels,
  loading
}: {
  agents: Agent[];
  channels: Channel[];
  loading: boolean;
}) {
  return (
    <section className="view">
      <ViewTitle icon={Monitor} title="Hosts" action="Approve / Rename" />
      <div className="split-layout">
        <div className="panel">
          <div className="panel-head">
            <h2>Agent inventory</h2>
            <span>{loading ? 'Loading' : `${agents.length} hosts`}</span>
          </div>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Status</th>
                  <th>Host</th>
                  <th>OS / Arch</th>
                  <th>Updated</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {agents.length === 0 ? (
                  <EmptyRow colSpan={6} label="No host agents registered" />
                ) : (
                  agents.map((agent) => (
                    <tr key={agent.ID}>
                      <td>
                        <strong>{agent.Name || agent.ID}</strong>
                        <small>{agent.MachineID}</small>
                      </td>
                      <td>
                        <Badge value={agent.Status} />
                      </td>
                      <td>{agent.Hostname}</td>
                      <td>{agent.OS} / {agent.Arch}</td>
                      <td>{formatTime(agent.UpdatedAt)}</td>
                      <td className="row-actions">
                        <button type="button">Review</button>
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </div>
        <div className="panel">
          <div className="panel-head">
            <h2>USB topology</h2>
            <span>{channels.length} tty devices</span>
          </div>
          <div className="topology-list">
            {channels.length === 0 ? (
              <div className="empty-state">No tty devices reported yet</div>
            ) : (
              channels.slice(0, 8).map((channel) => (
                <div className="topology-row" key={channel.ID}>
                  <PlugZap size={16} aria-hidden="true" />
                  <div>
                    <strong>{channel.Alias || channel.AutoName}</strong>
                    <span>{channel.IDPathTag || channel.IDPath}</span>
                  </div>
                  <Badge value={channel.Status} />
                </div>
              ))
            )}
          </div>
        </div>
      </div>
    </section>
  );
}

function ChannelsView({
  channels,
  loading,
  query
}: {
  channels: Channel[];
  loading: boolean;
  query: string;
}) {
  return (
    <section className="view">
      <ViewTitle icon={Cable} title="Channels" action="Configure defaults" />
      <div className="panel">
        <div className="panel-head">
          <h2>Serial channels</h2>
          <span>{loading ? 'Loading' : `${channels.length} shown${query ? ' after filter' : ''}`}</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Alias</th>
                <th>Status</th>
                <th>Role</th>
                <th>RFC2217</th>
                <th>Defaults</th>
                <th>ID path</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {channels.length === 0 ? (
                <EmptyRow colSpan={7} label="No channels match the current filter" />
              ) : (
                channels.map((channel) => (
                  <tr key={channel.ID}>
                    <td>
                      <strong>{channel.Alias || channel.AutoName}</strong>
                      <small>{channel.AutoName}</small>
                    </td>
                    <td>
                      <Badge value={channel.Status} />
                    </td>
                    <td>{channel.Role || 'console'}</td>
                    <td>:{channel.RFC2217Port || '-'}</td>
                    <td>
                      {channel.DefaultBaud || 115200} {channel.DefaultDataBits || 8}
                      {channel.DefaultParity || 'N'}{channel.DefaultStopBits || 1}
                    </td>
                    <td className="mono-cell">{channel.IDPathTag || channel.IDPath || '-'}</td>
                    <td className="row-actions">
                      <button type="button">Edit</button>
                      <button type="button">Disable</button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  );
}

function CalibrationView({ agents }: { agents: Agent[] }) {
  return (
    <section className="view">
      <ViewTitle icon={Gauge} title="Calibration" action="Confirm mapping" />
      <div className="workflow">
        <div className="panel narrow">
          <div className="panel-head">
            <h2>Port wizard</h2>
            <span>{agents.length} hosts</span>
          </div>
          <label className="field">
            <span>Host</span>
            <select>
              {agents.length === 0 ? <option>No hosts</option> : null}
              {agents.map((agent) => (
                <option key={agent.ID}>{agent.Name || agent.Hostname}</option>
              ))}
            </select>
          </label>
          <div className="step-list">
            <Step index={1} label="Select host" state={agents.length > 0 ? 'ready' : 'blocked'} />
            <Step index={2} label="Insert USB serial adapter" state="ready" />
            <Step index={3} label="Capture candidate tty" state="waiting" />
            <Step index={4} label="Confirm logical name" state="waiting" />
          </div>
        </div>
        <div className="panel">
          <div className="panel-head">
            <h2>Candidate details</h2>
            <span>Waiting for scan</span>
          </div>
          <dl className="detail-grid">
            <div><dt>ID_PATH</dt><dd>pending host scan</dd></div>
            <div><dt>Interface</dt><dd>if00</dd></div>
            <div><dt>VID/PID</dt><dd>----</dd></div>
            <div><dt>Driver</dt><dd>unknown</dd></div>
            <div><dt>Generated name</dt><dd>host.hub.port.if00</dd></div>
          </dl>
        </div>
      </div>
    </section>
  );
}

function TerminalView({ channels }: { channels: Channel[] }) {
  const current = channels[0];
  return (
    <section className="view terminal-view">
      <ViewTitle icon={TerminalSquare} title="Live Log / Terminal" action="Open control session" />
      <div className="terminal-layout">
        <div className="panel terminal-panel">
          <div className="panel-head">
            <h2>{current ? current.Alias || current.AutoName : 'No channel selected'}</h2>
            <span>RX+TX · relative · UTF-8</span>
          </div>
          <div className="terminal-output" aria-label="Live serial log">
            {sampleLogLines.map((line) => (
              <div className="log-line" key={`${line.ts}-${line.text}`}>
                <span>{line.ts}</span>
                <b className={line.dir === 'RX' ? 'rx' : 'tx'}>{line.dir}</b>
                <code>{line.text}</code>
              </div>
            ))}
          </div>
          <div className="terminal-input">
            <span>&gt;</span>
            <input placeholder="Type command" />
            <button type="button">Send</button>
          </div>
        </div>
        <div className="panel narrow controls">
          <div className="panel-head">
            <h2>Control</h2>
            <span>single owner</span>
          </div>
          <label className="field">
            <span>Channel</span>
            <select>
              {channels.length === 0 ? <option>No channels</option> : null}
              {channels.map((channel) => (
                <option key={channel.ID}>{channel.Alias || channel.AutoName}</option>
              ))}
            </select>
          </label>
          <div className="segmented">
            <button type="button" className="active">RX+TX</button>
            <button type="button">RX</button>
            <button type="button">TX</button>
          </div>
          <label className="toggle"><input type="checkbox" defaultChecked /> DTR</label>
          <label className="toggle"><input type="checkbox" defaultChecked /> RTS</label>
          <label className="field">
            <span>Baudrate</span>
            <input defaultValue="115200" />
          </label>
          <button type="button" className="danger">Break</button>
        </div>
      </div>
    </section>
  );
}

function LogsView({ channels }: { channels: Channel[] }) {
  return (
    <section className="view">
      <ViewTitle icon={HardDrive} title="Logs" action="Download range" />
      <div className="workflow">
        <div className="panel narrow">
          <div className="panel-head">
            <h2>Export</h2>
            <Download size={16} aria-hidden="true" />
          </div>
          <label className="field">
            <span>Channel</span>
            <select>
              {channels.length === 0 ? <option>No channels</option> : null}
              {channels.map((channel) => (
                <option key={channel.ID}>{channel.Alias || channel.AutoName}</option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>Direction</span>
            <select defaultValue="both">
              <option value="both">RX+TX</option>
              <option value="rx">RX only</option>
              <option value="tx">TX only</option>
            </select>
          </label>
          <label className="toggle"><input type="checkbox" defaultChecked /> Include timestamps</label>
          <label className="toggle"><input type="checkbox" defaultChecked /> Include direction</label>
          <button type="button">Prepare download</button>
        </div>
        <div className="panel">
          <div className="panel-head">
            <h2>Stored segments</h2>
            <span>raw framed traffic</span>
          </div>
          <div className="quota-grid">
            <Quota label="Global storage" value="0 B" limit="not configured" />
            <Quota label="Channel quota" value="0 B" limit="default" />
            <Quota label="Retention" value="0 days" limit="pending policy" />
          </div>
        </div>
      </div>
    </section>
  );
}

function ViewTitle({
  icon: Icon,
  title,
  action
}: {
  icon: typeof Monitor;
  title: string;
  action: string;
}) {
  return (
    <div className="view-title">
      <div>
        <Icon size={20} aria-hidden="true" />
        <h1>{title}</h1>
      </div>
      <button type="button">
        <Settings2 size={15} aria-hidden="true" />
        {action}
      </button>
    </div>
  );
}

function Badge({ value }: { value: string }) {
  const normalized = value.toLowerCase();
  return <span className={`badge ${normalized}`}>{value || 'unknown'}</span>;
}

function EmptyRow({ colSpan, label }: { colSpan: number; label: string }) {
  return (
    <tr>
      <td colSpan={colSpan} className="empty-row">
        <ListFilter size={15} aria-hidden="true" />
        {label}
      </td>
    </tr>
  );
}

function Step({ index, label, state }: { index: number; label: string; state: 'ready' | 'waiting' | 'blocked' }) {
  return (
    <div className={`step ${state}`}>
      <span>{index}</span>
      <strong>{label}</strong>
    </div>
  );
}

function Quota({ label, value, limit }: { label: string; value: string; limit: string }) {
  return (
    <div className="quota">
      <Activity size={16} aria-hidden="true" />
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{limit}</small>
    </div>
  );
}

function formatTime(value: string) {
  if (!value) {
    return '-';
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}
