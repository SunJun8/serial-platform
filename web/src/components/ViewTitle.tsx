import type { LucideIcon } from 'lucide-react';
import { Settings2 } from 'lucide-react';

export function ViewTitle({ icon: Icon, title, action }: { icon: LucideIcon; title: string; action: string }) {
  return (
    <div className="view-title">
      <div>
        <Icon size={20} aria-hidden="true" />
        <h1>{title}</h1>
      </div>
      <span className="view-action">
        <Settings2 size={15} aria-hidden="true" />
        {action}
      </span>
    </div>
  );
}
