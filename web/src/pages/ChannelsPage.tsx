import { type FormEvent, useEffect, useState } from 'react';
import { Cable, Plug, Trash2 } from 'lucide-react';
import { deleteNoContent, postJSON } from '../api';
import { Badge } from '../components/Badge';
import { EmptyRow } from '../components/EmptyRow';
import { FormFeedback } from '../components/FormFeedback';
import { ViewTitle } from '../components/ViewTitle';
import { useI18n } from '../i18n-context';
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
  const { t } = useI18n();
  const [form, setForm] = useState(() => defaultManualForm(allChannels, agents));
  const [manualState, setManualState] = useState<RequestState>(emptyRequest);
  const [statusBusyID, setStatusBusyID] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Channel | null>(null);
  const [deleteState, setDeleteState] = useState<RequestState>(emptyRequest);

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
      setManualState({ busy: false, error: null, message: t('manualChannelAdded') });
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

  async function deleteChannel(channel: Channel) {
    setDeleteState({ busy: true, error: null, message: null });
    try {
      await deleteNoContent(`/api/channels/${encodeURIComponent(channel.ID)}`);
      setDeleteTarget(null);
      setDeleteState({ busy: false, error: null, message: t('channelDeleted') });
      await onRefresh();
    } catch (err) {
      setDeleteState({ busy: false, error: errorMessage(err), message: null });
    }
  }

  return (
    <section className="view">
      <ViewTitle icon={Cable} title={t('channelsTitle')} action={t('channelsAction')} />
      <div className="channels-layout">
        <div className="panel">
          <div className="panel-head">
            <h2>{t('serialChannels')}</h2>
            <span>{loading ? t('loading') : `${channels.length} ${t('shown')}${query ? ` ${t('afterFilter')}` : ''}`}</span>
          </div>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>{t('alias')}</th>
                  <th>{t('status')}</th>
                  <th>{t('role')}</th>
                  <th>{t('rfc2217')}</th>
                  <th>{t('defaults')}</th>
                  <th>{t('device')}</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {channels.length === 0 ? (
                  <EmptyRow colSpan={7} label={t('noChannelsMatch')} />
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
                      <td>{channel.Role || t('console')}</td>
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
                          {channel.Status === 'disabled' ? t('enable') : t('disable')}
                        </button>
                        <button
                          type="button"
                          className="danger subtle"
                          onClick={() => {
                            setDeleteTarget(channel);
                            setDeleteState(emptyRequest);
                          }}
                        >
                          <Trash2 size={14} aria-hidden="true" />
                          {t('deleteChannel')}
                        </button>
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
          {deleteTarget ? (
            <div className="danger-confirm" role="alertdialog" aria-modal="true" aria-labelledby="delete-channel-title">
              <h3 id="delete-channel-title">{deleteTarget.Alias || deleteTarget.AutoName}</h3>
              <p>{t('deleteChannelConfirm')}</p>
              <FormFeedback state={deleteState} />
              <div className="connect-row">
                <button type="button" onClick={() => setDeleteTarget(null)} disabled={deleteState.busy}>
                  {t('cancel')}
                </button>
                <button
                  type="button"
                  className="danger"
                  onClick={() => void deleteChannel(deleteTarget)}
                  disabled={deleteState.busy}
                >
                  {deleteState.busy ? t('deleting') : t('confirmDelete')}
                </button>
              </div>
            </div>
          ) : null}
          {deleteTarget ? null : <FormFeedback state={deleteState} />}
        </div>
        <div className="panel narrow">
          <div className="panel-head">
            <h2>{t('manualChannel')}</h2>
            <span>{t('secondaryPath')}</span>
          </div>
          <form className="dense-form" onSubmit={(event) => void createManualChannel(event)}>
            <label className="field">
              <span>{t('agent')}</span>
              <select
                value={form.agentID}
                onChange={(event) => setForm((current) => ({ ...current, agentID: event.target.value }))}
                required
              >
                {agents.length === 0 ? <option value="">{t('noAgents')}</option> : null}
                {agents.map((agent) => (
                  <option key={agent.ID} value={agent.ID}>
                    {agent.Name || agent.Hostname || agent.ID}
                  </option>
                ))}
              </select>
            </label>
            <label className="field">
              <span>{t('alias')}</span>
              <input
                value={form.alias}
                onChange={(event) => setForm((current) => ({ ...current, alias: event.target.value }))}
                placeholder="console-1"
                required
              />
            </label>
            <label className="field">
              <span>{t('idPath')}</span>
              <input
                value={form.idPath}
                onChange={(event) => setForm((current) => ({ ...current, idPath: event.target.value }))}
                placeholder="pci-0000:00..."
              />
            </label>
            <label className="field">
              <span>{t('idPathTag')}</span>
              <input
                value={form.idPathTag}
                onChange={(event) => setForm((current) => ({ ...current, idPathTag: event.target.value }))}
                placeholder="usb-port-if00"
              />
            </label>
            <label className="field">
              <span>{t('rfc2217Port')}</span>
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
              <span>{t('baud')}</span>
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
                {manualState.busy ? t('adding') : t('addChannel')}
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
