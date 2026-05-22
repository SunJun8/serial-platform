import { type FormEvent, useEffect, useMemo, useRef, useState } from 'react';
import { Power, Send, TerminalSquare, Unplug } from 'lucide-react';
import { wsURL } from '../api';
import { ViewTitle } from '../components/ViewTitle';
import { useI18n } from '../i18n-context';
import { appendLiveLogFrame, emptyLiveLogBuffer, liveLogLines } from '../live-log-buffer';
import { useTerminalSession } from '../terminal-session';
import type { Channel, LiveLogFrame } from '../types';

export function TerminalPage({ channels }: { channels: Channel[] }) {
  const { t } = useI18n();
  const session = useTerminalSession();
  const [logBuffer, setLogBuffer] = useState(() => emptyLiveLogBuffer());
  const [input, setInput] = useState('');
  const outputRef = useRef<HTMLDivElement | null>(null);

  const channelByID = useMemo(() => new Map(channels.map((channel) => [channel.ID, channel])), [channels]);
  const selectedChannel = channelByID.get(session.selectedChannelID) ?? channels[0];
  const selectedChannelID = selectedChannel?.ID ?? '';
  const logLines = liveLogLines(logBuffer);

  useEffect(() => {
    setLogBuffer(emptyLiveLogBuffer());
    if (!session.selectedChannelID) {
      return undefined;
    }
    let closedByCleanup = false;
    const socket = new WebSocket(wsURL(`/ws/live-log/${encodeURIComponent(session.selectedChannelID)}`));
    socket.onmessage = (event) => {
      if (closedByCleanup) {
        return;
      }
      try {
        const frame = JSON.parse(String(event.data)) as LiveLogFrame;
        setLogBuffer((current) => appendLiveLogFrame(current, frame));
      } catch (err) {
        setLogBuffer((current) => appendLiveLogFrame(current, errorFrame(session.selectedChannelID, errorMessage(err))));
      }
    };
    socket.onerror = () => {
      if (closedByCleanup) {
        return;
      }
      setLogBuffer((current) => appendLiveLogFrame(current, errorFrame(session.selectedChannelID, 'live log websocket error')));
    };
    socket.onclose = (event) => {
      if (closedByCleanup || event.code === 1000) {
        return;
      }
      setLogBuffer((current) =>
        appendLiveLogFrame(current, errorFrame(session.selectedChannelID, event.reason || 'live log closed'))
      );
    };
    return () => {
      closedByCleanup = true;
      socket.close();
    };
  }, [session.selectedChannelID]);

  useEffect(() => {
    outputRef.current?.scrollTo({ top: outputRef.current.scrollHeight });
  }, [logLines]);

  function sendInput(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!input) {
      return;
    }
    if (session.writeText(input)) {
      setInput('');
    }
  }

  return (
    <section className="view terminal-view">
      <ViewTitle icon={TerminalSquare} title={t('terminalTitle')} action={t('terminalAction')} />
      <div className="terminal-layout">
        <div className="panel terminal-panel">
          <div className="panel-head">
            <h2>{selectedChannel ? selectedChannel.Alias || selectedChannel.AutoName : t('noChannelSelected')}</h2>
            <span>{t('liveLogSource')}</span>
          </div>
          <div className="terminal-output" aria-label={t('liveSerialLog')} ref={outputRef}>
            {logLines.length === 0 ? (
              <div className="terminal-empty">{t('waitingForLiveFrames')}</div>
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
              placeholder={session.connected ? t('typeCommand') : t('connectBeforeSending')}
              disabled={!session.connected}
            />
            <button type="submit" disabled={!session.connected || session.pendingCount > 0}>
              <Send size={15} aria-hidden="true" />
              {t('send')}
            </button>
          </form>
        </div>
        <div className="panel narrow controls">
          <div className="panel-head">
            <h2>{t('control')}</h2>
            <span>{session.pendingCount > 0 ? `${session.pendingCount} ${t('pendingRequests')}` : session.status}</span>
          </div>
          <label className="field">
            <span>{t('channel')}</span>
            <select
              value={selectedChannelID}
              onChange={(event) => session.selectChannel(event.target.value)}
              disabled={session.status === 'connecting'}
            >
              {channels.length === 0 ? <option value="">{t('noChannels')}</option> : null}
              {channels.map((channel) => (
                <option key={channel.ID} value={channel.ID}>
                  {channel.Alias || channel.AutoName}
                </option>
              ))}
            </select>
          </label>
          <div className="connect-row">
            <button
              type="button"
              onClick={session.connect}
              disabled={!selectedChannelID || session.connected || session.status === 'connecting'}
            >
              <Power size={15} aria-hidden="true" />
              {session.status === 'connecting' ? t('connecting') : t('connect')}
            </button>
            <button type="button" onClick={session.disconnect} disabled={!session.connected}>
              <Unplug size={15} aria-hidden="true" />
              {t('disconnect')}
            </button>
          </div>
          <label className="toggle">
            <input
              type="checkbox"
              checked={session.dtr}
              onChange={(event) => session.setDTRValue(event.target.checked)}
              disabled={!session.connected}
            />
            DTR
          </label>
          <label className="toggle">
            <input
              type="checkbox"
              checked={session.rts}
              onChange={(event) => session.setRTSValue(event.target.checked)}
              disabled={!session.connected}
            />
            RTS
          </label>
          <label className="field">
            <span>{t('baudrate')}</span>
            <input value={session.baud} type="number" min="1" onChange={(event) => session.setBaud(event.target.value)} />
          </label>
          <div className="connect-row">
            <button type="button" onClick={session.applySerialConfig} disabled={!session.connected || session.pendingCount > 0}>
              {t('apply')}
            </button>
            <button
              type="button"
              className="danger"
              onClick={() => session.sendBreak(250)}
              disabled={!session.connected || session.pendingCount > 0}
            >
              {t('breakSignal')}
            </button>
          </div>
          {session.error ? <div className="inline-error">{session.error}</div> : null}
        </div>
      </div>
    </section>
  );
}

function errorFrame(channelID: string, text: string): LiveLogFrame {
  const now = Date.now();
  return {
    channel_id: channelID,
    seq: now,
    timestamp_ns: now * 1000000,
    direction: 'err',
    flags: 0,
    payload: base64Encode(text)
  };
}

function base64Encode(value: string) {
  const bytes = new TextEncoder().encode(value);
  let binary = '';
  bytes.forEach((byte) => {
    binary += String.fromCharCode(byte);
  });
  return btoa(binary);
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : 'Request failed';
}
