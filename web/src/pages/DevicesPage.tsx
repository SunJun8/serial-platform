import { type FormEvent, useCallback, useEffect, useMemo, useState } from 'react';
import { PlugZap, RefreshCw } from 'lucide-react';
import { getJSON, postJSON } from '../api';
import { EmptyRow } from '../components/EmptyRow';
import { FormFeedback } from '../components/FormFeedback';
import { ViewTitle } from '../components/ViewTitle';
import type { Agent, Candidate, Channel, RequestState } from '../types';

const emptyRequest: RequestState = { busy: false, error: null, message: null };

export function DevicesPage({
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
      <ViewTitle icon={PlugZap} title="Devices" action="Create channel" />
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
            {selectedCandidate ? <CandidateDetail candidate={selectedCandidate} /> : null}
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

function defaultConfirmForm(candidate: Candidate | undefined, channels: Channel[]) {
  const usedPorts = new Set(channels.map((channel) => channel.RFC2217Port));
  let port = 7001;
  while (usedPorts.has(port)) {
    port += 1;
  }
  return {
    alias: candidate ? candidateAlias(candidate) : '',
    role: 'console',
    port: String(port),
    baud: '115200'
  };
}

function CandidateDetail({ candidate }: { candidate: Candidate }) {
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

function candidateAlias(candidate: Candidate) {
  return (
    candidate.IDPathTag ||
    candidate.Product ||
    candidate.DevName.split('/').pop() ||
    candidate.Interface ||
    candidate.ID
  ).replace(/\s+/g, '-');
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

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : 'Request failed';
}
