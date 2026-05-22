import type { LiveLogFrame, LogDisplayLine } from './types';

const textDecoder = new TextDecoder();

export type LiveLogBuffer = {
  lines: LogDisplayLine[];
  current: LogDisplayLine | null;
  lastDirection: string | null;
  nextLineID: number;
};

type TextPart = {
  text: string;
  endsLine: boolean;
};

export function emptyLiveLogBuffer(): LiveLogBuffer {
  return { lines: [], current: null, lastDirection: null, nextLineID: 0 };
}

export function appendLiveLogFrame(buffer: LiveLogBuffer, frame: LiveLogFrame, limit = 500): LiveLogBuffer {
  const dir = formatDirection(frame.direction);
  const text = decodePayload(frame.payload);
  let lines = buffer.lines;
  let current = buffer.current;
  let nextLineID = buffer.nextLineID;

  if (current && buffer.lastDirection !== dir) {
    lines = pushLine(lines, current, limit);
    current = null;
  }

  const parts = splitPayloadLines(text);
  if (parts.length === 0) {
    if (!current) {
      current = newLine(frame, dir, nextLineID);
      nextLineID += 1;
    }
  }

  for (const part of parts) {
    if (!current) {
      current = newLine(frame, dir, nextLineID);
      nextLineID += 1;
    }
    current = { ...current, text: current.text + part.text };
    if (part.endsLine) {
      lines = pushLine(lines, current, limit);
      current = null;
    }
  }

  return { lines, current, lastDirection: dir, nextLineID };
}

export function liveLogLines(buffer: LiveLogBuffer): LogDisplayLine[] {
  return buffer.current ? [...buffer.lines, buffer.current] : buffer.lines;
}

export function decodePayload(payload: string) {
  try {
    const bytes = Uint8Array.from(atob(payload), (char) => char.charCodeAt(0));
    return textDecoder.decode(bytes);
  } catch {
    return payload;
  }
}

export function formatDirection(direction: LiveLogFrame['direction']) {
  if (direction === 1 || direction === '1' || direction === 'rx') {
    return 'RX';
  }
  if (direction === 2 || direction === '2' || direction === 'tx') {
    return 'TX';
  }
  return String(direction).toUpperCase();
}

function splitPayloadLines(text: string): TextPart[] {
  const parts: TextPart[] = [];
  let start = 0;
  for (let index = 0; index < text.length; index += 1) {
    if (text[index] !== '\n') {
      continue;
    }
    const end = index > start && text[index - 1] === '\r' ? index - 1 : index;
    parts.push({ text: text.slice(start, end), endsLine: true });
    start = index + 1;
  }
  if (start < text.length) {
    parts.push({ text: text.slice(start), endsLine: false });
  }
  return parts;
}

function newLine(frame: LiveLogFrame, dir: string, index: number): LogDisplayLine {
  return {
    id: lineID(frame, dir, index),
    ts: frame.timestamp_ns ? new Date(Math.floor(Number(frame.timestamp_ns) / 1000000)).toLocaleTimeString() : String(frame.seq),
    dir,
    text: ''
  };
}

function lineID(frame: LiveLogFrame, dir: string, index: number) {
  return `${frame.seq}-${frame.timestamp_ns}-${dir}-${index}`;
}

function pushLine(lines: LogDisplayLine[], line: LogDisplayLine, limit: number) {
  return [...lines, line].slice(-limit);
}
