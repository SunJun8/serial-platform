export type ViewKey = 'agents' | 'devices' | 'channels' | 'terminal' | 'logs';

export type Language = 'en' | 'zh-CN';

export type RequestState = {
  busy: boolean;
  error: string | null;
  message: string | null;
};

export type RefreshState = 'idle' | 'loading' | 'success' | 'error';

export type TerminalStatus = 'idle' | 'connecting' | 'connected' | 'error';

export type LogDisplayLine = {
  id: string;
  ts: string;
  dir: string;
  text: string;
};

export type Agent = {
  ID: string;
  Name: string;
  Status: 'pending' | 'active' | 'offline' | string;
  Hostname: string;
  OS: string;
  Arch: string;
  MachineID: string;
  UpdatedAt: string;
};

export type Channel = {
  ID: string;
  AgentID: string;
  AutoName: string;
  Alias: string;
  Role: string;
  DevName: string;
  IDPath: string;
  IDPathTag: string;
  SysfsDevpath: string;
  RFC2217Port: number;
  Status: 'online' | 'offline' | 'busy' | 'disabled' | 'error' | string;
  DefaultBaud: number;
  DefaultDataBits: number;
  DefaultParity: string;
  DefaultStopBits: number;
  DefaultFlow: string;
  ErrorMessage: string;
  UpdatedAt: string;
};

export type Candidate = {
  ID: string;
  AgentID: string;
  DevName: string;
  IDPath: string;
  IDPathTag: string;
  SysfsDevpath: string;
  Interface: string;
  VID: string;
  PID: string;
  Serial: string;
  Driver: string;
  Manufacturer: string;
  Product: string;
  FirstSeen: string;
  LastSeen: string;
};

export type ChannelPayload = {
  agent_id: string;
  alias: string;
  role: string;
  id_path?: string;
  id_path_tag?: string;
  rfc2217_port: number;
  default_baud: number;
  default_data_bits: 8;
  default_parity: 'N';
  default_stop_bits: 1;
  default_flow: 'none';
};

export type TerminalMessage =
  | {
      type: 'terminal_write';
      request_id: string;
      data: string;
    }
  | {
      type: 'serial_set_config';
      request_id: string;
      baud: number;
      data_bits: number;
      parity: 'N';
      stop_bits: 1;
      flow: 'none';
    }
  | {
      type: 'serial_set_dtr' | 'serial_set_rts';
      request_id: string;
      value: boolean;
    }
  | {
      type: 'serial_send_break';
      request_id: string;
      duration_ms: number;
    };

export type OperationResult = {
  type: 'operation_result';
  request_id: string;
  ok: boolean;
  error?: string;
};

export type LiveLogFrame = {
  channel_id: string;
  seq: number;
  timestamp_ns: number;
  direction: 'rx' | 'tx' | 1 | 2 | string | number;
  flags: number;
  payload: string;
};
