import { type ReactNode } from 'react';
import { getAvatarColor } from '../utils/avatar';

type AvatarSize = 'xs' | 'sm' | 'md' | 'lg';

interface AvatarProps {
  name?: string;
  src?: string;
  size?: AvatarSize;
  className?: string;
}

const sizeMap: Record<AvatarSize, { container: string; text: string }> = {
  xs: { container: 'w-6 h-6', text: 'text-[9px]' },
  sm: { container: 'w-7 h-7', text: 'text-[10px]' },
  md: { container: 'w-9 h-9', text: 'text-xs' },
  lg: { container: 'w-12 h-12', text: 'text-sm' },
};

export function Avatar({ name, src, size = 'md', className = '' }: AvatarProps) {
  const s = sizeMap[size];
  const initial = name?.charAt(0).toUpperCase() || '?';
  const bgColor = name ? getAvatarColor(name) : 'var(--ag-text-tertiary)';

  if (src) {
    return (
      <img
        src={src}
        alt={name || ''}
        className={`${s.container} rounded-full object-cover flex-shrink-0 ${className}`}
      />
    );
  }

  return (
    <div
      className={`${s.container} rounded-full flex items-center justify-center font-bold text-white flex-shrink-0 ${className}`}
      style={{ backgroundColor: bgColor }}
    >
      <span className={s.text}>{initial}</span>
    </div>
  );
}

interface AvatarGroupProps {
  max?: number;
  children: ReactNode[];
}

export function AvatarGroup({ max = 4, children }: AvatarGroupProps) {
  const visible = children.slice(0, max);
  const overflow = children.length - max;

  return (
    <div className="flex items-center -space-x-2.5">
      {visible.map((child, i) => (
        <div key={i} className="ring-2 ring-bg rounded-full">
          {child}
        </div>
      ))}
      {overflow > 0 && (
        <div className="w-9 h-9 rounded-full bg-bg-active flex items-center justify-center text-[10px] font-bold text-text-secondary ring-2 ring-bg">
          +{overflow}
        </div>
      )}
    </div>
  );
}
