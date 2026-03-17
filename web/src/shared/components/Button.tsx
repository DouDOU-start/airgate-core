import { type ButtonHTMLAttributes, type ReactNode } from 'react';
import { Loader2 } from 'lucide-react';

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost' | 'outline';
  size?: 'sm' | 'md' | 'lg';
  loading?: boolean;
  icon?: ReactNode;
}

const variantStyles: Record<string, string> = {
  primary:
    'bg-primary text-text-inverse hover:bg-primary-hover shadow-md hover:shadow-lg',
  secondary:
    'bg-surface text-text border border-glass-border hover:bg-bg-hover hover:border-border',
  danger:
    'bg-danger text-white hover:brightness-110',
  ghost:
    'text-text-tertiary hover:text-text hover:bg-bg-hover',
  outline:
    'border border-primary/40 text-primary bg-transparent hover:bg-primary-subtle hover:border-primary/60',
};

const sizeStyles: Record<string, string> = {
  sm: 'h-8 px-3 text-xs gap-1.5',
  md: 'h-9 px-4 text-sm gap-2',
  lg: 'h-11 px-6 text-sm gap-2',
};

export function Button({
  variant = 'primary',
  size = 'md',
  loading = false,
  icon,
  children,
  disabled,
  className = '',
  ...props
}: ButtonProps) {
  return (
    <button
      className={`inline-flex items-center justify-center rounded-lg font-medium transition-all duration-200 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed disabled:shadow-none ${variantStyles[variant]} ${sizeStyles[size]} ${className}`}
      disabled={disabled || loading}
      {...props}
    >
      {loading ? (
        <span className="flex-shrink-0">
          <Loader2 className="h-4 w-4 animate-spin" />
        </span>
      ) : icon ? (
        <span className="flex-shrink-0">{icon}</span>
      ) : null}
      <span>{children}</span>
    </button>
  );
}
