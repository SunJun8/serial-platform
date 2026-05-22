import { useCallback, useEffect, useMemo, useState } from 'react';
import type { LucideIcon } from 'lucide-react';
import { Cable, HardDrive, Monitor, PlugZap, RefreshCw, Router, Search, TerminalSquare } from 'lucide-react';
import { getJSON, postJSON } from './api';
import { Metric } from './components/Metric';
import { languages, type MessageKey } from './i18n';
import { useI18n } from './i18n-context';
import { AgentsPage } from './pages/AgentsPage';
import { ChannelsPage } from './pages/ChannelsPage';
import { DevicesPage } from './pages/DevicesPage';
import { LogsPage } from './pages/LogsPage';
import { TerminalPage } from './pages/TerminalPage';
import { TerminalSessionProvider } from './terminal-session';
import type { Agent, Channel, Language, RefreshState, ViewKey } from './types';

type NavItem = {
  key: ViewKey;
  labelKey: MessageKey;
  icon: LucideIcon;
};

const navItems: NavItem[] = [
  { key: 'agents', labelKey: 'navAgents', icon: Monitor },
  { key: 'devices', labelKey: 'navDevices', icon: PlugZap },
  { key: 'channels', labelKey: 'navChannels', icon: Cable },
  { key: 'terminal', labelKey: 'navTerminal', icon: TerminalSquare },
  { key: 'logs', labelKey: 'navLogs', icon: HardDrive }
];

export function App() {
  const { language, setLanguage, t } = useI18n();
  const [activeView, setActiveView] = useState<ViewKey>('agents');
  const [agents, setAgents] = useState<Agent[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busyAgentID, setBusyAgentID] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [refreshState, setRefreshState] = useState<RefreshState>('idle');
  const [lastUpdatedAt, setLastUpdatedAt] = useState<Date | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setRefreshState('loading');
    setError(null);
    try {
      const [nextAgents, nextChannels] = await Promise.all([
        getJSON<Agent[]>('/api/agents'),
        getJSON<Channel[]>('/api/channels')
      ]);
      setAgents(nextAgents);
      setChannels(nextChannels);
      setLastUpdatedAt(new Date());
      setRefreshState('success');
    } catch (err) {
      setError(errorMessage(err));
      setRefreshState('error');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    if (refreshState !== 'success') {
      return undefined;
    }
    const timeoutID = window.setTimeout(() => setRefreshState('idle'), 1800);
    return () => window.clearTimeout(timeoutID);
  }, [refreshState]);

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
  const apiStatus = error ? t('apiUnavailable') : loading ? t('loadingAPI') : t('apiConnected');
  const apiStatusClass = error ? 'status-dot error' : loading ? 'status-dot' : 'status-dot online';
  const refreshStatusText =
    refreshState === 'loading'
      ? t('refreshing')
      : refreshState === 'success'
        ? t('updatedJustNow')
        : refreshState === 'error'
          ? t('apiUnavailable')
          : lastUpdatedAt
            ? t('updatedJustNow')
            : '';
  const refreshTitle =
    refreshState === 'loading'
      ? t('refreshing')
      : refreshState === 'success'
        ? t('updatedJustNow')
        : refreshState === 'error'
          ? t('apiUnavailable')
          : t('refresh');

  return (
    <div className="shell">
      <aside className="sidebar" aria-label="Primary navigation">
        <div className="brand">
          <Router size={20} aria-hidden="true" />
          <div>
            <strong>{t('appName')}</strong>
            <span>{t('centralServer')}</span>
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
                <span>{t(item.labelKey)}</span>
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
            <Metric label={t('metricAgents')} value={agents.length} tone="neutral" />
            <Metric label={t('metricPending')} value={pendingAgents} tone={pendingAgents > 0 ? 'warn' : 'neutral'} />
            <Metric label={t('metricOnlineChannels')} value={channelStats.online} tone="good" />
            <Metric label={t('metricBusy')} value={channelStats.busy} tone={channelStats.busy > 0 ? 'warn' : 'neutral'} />
          </div>
          <div className="toolbar">
            <label className="search-box">
              <Search size={15} aria-hidden="true" />
              <input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder={t('filterChannels')}
              />
            </label>
            <label className="language-select">
              <span>{t('language')}</span>
              <select value={language} onChange={(event) => setLanguage(event.target.value as Language)}>
                {languages.map((item) => (
                  <option key={item.value} value={item.value}>
                    {item.label}
                  </option>
                ))}
              </select>
            </label>
            <span className="refresh-status">{refreshStatusText}</span>
            <button
              type="button"
              className={refreshState === 'loading' ? 'icon-button spinning' : 'icon-button'}
              onClick={() => void refresh()}
              title={refreshTitle}
              disabled={refreshState === 'loading'}
            >
              <RefreshCw size={16} aria-hidden="true" />
            </button>
          </div>
        </header>

        {error ? (
          <div className="error-strip">
            {t('apiError')}: {error}
          </div>
        ) : null}

        <TerminalSessionProvider channels={channels}>
          {activeView === 'agents' ? (
            <AgentsPage
              agents={agents}
              channels={channels}
              loading={loading}
              busyAgentID={busyAgentID}
              onApproveAgent={(agentID) => void approveAgent(agentID)}
            />
          ) : null}
          {activeView === 'devices' ? <DevicesPage agents={agents} channels={channels} onRefresh={refresh} /> : null}
          {activeView === 'channels' ? (
            <ChannelsPage
              agents={agents}
              channels={visibleChannels}
              allChannels={channels}
              loading={loading}
              query={query}
              onRefresh={refresh}
            />
          ) : null}
          {activeView === 'terminal' ? <TerminalPage channels={channels} /> : null}
          {activeView === 'logs' ? <LogsPage channels={channels} /> : null}
        </TerminalSessionProvider>
      </main>
    </div>
  );
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : 'Request failed';
}
