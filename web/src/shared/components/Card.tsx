import { type ReactNode } from 'react';

/* ==================== Card ==================== */

interface CardProps {
  children: ReactNode;
  className?: string;
  title?: string;
  extra?: ReactNode;
  noPadding?: boolean;
}

export function Card({ children, className = '', title, extra, noPadding }: CardProps) {
  return (
    <div
      className={`border border-glass-border bg-bg-elevated shadow-sm rounded-xl ${className}`}
    >
      {title && (
        <div className="flex items-center justify-between px-5 py-4 border-b border-border">
          <h3 className="text-sm font-semibold text-text">{title}</h3>
          {extra}
        </div>
      )}
      <div className={noPadding ? '' : 'p-5'}>{children}</div>
    </div>
  );
}

/* ==================== StatCard ==================== */

interface StatCardProps {
  title: string;
  value: string | number;
  icon?: ReactNode;
  change?: string;
  changeType?: 'up' | 'down';
  accentColor?: string;
}

export function StatCard({ title, value, icon, change, changeType, accentColor = 'var(--ag-primary)' }: StatCardProps) {
  return (
    <div className="border border-glass-border bg-bg-elevated shadow-sm ag-iridescent-glow group relative overflow-hidden rounded-xl p-6 transition-all duration-200">
      {/* 顶部发光线 */}
      <div
        className="absolute top-0 left-0 right-0 h-px opacity-40 group-hover:opacity-80 transition-opacity"
        style={{ background: `linear-gradient(90deg, transparent, ${accentColor}, transparent)` }}
      />

      <div className="flex items-start justify-between">
        <div className="space-y-2">
          <p className="text-xs font-medium text-text-tertiary uppercase tracking-wider">
            {title}
          </p>
          <p className="text-2xl font-bold tracking-tight font-mono">
            {value}
          </p>
          {change && (
            <p
              className={`text-xs font-medium ${changeType === 'up' ? 'text-success' : 'text-danger'}`}
            >
              {changeType === 'up' ? '↑' : '↓'} {change}
            </p>
          )}
        </div>
        {icon && (
          <div
            className="flex items-center justify-center w-10 h-10 rounded-md transition-colors"
            style={{ background: `color-mix(in srgb, ${accentColor} 12%, transparent)`, color: accentColor }}
          >
            {icon}
          </div>
        )}
      </div>
    </div>
  );
}
