import {
  type InputHTMLAttributes,
  type TextareaHTMLAttributes,
  forwardRef,
  type ReactNode,
  useState,
  useRef,
  useEffect,
  useCallback,
  useLayoutEffect,
} from 'react';
import { createPortal } from 'react-dom';

/* ==================== 共享样式 ==================== */

const inputBase =
  'block w-full rounded-md border border-glass-border bg-surface px-3 py-2 text-sm text-text placeholder-text-tertiary transition-all duration-200 focus:outline-none focus:border-border-focus focus:shadow-[0_0_0_3px_var(--ag-primary-subtle)] disabled:opacity-40 disabled:cursor-not-allowed';

const inputError =
  'border-danger focus:border-danger focus:shadow-[0_0_0_3px_var(--ag-danger-subtle)]';

/* ==================== Input ==================== */

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  error?: string;
  hint?: string;
  icon?: ReactNode;
}

export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ label, error, hint, icon, className = '', type, ...props }, ref) => {
    const [showPwd, setShowPwd] = useState(false);
    const isPassword = type === 'password';

    return (
      <div className="space-y-1.5">
        {label && (
          <label className="block text-xs font-medium text-text-secondary uppercase tracking-wider">
            {label}
            {props.required && <span className="text-danger ml-0.5">*</span>}
          </label>
        )}
        <div className="relative">
          {icon && (
            <div className="absolute left-3 top-0 bottom-0 flex items-center text-text pointer-events-none z-10">
              {icon}
            </div>
          )}
          <input
            ref={ref}
            type={isPassword && showPwd ? 'text' : type}
            className={`${inputBase} ${error ? inputError : ''} ${icon ? 'pl-10' : ''} ${isPassword ? 'pr-10' : ''} ${className}`}
            {...props}
          />
          {isPassword && (
            <button
              type="button"
              tabIndex={-1}
              onClick={() => setShowPwd((v) => !v)}
              className="absolute right-3 top-0 bottom-0 flex items-center text-text-tertiary hover:text-text transition-colors"
            >
              {showPwd ? (
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
                  <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
                  <line x1="1" y1="1" x2="23" y2="23" />
                </svg>
              ) : (
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
                  <circle cx="12" cy="12" r="3" />
                </svg>
              )}
            </button>
          )}
        </div>
        {error && <p className="text-xs text-danger">{error}</p>}
        {hint && !error && <p className="text-xs text-text-tertiary">{hint}</p>}
      </div>
    );
  },
);

Input.displayName = 'Input';

/* ==================== Textarea ==================== */

interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  label?: string;
  error?: string;
}

export function Textarea({ label, error, className = '', ...props }: TextareaProps) {
  return (
    <div className="space-y-1.5">
      {label && (
        <label className="block text-xs font-medium text-text-secondary uppercase tracking-wider">
          {label}
          {props.required && <span className="text-danger ml-0.5">*</span>}
        </label>
      )}
      <textarea
        className={`${inputBase} min-h-[80px] resize-y ${error ? inputError : ''} ${className}`}
        {...props}
      />
      {error && <p className="text-xs text-danger">{error}</p>}
    </div>
  );
}

/* ==================== Select ==================== */

interface SelectProps {
  label?: string;
  error?: string;
  options: Array<{ value: string; label: string }>;
  value?: string;
  onChange?: (e: { target: { value: string } }) => void;
  required?: boolean;
  disabled?: boolean;
  placeholder?: string;
  className?: string;
  name?: string;
  id?: string;
}

export function Select({
  label,
  error,
  options,
  value,
  onChange,
  required,
  disabled,
  placeholder,
  className = '',
  name,
  id,
}: SelectProps) {
  const [open, setOpen] = useState(false);
  const [focusIdx, setFocusIdx] = useState(-1);
  const containerRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const [dropdownPos, setDropdownPos] = useState({ top: 0, left: 0, width: 0, openUp: false });

  // Update dropdown position when open, flip upward if not enough space below
  useLayoutEffect(() => {
    if (!open || !buttonRef.current) return;
    const rect = buttonRef.current.getBoundingClientRect();
    const maxDropH = 240; // max-h-60 = 15rem = 240px
    const spaceBelow = window.innerHeight - rect.bottom;
    const openUp = spaceBelow < maxDropH && rect.top > spaceBelow;
    setDropdownPos({
      top: openUp ? rect.top : rect.bottom + 4,
      left: rect.left,
      width: rect.width,
      openUp,
    });
  }, [open]);

  const selectedOption = options.find((o) => o.value === value);
  const displayLabel = selectedOption?.label ?? placeholder ?? '';

  // Close on outside click
  useEffect(() => {
    if (!open) return;
    function handleClick(e: MouseEvent) {
      const target = e.target as Node;
      if (
        containerRef.current && !containerRef.current.contains(target) &&
        listRef.current && !listRef.current.contains(target)
      ) {
        setOpen(false);
      }
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [open]);

  // Scroll focused item into view
  useEffect(() => {
    if (!open || focusIdx < 0 || !listRef.current) return;
    const item = listRef.current.children[focusIdx] as HTMLElement | undefined;
    item?.scrollIntoView({ block: 'nearest' });
  }, [focusIdx, open]);

  const select = useCallback(
    (val: string) => {
      onChange?.({ target: { value: val } });
      setOpen(false);
    },
    [onChange],
  );

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (disabled) return;
      switch (e.key) {
        case 'Enter':
        case ' ':
          e.preventDefault();
          if (open && focusIdx >= 0) {
            select(options[focusIdx]!.value);
          } else {
            setOpen(true);
            setFocusIdx(options.findIndex((o) => o.value === value));
          }
          break;
        case 'ArrowDown':
          e.preventDefault();
          if (!open) {
            setOpen(true);
            setFocusIdx(options.findIndex((o) => o.value === value));
          } else {
            setFocusIdx((i) => (i < options.length - 1 ? i + 1 : 0));
          }
          break;
        case 'ArrowUp':
          e.preventDefault();
          if (open) {
            setFocusIdx((i) => (i > 0 ? i - 1 : options.length - 1));
          }
          break;
        case 'Escape':
          setOpen(false);
          break;
        case 'Tab':
          setOpen(false);
          break;
      }
    },
    [disabled, open, focusIdx, options, value, select],
  );

  return (
    <div className="space-y-1.5" ref={containerRef}>
      {label && (
        <label className="block text-xs font-medium text-text-secondary uppercase tracking-wider">
          {label}
          {required && <span className="text-danger ml-0.5">*</span>}
        </label>
      )}
      {/* Hidden native select for form submission */}
      <input type="hidden" name={name} value={value ?? ''} />
      <div className="relative">
        <button
          ref={buttonRef}
          type="button"
          id={id}
          role="combobox"
          aria-expanded={open}
          aria-haspopup="listbox"
          disabled={disabled}
          onClick={() => !disabled && setOpen((o) => !o)}
          onKeyDown={handleKeyDown}
          className={`${inputBase} cursor-pointer pr-10 text-left ${error ? inputError : ''} ${
            open ? 'shadow-[0_0_0_2px_var(--ag-primary-subtle)]' : ''
          } ${!selectedOption ? 'text-text-tertiary' : ''} ${className}`}
          style={open ? { borderColor: 'var(--ag-border-focus)' } : undefined}
        >
          {displayLabel || '\u00A0'}
        </button>
        {/* Chevron icon */}
        <div
          className={`pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 z-10 text-text transition-transform duration-200 ${
            open ? 'rotate-180' : ''
          }`}
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <path d="m6 9 6 6 6-6" />
          </svg>
        </div>
        {/* Dropdown panel — rendered via portal to avoid overflow clipping */}
        {open && createPortal(
          <ul
            ref={listRef}
            role="listbox"
            className="ag-glass-dropdown fixed z-[9999] max-h-60 overflow-auto rounded-lg py-1"
            style={{
              ...(dropdownPos.openUp
                ? { bottom: window.innerHeight - dropdownPos.top + 4 }
                : { top: dropdownPos.top }),
              left: dropdownPos.left,
              width: dropdownPos.width,
              animation: 'ag-scale-in 0.15s ease-out forwards',
            }}
          >
            {options.map((opt, idx) => {
              const isSelected = opt.value === value;
              const isFocused = idx === focusIdx;
              return (
                <li
                  key={opt.value}
                  role="option"
                  aria-selected={isSelected}
                  onMouseEnter={() => setFocusIdx(idx)}
                  onClick={() => select(opt.value)}
                  className={`flex items-center justify-between cursor-pointer px-3 py-2 text-sm transition-colors ${
                    isFocused ? 'bg-bg-hover text-text' : 'text-text-secondary'
                  } ${isSelected ? 'text-primary font-medium' : ''}`}
                >
                  <span>{opt.label}</span>
                  {isSelected && (
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" className="text-primary flex-shrink-0">
                      <path d="M20 6 9 17l-5-5" />
                    </svg>
                  )}
                </li>
              );
            })}
          </ul>,
          document.body,
        )}
      </div>
      {error && <p className="text-xs text-danger">{error}</p>}
    </div>
  );
}
