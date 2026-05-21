import { type FormEvent, useEffect, useState } from 'react';
import { Cable, Plug } from 'lucide-react';
import { postJSON } from '../api';
import { Badge } from '../components/Badge';
import { EmptyRow } from '../components/EmptyRow';
import { FormFeedback } from '../components/FormFeedback';
import { ViewTitle } from '../components/ViewTitle';
import type { Agent, Channel, ChannelPayload, RequestState } from '../types';

const emptyRequest: RequestState = { busy: false, error: null, message: null };

export function ChannelsPage({
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

function nextRFC2217Port(channels: Channel[]) {
  const used = new Set(channels.map((channel) => channel.RFC2217Port));
  let port = 7001;
  while (used.has(port)) {
    port += 1;
  }
  return port;
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : 'Request failed';
}
