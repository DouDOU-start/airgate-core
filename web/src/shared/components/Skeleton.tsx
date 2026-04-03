interface SkeletonProps {
  variant?: 'text' | 'circle' | 'block';
  width?: string | number;
  height?: string | number;
  className?: string;
}

export function Skeleton({ variant = 'text', width, height, className = '' }: SkeletonProps) {
  const base = 'ag-shimmer rounded';

  if (variant === 'circle') {
    const size = width ?? 40;
    return (
      <div
        className={`${base} rounded-full flex-shrink-0 ${className}`}
        style={{ width: size, height: size }}
      />
    );
  }

  if (variant === 'block') {
    return (
      <div
        className={`${base} rounded-[10px] ${className}`}
        style={{ width: width ?? '100%', height: height ?? 80 }}
      />
    );
  }

  // text
  return (
    <div
      className={`${base} ${className}`}
      style={{ width: width ?? '100%', height: height ?? 14 }}
    />
  );
}

/** Row skeleton: avatar + two text lines */
export function SkeletonRow({ className = '' }: { className?: string }) {
  return (
    <div className={`flex items-center gap-3 ${className}`}>
      <Skeleton variant="circle" width={40} />
      <div className="flex-1 space-y-2">
        <Skeleton width={160} height={12} />
        <Skeleton width={100} height={10} />
      </div>
    </div>
  );
}
