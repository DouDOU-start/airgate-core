interface TabItem {
  key: string;
  label: string;
}

interface TabsProps {
  items: TabItem[];
  activeKey: string;
  onChange: (key: string) => void;
  variant?: 'pill' | 'underline';
  className?: string;
}

export function Tabs({ items, activeKey, onChange, variant = 'pill', className = '' }: TabsProps) {
  if (variant === 'underline') {
    return (
      <div className={`flex border-b border-border ${className}`}>
        {items.map((item) => (
          <button
            key={item.key}
            onClick={() => onChange(item.key)}
            className={`px-3 pb-2.5 text-[13px] font-medium transition-all cursor-pointer relative ${
              activeKey === item.key
                ? 'text-primary'
                : 'text-text-tertiary hover:text-text-secondary'
            }`}
          >
            {item.label}
            {activeKey === item.key && (
              <div className="absolute bottom-0 left-0 right-0 h-0.5 bg-primary rounded-full" />
            )}
          </button>
        ))}
      </div>
    );
  }

  return (
    <div className={`inline-flex items-center gap-1 rounded-[10px] bg-bg-elevated p-1 ${className}`}>
      {items.map((item) => (
        <button
          key={item.key}
          onClick={() => onChange(item.key)}
          className={`px-2.5 py-1 text-[13px] font-medium rounded-lg transition-all cursor-pointer ${
            activeKey === item.key
              ? 'bg-surface text-text shadow-sm'
              : 'text-text-tertiary hover:text-text-secondary'
          }`}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}
