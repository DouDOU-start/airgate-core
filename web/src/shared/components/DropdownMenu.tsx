import { type ReactNode, forwardRef } from 'react';
import { createPortal } from 'react-dom';

export interface DropdownMenuItem {
  icon?: ReactNode;
  label: string;
  onClick: () => void;
  danger?: boolean;
  divider?: boolean;
}

interface DropdownMenuProps {
  items: DropdownMenuItem[];
  position: { top: number; left: number };
  onClose: () => void;
}

export const DropdownMenu = forwardRef<HTMLDivElement, DropdownMenuProps>(
  ({ items, position, onClose }, ref) => {
    return createPortal(
      <div
        ref={ref}
        className="fixed p-1 rounded-[10px] shadow-lg min-w-[140px]"
        style={{
          top: position.top,
          left: position.left,
          transform: 'translateX(-100%)',
          zIndex: 9999,
          background: 'var(--ag-bg-elevated)',
          border: '1px solid var(--ag-glass-border)',
        }}
      >
        {items.map((item, idx) => (
          <div key={idx}>
            {item.divider && (
              <div className="my-1 border-t" style={{ borderColor: 'var(--ag-border-subtle)' }} />
            )}
            <button
              className="flex items-center gap-2.5 w-full px-3 py-2 text-sm rounded-lg hover:bg-bg-hover transition-colors text-left cursor-pointer"
              style={{ color: item.danger ? 'var(--ag-danger)' : 'var(--ag-text-secondary)' }}
              onClick={() => {
                item.onClick();
                onClose();
              }}
            >
              {item.icon}
              {item.label}
            </button>
          </div>
        ))}
      </div>,
      document.body,
    );
  },
);

DropdownMenu.displayName = 'DropdownMenu';
