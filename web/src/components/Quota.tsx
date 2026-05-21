import { Activity } from 'lucide-react';

export function Quota({ label, value, limit }: { label: string; value: string; limit: string }) {
  return (
    <div className="quota">
      <Activity size={16} aria-hidden="true" />
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{limit}</small>
    </div>
  );
}
