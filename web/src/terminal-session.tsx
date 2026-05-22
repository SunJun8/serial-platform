import { createContext, use, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { wsURL } from './api';
import type { Channel, OperationResult, TerminalMessage, TerminalStatus } from './types';

const textEncoder = new TextEncoder();

type TerminalSessionContextValue = {
  selectedChannelID: string;
  status: TerminalStatus;
  error: string | null;
  pendingCount: number;
  baud: string;
  dtr: boolean;
  rts: boolean;
  connected: boolean;
  selectChannel: (channelID: string) => void;
  connect: () => void;
  disconnect: () => void;
  setBaud: (baud: string) => void;
  applySerialConfig: () => void;
  setDTRValue: (value: boolean) => void;
  setRTSValue: (value: boolean) => void;
  writeText: (text: string) => boolean;
  sendBreak: (durationMS: number) => void;
};

const TerminalSessionContext = createContext<TerminalSessionContextValue | null>(null);

export function TerminalSessionProvider({ channels, children }: { channels: Channel[]; children: ReactNode }) {
  const [selectedChannelID, setSelectedChannelID] = useState(channels[0]?.ID ?? '');
  const [status, setStatus] = useState<TerminalStatus>('idle');
  const [error, setError] = useState<string | null>(null);
  const [pendingCount, setPendingCount] = useState(0);
  const [baud, setBaud] = useState('115200');
  const [dtr, setDTR] = useState(true);
  const [rts, setRTS] = useState(true);
  const socketRef = useRef<WebSocket | null>(null);
  const selectedRef = useRef(selectedChannelID);
  const connected = status === 'connected';

  const channelByID = useMemo(() => new Map(channels.map((channel) => [channel.ID, channel])), [channels]);
  const selectedChannel = channelByID.get(selectedChannelID);

  useEffect(() => {
    selectedRef.current = selectedChannelID;
  }, [selectedChannelID]);

  const disconnect = useCallback(() => {
    const socket = socketRef.current;
    socketRef.current = null;
    socket?.close();
    setStatus('idle');
    setError(null);
    setPendingCount(0);
  }, []);

  useEffect(() => {
    if (!selectedChannelID && channels[0]) {
      setSelectedChannelID(channels[0].ID);
      return;
    }
    if (selectedChannelID && !channelByID.has(selectedChannelID)) {
      disconnect();
      setSelectedChannelID(channels[0]?.ID ?? '');
    }
  }, [channelByID, channels, disconnect, selectedChannelID]);

  useEffect(() => {
    if (selectedChannel && !connected) {
      setBaud(String(selectedChannel.DefaultBaud || 115200));
    }
  }, [connected, selectedChannel]);

  useEffect(
    () => () => {
      socketRef.current?.close();
    },
    []
  );

  const selectChannel = useCallback(
    (channelID: string) => {
      if (channelID === selectedRef.current) {
        return;
      }
      disconnect();
      setSelectedChannelID(channelID);
    },
    [disconnect]
  );

  const connect = useCallback(() => {
    const channelID = selectedRef.current;
    if (!channelID || socketRef.current || status === 'connecting') {
      return;
    }
    setStatus('connecting');
    setError(null);
    const socket = new WebSocket(wsURL(`/ws/terminal/${encodeURIComponent(channelID)}`));
    socketRef.current = socket;
    socket.onopen = () => {
      if (socketRef.current === socket) {
        setStatus('connected');
      }
    };
    socket.onmessage = (event) => {
      if (socketRef.current !== socket) {
        return;
      }
      try {
        const result = JSON.parse(String(event.data)) as OperationResult;
        setPendingCount((count) => Math.max(0, count - 1));
        if (!result.ok) {
          setError(result.error || 'operation failed');
        }
      } catch (err) {
        setError(errorMessage(err));
      }
    };
    socket.onerror = () => {
      if (socketRef.current === socket) {
        setStatus('error');
        setError('terminal websocket error');
      }
    };
    socket.onclose = (event) => {
      if (socketRef.current !== socket) {
        return;
      }
      socketRef.current = null;
      setPendingCount(0);
      setStatus(event.code === 1000 ? 'idle' : 'error');
      setError(event.code === 1000 ? null : event.reason || 'terminal closed');
    };
  }, [status]);

  const sendTerminalMessage = useCallback((message: TerminalMessage) => {
    if (!socketRef.current || socketRef.current.readyState !== WebSocket.OPEN) {
      setError('terminal is not connected');
      return false;
    }
    socketRef.current.send(JSON.stringify(message));
    setPendingCount((count) => count + 1);
    return true;
  }, []);

  const writeText = useCallback(
    (text: string) =>
      sendTerminalMessage({
        type: 'terminal_write',
        request_id: requestID(),
        data: base64Encode(text)
      }),
    [sendTerminalMessage]
  );

  const applySerialConfig = useCallback(() => {
    sendTerminalMessage({
      type: 'serial_set_config',
      request_id: requestID(),
      baud: Number(baud),
      data_bits: 8,
      parity: 'N',
      stop_bits: 1,
      flow: 'none'
    });
  }, [baud, sendTerminalMessage]);

  const setDTRValue = useCallback(
    (value: boolean) => {
      setDTR(value);
      sendTerminalMessage({ type: 'serial_set_dtr', request_id: requestID(), value });
    },
    [sendTerminalMessage]
  );

  const setRTSValue = useCallback(
    (value: boolean) => {
      setRTS(value);
      sendTerminalMessage({ type: 'serial_set_rts', request_id: requestID(), value });
    },
    [sendTerminalMessage]
  );

  const sendBreak = useCallback(
    (durationMS: number) => {
      sendTerminalMessage({ type: 'serial_send_break', request_id: requestID(), duration_ms: durationMS });
    },
    [sendTerminalMessage]
  );

  const value = useMemo<TerminalSessionContextValue>(
    () => ({
      selectedChannelID,
      status,
      error,
      pendingCount,
      baud,
      dtr,
      rts,
      connected,
      selectChannel,
      connect,
      disconnect,
      setBaud,
      applySerialConfig,
      setDTRValue,
      setRTSValue,
      writeText,
      sendBreak
    }),
    [
      selectedChannelID,
      status,
      error,
      pendingCount,
      baud,
      dtr,
      rts,
      connected,
      selectChannel,
      connect,
      disconnect,
      applySerialConfig,
      setDTRValue,
      setRTSValue,
      writeText,
      sendBreak
    ]
  );

  return <TerminalSessionContext value={value}>{children}</TerminalSessionContext>;
}

export function useTerminalSession() {
  const value = use(TerminalSessionContext);
  if (!value) {
    throw new Error('useTerminalSession must be used within TerminalSessionProvider');
  }
  return value;
}

function requestID() {
  if (window.crypto?.randomUUID) {
    return window.crypto.randomUUID();
  }
  return `web-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function base64Encode(value: string) {
  const bytes = textEncoder.encode(value);
  let binary = '';
  bytes.forEach((byte) => {
    binary += String.fromCharCode(byte);
  });
  return btoa(binary);
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : 'Request failed';
}
