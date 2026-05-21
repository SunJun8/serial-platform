export async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) {
    throw new Error(await res.text());
  }
  return res.json() as Promise<T>;
}

export async function postJSON<T, B = unknown>(path: string, body?: B): Promise<T> {
  const init: RequestInit = { method: 'POST' };
  if (body !== undefined) {
    init.headers = { 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body);
  }
  const res = await fetch(path, init);
  if (!res.ok) {
    throw new Error(await res.text());
  }
  return res.json() as Promise<T>;
}

export async function patchJSON<T, B = unknown>(path: string, body: B): Promise<T> {
  const res = await fetch(path, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body)
  });
  if (!res.ok) {
    throw new Error(await res.text());
  }
  return res.json() as Promise<T>;
}

export function wsURL(path: string): string {
  const base = new URL(path, window.location.href);
  base.protocol = base.protocol === 'https:' ? 'wss:' : 'ws:';
  return base.toString();
}

export function downloadURL(path: string, params: Record<string, string | boolean | undefined>): string {
  const url = new URL(path, window.location.href);
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') {
      url.searchParams.set(key, String(value));
    }
  });
  return `${url.pathname}${url.search}`;
}
