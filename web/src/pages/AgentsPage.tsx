import { Monitor, PlugZap } from 'lucide-react';
import { Badge } from '../components/Badge';
import { EmptyRow } from '../components/EmptyRow';
import { ViewTitle } from '../components/ViewTitle';
import type { Agent, Channel } from '../types';

export function AgentsPage({
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
      <ViewTitle icon={Monitor} title="Agents" action="Approve / Rename" />
      <div className="split-layout">
        <div className="panel">
          <div className="panel-head">
            <h2>Agent inventory</h2>
            <span>{loading ? 'Loading' : `${agents.length} agents`}</span>
          </div>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Status</th>
                  <th>Agent</th>
                  <th>OS / Arch</th>
                  <th>Updated</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {agents.length === 0 ? (
                  <EmptyRow colSpan={6} label="No agents registered" />
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
