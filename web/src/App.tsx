import { type FormEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { LucideIcon } from 'lucide-react';
import {
  Activity,
  Cable,
  Download,
  Gauge,
  HardDrive,
  ListFilter,
  Monitor,
  Plug,
  PlugZap,
  Power,
  RefreshCw,
  Router,
  Search,
  Send,
  Settings2,
  TerminalSquare,
  Unplug
} from 'lucide-react';
import { downloadURL, getJSON, postJSON, wsURL } from './api';
import type {
  Agent,
  Candidate,
  Channel,
  ChannelPayload,
  LiveLogFrame,
  OperationResult,
  TerminalMessage
} from './types';

type ViewKey = 'hosts' | 'channels' | 'calibration' | 'terminal' | 'logs';
type RequestState = { busy: boolean; error: string | null; message: string | null };
type TerminalStatus = 'idle' | 'connecting' | 'connected' | 'error';
type LogLine = {
  id: string;
  ts: string;
  dir: string;
  text: string;
};

type NavItem = {
  key: ViewKey;
  label: string;
  icon: LucideIcon;
};

const navItems: NavItem[] = [
  { key: 'hosts', label: 'Hosts', icon: Monitor },
  { key: 'channels', label: 'Channels', icon: Cable },
  { key: 'calibration', label: 'Calibration', icon: Gauge },
  { key: 'terminal', label: 'Live Log / Terminal', icon: TerminalSquare },
  { key: 'logs', label: 'Logs', icon: HardDrive }
];

const emptyRequest: RequestState = { busy: false, error: null, message: null };
const textDecoder = new TextDecoder();
const textEncoder = new TextEncoder();

function defaultManualForm(channels: Channel[], agents: Agent[]) {
  return {
    agentID: agents[0]?.ID ?? '',
    alias: '',
    idPath: '',
    idPathTag: '',
    port: String(nextRFC2217Port(channels)),
    baud: '115200'
  };
}

function defaultConfirmForm(candidate: Candidate | undefined, channels: Channel[]) {
  return {
    alias: candidate ? candidateAlias(candidate) : '',
    role: 'console',
    port: String(nextRFC2217Port(channels)),
    baud: '115200'
  };
}

export function App() {
  const [activeView, setActiveView] = useState<ViewKey>('hosts');
  const [agents, setAgents] = useState<Agent[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busyAgentID, setBusyAgentID] = useState<string | null>(null);
  const [query, setQuery] = useState('');

  const refresh = useCallback(async () => {
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
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  async function approveAgent(agentID: string) {
    setBusyAgentID(agentID);
    setError(null);
    try {
      const updated = await postJSON<Agent>(`/api/agents/${encodeURIComponent(agentID)}/approve`);
      setAgents((current) => current.map((agent) => (agent.ID === updated.ID ? updated : agent)));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusyAgentID(null);
    }
  }

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
        channel.DevName,
        channel.IDPath,
        channel.IDPathTag,
        String(channel.RFC2217Port)
      ].some((value) => value.toLowerCase().includes(needle))
    );
  }, [channels, query]);

  const channelStats = useMemo(
    () =>
      channels.reduce(
        (acc, channel) => {
          if (channel.Status === 'online') {
            acc.online += 1;
          }
          if (channel.Status === 'busy') {
            acc.busy += 1;
          }
          return acc;
        },
        { online: 0, busy: 0 }
      ),
    [channels]
  );
  const pendingAgents = useMemo(
    () => agents.reduce((count, agent) => count + (agent.Status === 'pending' ? 1 : 0), 0),
    [agents]
  );
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
            <Metric label="Online channels" value={channelStats.online} tone="good" />
            <Metric label="Busy" value={channelStats.busy} tone={channelStats.busy > 0 ? 'warn' : 'neutral'} />
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

        {activeView === 'hosts' ? (
          <HostsView
            agents={agents}
            channels={channels}
            loading={loading}
            busyAgentID={busyAgentID}
            onApproveAgent={(agentID) => void approveAgent(agentID)}
          />
        ) : null}
        {activeView === 'channels' ? (
          <ChannelsView
            agents={agents}
            channels={visibleChannels}
            allChannels={channels}
            loading={loading}
            query={query}
            onRefresh={refresh}
          />
        ) : null}
        {activeView === 'calibration' ? (
          <CalibrationView agents={agents} channels={channels} onRefresh={refresh} />
        ) : null}
        {activeView === 'terminal' ? <TerminalView channels={channels} /> : null}
        {activeView === 'logs' ? <LogsView channels={channels} /> : null}
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
  loading,
  busyAgentID,
  onApproveAgent
}: {
  agents: Agent[];
  channels: Channel[];
  loading: boolean;
  busyAgentID: string | null;
  onApproveAgent: (agentID: string) => void;
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
                      <td>
                        {agent.OS} / {agent.Arch}
                      </td>
                      <td>{formatTime(agent.UpdatedAt)}</td>
                      <td className="row-actions">
                        <button
                          type="button"
                          disabled={agent.Status === 'active' || busyAgentID === agent.ID}
                          onClick={() => onApproveAgent(agent.ID)}
                        >
                          {agent.Status === 'active' ? 'Active' : busyAgentID === agent.ID ? 'Saving' : 'Approve'}
                        </button>
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
                    <span>{channel.IDPathTag || channel.IDPath || channel.DevName}</span>
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
  agents,
  channels,
  allChannels,
  loading,
  query,
  onRefresh
}: {
  agents: Agent[];
  channels: Channel[];
  allChannels: Channel[];
  loading: boolean;
  query: string;
  onRefresh: () => Promise<void>;
}) {
  const [form, setForm] = useState(() => defaultManualForm(allChannels, agents));
  const [manualState, setManualState] = useState<RequestState>(emptyRequest);
  const [statusBusyID, setStatusBusyID] = useState<string | null>(null);

  useEffect(() => {
    setForm((current) => ({
      ...current,
      agentID: current.agentID || agents[0]?.ID || '',
      port: current.port || String(nextRFC2217Port(allChannels))
    }));
  }, [agents, allChannels]);

  async function createManualChannel(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setManualState({ busy: true, error: null, message: null });
    try {
      const payload: ChannelPayload = {
        agent_id: form.agentID,
        alias: form.alias.trim(),
        role: 'console',
        id_path: form.idPath.trim(),
        id_path_tag: form.idPathTag.trim(),
        rfc2217_port: Number(form.port),
        default_baud: Number(form.baud),
        default_data_bits: 8,
        default_parity: 'N',
        default_stop_bits: 1,
        default_flow: 'none'
      };
      const created = await postJSON<Channel, ChannelPayload>('/api/channels', payload);
      await onRefresh();
      setForm({
        ...defaultManualForm([...allChannels, created], agents),
        agentID: form.agentID,
        baud: form.baud
      });
      setManualState({ busy: false, error: null, message: 'Manual channel added' });
    } catch (err) {
      setManualState({ busy: false, error: errorMessage(err), message: null });
    }
  }

  async function setChannelEnabled(channel: Channel, enabled: boolean) {
    setStatusBusyID(channel.ID);
    setManualState((current) => ({ ...current, error: null, message: null }));
    try {
      await postJSON<Channel>(`/api/channels/${encodeURIComponent(channel.ID)}/${enabled ? 'enable' : 'disable'}`);
      await onRefresh();
    } catch (err) {
      setManualState((current) => ({ ...current, error: errorMessage(err) }));
    } finally {
      setStatusBusyID(null);
    }
  }

  return (
    <section className="view">
      <ViewTitle icon={Cable} title="Channels" action="Manual fallback" />
      <div className="channels-layout">
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
                  <th>Device</th>
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
                        {channel.DefaultParity || 'N'}
                        {channel.DefaultStopBits || 1} {channel.DefaultFlow || 'none'}
                      </td>
                      <td className="mono-cell">{channel.IDPathTag || channel.IDPath || channel.DevName || '-'}</td>
                      <td className="row-actions">
                        <button
                          type="button"
                          disabled={statusBusyID === channel.ID}
                          onClick={() => void setChannelEnabled(channel, channel.Status === 'disabled')}
                        >
                          {channel.Status === 'disabled' ? 'Enable' : 'Disable'}
                        </button>
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </div>
        <div className="panel narrow">
          <div className="panel-head">
            <h2>Manual channel</h2>
            <span>secondary path</span>
          </div>
          <form className="dense-form" onSubmit={(event) => void createManualChannel(event)}>
            <label className="field">
              <span>Agent</span>
              <select
                value={form.agentID}
                onChange={(event) => setForm((current) => ({ ...current, agentID: event.target.value }))}
                required
              >
                {agents.length === 0 ? <option value="">No agents</option> : null}
                {agents.map((agent) => (
                  <option key={agent.ID} value={agent.ID}>
                    {agent.Name || agent.Hostname || agent.ID}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>Alias</span>
              <input
                value={form.alias}
                onChange={(event) => setForm((current) => ({ ...current, alias: event.target.value }))}
                placeholder="console-1"
                required
              />
            </label>
            <label className="field">
              <span>ID path</span>
              <input
                value={form.idPath}
                onChange={(event) => setForm((current) => ({ ...current, idPath: event.target.value }))}
                placeholder="pci-0000:00..."
              />
            </label>
            <label className="field">
              <span>ID path tag</span>
              <input
                value={form.idPathTag}
                onChange={(event) => setForm((current) => ({ ...current, idPathTag: event.target.value }))}
                placeholder="usb-port-if00"
              />
            </label>
            <label className="field">
              <span>RFC2217 port</span>
              <input
                value={form.port}
                type="number"
                min="1"
                max="65535"
                onChange={(event) => setForm((current) => ({ ...current, port: event.target.value }))}
                required
              />
            </label>
            <label className="field">
              <span>Baud</span>
              <input
                value={form.baud}
                type="number"
                min="1"
                onChange={(event) => setForm((current) => ({ ...current, baud: event.target.value }))}
                required
              />
            </label>
            <FormFeedback state={manualState} />
            <div className="form-actions">
              <button type="submit" disabled={manualState.busy || agents.length === 0}>
                <Plug size={15} aria-hidden="true" />
                {manualState.busy ? 'Adding' : 'Add channel'}
              </button>
            </div>
          </form>
        </div>
      </div>
    </section>
  );
}

function CalibrationView({
  agents,
  channels,
  onRefresh
}: {
  agents: Agent[];
  channels: Channel[];
  onRefresh: () => Promise<void>;
}) {
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [selectedID, setSelectedID] = useState('');
  const [loading, setLoading] = useState(true);
  const [state, setState] = useState<RequestState>(emptyRequest);
  const selectedCandidate = useMemo(
    () => candidates.find((candidate) => candidate.ID === selectedID) ?? candidates[0],
    [candidates, selectedID]
  );
  const [form, setForm] = useState(() => defaultConfirmForm(undefined, channels));

  const agentByID = useMemo(() => new Map(agents.map((agent) => [agent.ID, agent])), [agents]);

  const refreshCandidates = useCallback(async () => {
    setLoading(true);
    setState((current) => ({ ...current, error: null }));
    try {
      const next = await getJSON<Candidate[]>('/api/candidates');
      setCandidates(next);
      setSelectedID((current) => (next.some((candidate) => candidate.ID === current) ? current : next[0]?.ID ?? ''));
    } catch (err) {
      setState({ busy: false, error: errorMessage(err), message: null });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refreshCandidates();
  }, [refreshCandidates]);

  useEffect(() => {
    setForm(defaultConfirmForm(selectedCandidate, channels));
  }, [selectedCandidate, channels]);

  async function confirmCandidate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selectedCandidate) {
      return;
    }
    setState({ busy: true, error: null, message: null });
    try {
      const payload = {
        alias: form.alias.trim(),
        role: form.role.trim() || 'console',
        rfc2217_port: Number(form.port),
        default_baud: Number(form.baud),
        default_data_bits: 8,
        default_parity: 'N',
        default_stop_bits: 1,
        default_flow: 'none'
      } as const;
      await postJSON<Channel, typeof payload>(
        `/api/candidates/${encodeURIComponent(selectedCandidate.ID)}/confirm`,
        payload
      );
      await Promise.all([refreshCandidates(), onRefresh()]);
      setState({ busy: false, error: null, message: 'Candidate confirmed' });
    } catch (err) {
      setState({ busy: false, error: errorMessage(err), message: null });
    }
  }

  return (
    <section className="view">
      <ViewTitle icon={Gauge} title="Calibration" action="Confirm mapping" />
      <div className="workflow">
        <div className="panel">
          <div className="panel-head">
            <h2>Discovered candidates</h2>
            <span>{loading ? 'Loading' : `${candidates.length} pending`}</span>
          </div>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Device</th>
                  <th>Agent</th>
                  <th>VID/PID</th>
                  <th>Driver</th>
                  <th>Last seen</th>
                </tr>
              </thead>
              <tbody>
                {candidates.length === 0 ? (
                  <EmptyRow colSpan={5} label="No pending candidates" />
                ) : (
                  candidates.map((candidate) => {
                    const agent = agentByID.get(candidate.AgentID);
                    return (
                      <tr
                        key={candidate.ID}
                        className={candidate.ID === selectedCandidate?.ID ? 'selected-row' : undefined}
                        onClick={() => setSelectedID(candidate.ID)}
                        onKeyDown={(event) => {
                          if (event.key === 'Enter' || event.key === ' ') {
                            event.preventDefault();
                            setSelectedID(candidate.ID);
                          }
                        }}
                        role="button"
                        tabIndex={0}
                        aria-pressed={candidate.ID === selectedCandidate?.ID}
                      >
                        <td>
                          <strong>{candidateAlias(candidate)}</strong>
                          <small>{candidate.DevName}</small>
                        </td>
                        <td>{agent?.Name || agent?.Hostname || candidate.AgentID}</td>
                        <td>
                          {candidate.VID || '-'}:{candidate.PID || '-'}
                        </td>
                        <td>{candidate.Driver || '-'}</td>
                        <td>{formatTime(candidate.LastSeen)}</td>
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          </div>
        </div>
        <div className="panel narrow">
          <div className="panel-head">
            <h2>Confirm channel</h2>
            <span>{selectedCandidate ? selectedCandidate.DevName : 'No selection'}</span>
          </div>
          <form className="dense-form" onSubmit={(event) => void confirmCandidate(event)}>
            <label className="field">
              <span>Alias</span>
              <input
                value={form.alias}
                onChange={(event) => setForm((current) => ({ ...current, alias: event.target.value }))}
                required
              />
            </label>
            <label className="field">
              <span>Role</span>
              <input
                value={form.role}
                onChange={(event) => setForm((current) => ({ ...current, role: event.target.value }))}
                required
              />
            </label>
            <label className="field">
              <span>RFC2217 port</span>
              <input
                value={form.port}
                type="number"
                min="1"
                max="65535"
                onChange={(event) => setForm((current) => ({ ...current, port: event.target.value }))}
                required
              />
            </label>
            <label className="field">
              <span>Baud</span>
              <input
                value={form.baud}
                type="number"
                min="1"
                onChange={(event) => setForm((current) => ({ ...current, baud: event.target.value }))}
                required
              />
            </label>
            {selectedCandidate ? <CandidateDetails candidate={selectedCandidate} /> : null}
            <FormFeedback state={state} />
            <div className="form-actions">
              <button type="button" onClick={() => void refreshCandidates()} disabled={loading || state.busy}>
                <RefreshCw size={15} aria-hidden="true" />
                Refresh
              </button>
              <button type="submit" disabled={!selectedCandidate || state.busy}>
                <PlugZap size={15} aria-hidden="true" />
                {state.busy ? 'Confirming' : 'Confirm'}
              </button>
            </div>
          </form>
        </div>
      </div>
    </section>
  );
}

function CandidateDetails({ candidate }: { candidate: Candidate }) {
  return (
    <dl className="detail-grid compact">
      <div>
        <dt>ID_PATH</dt>
        <dd>{candidate.IDPath || '-'}</dd>
      </div>
      <div>
        <dt>ID_PATH_TAG</dt>
        <dd>{candidate.IDPathTag || '-'}</dd>
      </div>
      <div>
        <dt>Interface</dt>
        <dd>{candidate.Interface || '-'}</dd>
      </div>
      <div>
        <dt>Serial</dt>
        <dd>{candidate.Serial || '-'}</dd>
      </div>
      <div>
        <dt>Product</dt>
        <dd>{candidate.Product || '-'}</dd>
      </div>
      <div>
        <dt>Manufacturer</dt>
        <dd>{candidate.Manufacturer || '-'}</dd>
      </div>
    </dl>
  );
}

function TerminalView({ channels }: { channels: Channel[] }) {
  const [selectedID, setSelectedID] = useState(channels[0]?.ID ?? '');
  const [logLines, setLogLines] = useState<LogLine[]>([]);
  const [input, setInput] = useState('');
  const [baud, setBaud] = useState('115200');
  const [dtr, setDTR] = useState(true);
  const [rts, setRTS] = useState(true);
  const [terminalStatus, setTerminalStatus] = useState<TerminalStatus>('idle');
  const [terminalError, setTerminalError] = useState<string | null>(null);
  const [pendingCount, setPendingCount] = useState(0);
  const terminalWS = useRef<WebSocket | null>(null);
  const outputRef = useRef<HTMLDivElement | null>(null);

  const channelByID = useMemo(() => new Map(channels.map((channel) => [channel.ID, channel])), [channels]);
  const selectedChannel = channelByID.get(selectedID) ?? channels[0];
  const selectedChannelID = selectedChannel?.ID ?? '';
  const connected = terminalStatus === 'connected';

  useEffect(() => {
    if (!selectedChannel) {
      setBaud('115200');
      return;
    }
    if (!connected) {
      setBaud(String(selectedChannel.DefaultBaud || 115200));
    }
  }, [connected, selectedChannel]);

  useEffect(() => {
    if (!selectedChannelID) {
      setSelectedID('');
      return;
    }
    if (!channelByID.has(selectedID)) {
      setSelectedID(selectedChannelID);
    }
  }, [channelByID, selectedChannelID, selectedID]);

  useEffect(() => {
    setLogLines([]);
    if (!selectedChannelID) {
      return undefined;
    }
    let closedByCleanup = false;
    const socket = new WebSocket(wsURL(`/ws/live-log/${encodeURIComponent(selectedChannelID)}`));
    socket.onmessage = (event) => {
      if (closedByCleanup) {
        return;
      }
      try {
        const frame = JSON.parse(String(event.data)) as LiveLogFrame;
        setLogLines((current) => appendLogLine(current, frame));
      } catch (err) {
        setLogLines((current) => appendTextLine(current, 'ERR', errorMessage(err)));
      }
    };
    socket.onerror = () => {
      if (closedByCleanup) {
        return;
      }
      setLogLines((current) => appendTextLine(current, 'ERR', 'live log websocket error'));
    };
    socket.onclose = (event) => {
      if (closedByCleanup) {
        return;
      }
      if (event.code !== 1000) {
        setLogLines((current) => appendTextLine(current, 'ERR', event.reason || 'live log closed'));
      }
    };
    return () => {
      closedByCleanup = true;
      socket.close();
    };
  }, [selectedChannelID]);

  useEffect(() => {
    return () => {
      terminalWS.current?.close();
    };
  }, []);

  useEffect(() => {
    terminalWS.current?.close();
    terminalWS.current = null;
    setTerminalStatus('idle');
    setTerminalError(null);
    setPendingCount(0);
  }, [selectedChannelID]);

  useEffect(() => {
    outputRef.current?.scrollTo({ top: outputRef.current.scrollHeight });
  }, [logLines]);

  function connectTerminal() {
    if (!selectedChannelID || terminalStatus === 'connecting' || terminalStatus === 'connected') {
      return;
    }
    setTerminalStatus('connecting');
    setTerminalError(null);
    const socket = new WebSocket(wsURL(`/ws/terminal/${encodeURIComponent(selectedChannelID)}`));
    terminalWS.current = socket;
    socket.onopen = () => {
      if (terminalWS.current === socket) {
        setTerminalStatus('connected');
      }
    };
    socket.onmessage = (event) => {
      if (terminalWS.current !== socket) {
        return;
      }
      try {
        const result = JSON.parse(String(event.data)) as OperationResult;
        setPendingCount((count) => Math.max(0, count - 1));
        if (!result.ok) {
          setTerminalError(result.error || 'operation failed');
        }
      } catch (err) {
        setTerminalError(errorMessage(err));
      }
    };
    socket.onerror = () => {
      if (terminalWS.current !== socket) {
        return;
      }
      setTerminalStatus('error');
      setTerminalError('terminal websocket error');
    };
    socket.onclose = (event) => {
      if (terminalWS.current !== socket) {
        return;
      }
      terminalWS.current = null;
      setPendingCount(0);
      setTerminalStatus(event.code === 1000 ? 'idle' : 'error');
      setTerminalError(event.code === 1000 ? null : event.reason || 'terminal closed');
    };
  }

  function disconnectTerminal() {
    terminalWS.current?.close();
    terminalWS.current = null;
    setTerminalStatus('idle');
    setTerminalError(null);
    setPendingCount(0);
  }

  function sendTerminalMessage(message: TerminalMessage) {
    if (!terminalWS.current || terminalWS.current.readyState !== WebSocket.OPEN) {
      setTerminalError('terminal is not connected');
      return false;
    }
    terminalWS.current.send(JSON.stringify(message));
    setPendingCount((count) => count + 1);
    return true;
  }

  function sendInput(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!input) {
      return;
    }
    const sent = sendTerminalMessage({
      type: 'terminal_write',
      request_id: requestID(),
      data: base64Encode(input)
    });
    if (sent) {
      setInput('');
    }
  }

  function applySerialConfig() {
    sendTerminalMessage({
      type: 'serial_set_config',
      request_id: requestID(),
      baud: Number(baud),
      data_bits: 8,
      parity: 'N',
      stop_bits: 1,
      flow: 'none'
    });
  }

  function updateDTR(value: boolean) {
    setDTR(value);
    sendTerminalMessage({ type: 'serial_set_dtr', request_id: requestID(), value });
  }

  function updateRTS(value: boolean) {
    setRTS(value);
    sendTerminalMessage({ type: 'serial_set_rts', request_id: requestID(), value });
  }

  return (
    <section className="view terminal-view">
      <ViewTitle icon={TerminalSquare} title="Live Log / Terminal" action="Connect to control" />
      <div className="terminal-layout">
        <div className="panel terminal-panel">
          <div className="panel-head">
            <h2>{selectedChannel ? selectedChannel.Alias || selectedChannel.AutoName : 'No channel selected'}</h2>
            <span>RX/TX from live log WS</span>
          </div>
          <div className="terminal-output" aria-label="Live serial log" ref={outputRef}>
            {logLines.length === 0 ? (
              <div className="terminal-empty">Waiting for live frames</div>
            ) : (
              logLines.map((line) => (
                <div className="log-line" key={line.id}>
                  <span>{line.ts}</span>
                  <b className={line.dir.toLowerCase()}>{line.dir}</b>
                  <code>{line.text}</code>
                </div>
              ))
            )}
          </div>
          <form className="terminal-input" onSubmit={sendInput}>
            <span>&gt;</span>
            <input
              value={input}
              onChange={(event) => setInput(event.target.value)}
              placeholder={connected ? 'Type command' : 'Connect before sending'}
              disabled={!connected}
            />
            <button type="submit" disabled={!connected || pendingCount > 0}>
              <Send size={15} aria-hidden="true" />
              Send
            </button>
          </form>
        </div>
        <div className="panel narrow controls">
          <div className="panel-head">
            <h2>Control</h2>
            <span>{pendingCount > 0 ? `${pendingCount} pending` : terminalStatus}</span>
          </div>
          <label className="field">
            <span>Channel</span>
            <select
              value={selectedChannelID}
              onChange={(event) => setSelectedID(event.target.value)}
              disabled={connected || terminalStatus === 'connecting'}
            >
              {channels.length === 0 ? <option value="">No channels</option> : null}
              {channels.map((channel) => (
                <option key={channel.ID} value={channel.ID}>
                  {channel.Alias || channel.AutoName}
                </option>
              ))}
            </select>
          </label>
          <div className="connect-row">
            <button type="button" onClick={connectTerminal} disabled={!selectedChannelID || connected}>
              <Power size={15} aria-hidden="true" />
              {terminalStatus === 'connecting' ? 'Connecting' : 'Connect'}
            </button>
            <button type="button" onClick={disconnectTerminal} disabled={!connected}>
              <Unplug size={15} aria-hidden="true" />
              Disconnect
            </button>
          </div>
          <label className="toggle">
            <input type="checkbox" checked={dtr} onChange={(event) => updateDTR(event.target.checked)} disabled={!connected} />
            DTR
          </label>
          <label className="toggle">
            <input type="checkbox" checked={rts} onChange={(event) => updateRTS(event.target.checked)} disabled={!connected} />
            RTS
          </label>
          <label className="field">
            <span>Baudrate</span>
            <input value={baud} type="number" min="1" onChange={(event) => setBaud(event.target.value)} />
          </label>
          <div className="connect-row">
            <button type="button" onClick={applySerialConfig} disabled={!connected || pendingCount > 0}>
              Apply
            </button>
            <button
              type="button"
              className="danger"
              onClick={() =>
                sendTerminalMessage({ type: 'serial_send_break', request_id: requestID(), duration_ms: 250 })
              }
              disabled={!connected || pendingCount > 0}
            >
              Break
            </button>
          </div>
          {terminalError ? <div className="inline-error">{terminalError}</div> : null}
        </div>
      </div>
    </section>
  );
}

function LogsView({ channels }: { channels: Channel[] }) {
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

function ViewTitle({
  icon: Icon,
  title,
  action
}: {
  icon: LucideIcon;
  title: string;
  action: string;
}) {
  return (
    <div className="view-title">
      <div>
        <Icon size={20} aria-hidden="true" />
        <h1>{title}</h1>
      </div>
      <span className="view-action">
        <Settings2 size={15} aria-hidden="true" />
        {action}
      </span>
    </div>
  );
}

function Badge({ value }: { value: string }) {
  const normalized = value ? value.toLowerCase() : 'unknown';
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

function FormFeedback({ state }: { state: RequestState }) {
  if (state.error) {
    return <div className="inline-error">{state.error}</div>;
  }
  if (state.message) {
    return <div className="inline-success">{state.message}</div>;
  }
  return null;
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

function nextRFC2217Port(channels: Channel[]) {
  const used = new Set(channels.map((channel) => channel.RFC2217Port));
  let port = 7001;
  while (used.has(port)) {
    port += 1;
  }
  return port;
}

function candidateAlias(candidate: Candidate) {
  return (
    candidate.IDPathTag ||
    candidate.Product ||
    candidate.DevName.split('/').pop() ||
    candidate.Interface ||
    candidate.ID
  ).replace(/\s+/g, '-');
}

function appendLogLine(current: LogLine[], frame: LiveLogFrame) {
  const text = decodePayload(frame.payload);
  const line: LogLine = {
    id: `${frame.seq}-${frame.timestamp_ns}`,
    ts: frame.timestamp_ns ? new Date(Math.floor(frame.timestamp_ns / 1000000)).toLocaleTimeString() : String(frame.seq),
    dir: formatDirection(frame.direction),
    text
  };
  return [...current.slice(-499), line];
}

function appendTextLine(current: LogLine[], dir: string, text: string) {
  return [
    ...current.slice(-499),
    {
      id: `${Date.now()}-${current.length}`,
      ts: new Date().toLocaleTimeString(),
      dir,
      text
    }
  ];
}

function decodePayload(payload: string) {
  try {
    const bytes = Uint8Array.from(atob(payload), (char) => char.charCodeAt(0));
    return textDecoder.decode(bytes);
  } catch {
    return payload;
  }
}

function base64Encode(value: string) {
  const bytes = textEncoder.encode(value);
  let binary = '';
  bytes.forEach((byte) => {
    binary += String.fromCharCode(byte);
  });
  return btoa(binary);
}

function formatDirection(direction: LiveLogFrame['direction']) {
  if (direction === 1 || direction === '1') {
    return 'RX';
  }
  if (direction === 2 || direction === '2') {
    return 'TX';
  }
  return String(direction).toUpperCase();
}

function requestID() {
  if (window.crypto?.randomUUID) {
    return window.crypto.randomUUID();
  }
  return `web-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : 'Request failed';
}
