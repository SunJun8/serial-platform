import { type FormEvent, useEffect, useMemo, useRef, useState } from 'react';
import { Power, Send, TerminalSquare, Unplug } from 'lucide-react';
import { wsURL } from '../api';
import { ViewTitle } from '../components/ViewTitle';
import type { Channel, LiveLogFrame, OperationResult, TerminalMessage } from '../types';

type TerminalStatus = 'idle' | 'connecting' | 'connected' | 'error';
type LogLine = {
  id: string;
  ts: string;
  dir: string;
  text: string;
};

const textDecoder = new TextDecoder();
const textEncoder = new TextEncoder();

export function TerminalPage({ channels }: { channels: Channel[] }) {
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
      <ViewTitle icon={TerminalSquare} title="Terminal" action="Connect to control" />
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
            <input
              type="checkbox"
              checked={dtr}
              onChange={(event) => updateDTR(event.target.checked)}
              disabled={!connected}
            />
            DTR
          </label>
          <label className="toggle">
            <input
              type="checkbox"
              checked={rts}
              onChange={(event) => updateRTS(event.target.checked)}
              disabled={!connected}
            />
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
