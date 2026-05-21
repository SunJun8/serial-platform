import { ListFilter } from 'lucide-react';

export function EmptyRow({ colSpan, label }: { colSpan: number; label: string }) {
  return (
    <tr>
      <td colSpan={colSpan} className="empty-row">
        <ListFilter size={15} aria-hidden="true" />
        {label}
      </td>
    </tr>
  );
}
