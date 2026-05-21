export function Badge({ value }: { value: string }) {
  const normalized = value ? value.toLowerCase() : 'unknown';
  return <span className={`badge ${normalized}`}>{value || 'unknown'}</span>;
}
