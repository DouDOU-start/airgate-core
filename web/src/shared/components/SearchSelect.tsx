import { useState, useRef, useEffect, useCallback, type ReactNode } from 'react';
import { X, ChevronDown, Loader2, Search } from 'lucide-react';
import { inputBase } from './Input';

export interface SearchSelectOption {
  value: string;
  label: string;
  description?: string;
  /** 渲染在选项右侧（如倍率标签），支持 ReactNode 用于富文本样式 */
  suffix?: ReactNode;
}

interface SearchSelectProps {
  label?: string;
  required?: boolean;
  placeholder?: string;
  value?: string;
  onChange: (value: string) => void;
  /** 异步搜索回调；省略时使用本地 label 过滤 */
  onSearch?: (keyword: string) => void;
  options: SearchSelectOption[];
  loading?: boolean;
  disabled?: boolean;
  className?: string;
}

export function SearchSelect({
  label: fieldLabel,
  required,
  placeholder,
  value,
  onChange,
  onSearch,
  options,
  loading = false,
  disabled = false,
  className = '',
}: SearchSelectProps) {
  const [open, setOpen] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [focusIdx, setFocusIdx] = useState(-1);
  const containerRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout>>(undefined);

  const selectedOption = options.find((o) => o.value === value);

  // 点击外部关闭
  useEffect(() => {
    if (!open) return;
    function handleClick(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
        setKeyword('');
      }
    }
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [open]);

  // 滚动聚焦项到可见区域
  useEffect(() => {
    if (!open || focusIdx < 0 || !listRef.current) return;
    const item = listRef.current.children[focusIdx] as HTMLElement | undefined;
    item?.scrollIntoView({ block: 'nearest' });
  }, [focusIdx, open]);

  // 本地过滤（onSearch 未提供时）
  const filteredOptions = onSearch
    ? options
    : options.filter((o) => !keyword || o.label.toLowerCase().includes(keyword.toLowerCase()));

  // 防抖搜索 / 本地过滤
  const handleInput = useCallback(
    (val: string) => {
      setKeyword(val);
      setFocusIdx(-1);
      if (onSearch) {
        clearTimeout(debounceRef.current);
        debounceRef.current = setTimeout(() => onSearch(val), 300);
      }
    },
    [onSearch],
  );

  useEffect(() => {
    return () => clearTimeout(debounceRef.current);
  }, []);

  const select = useCallback(
    (val: string) => {
      onChange(val);
      setOpen(false);
      setKeyword('');
    },
    [onChange],
  );

  const clear = useCallback(() => {
    onChange('');
    setKeyword('');
    onSearch?.('');
  }, [onChange, onSearch]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      switch (e.key) {
        case 'ArrowDown':
          e.preventDefault();
          if (!open) {
            setOpen(true);
          } else {
            setFocusIdx((i) => (i < filteredOptions.length - 1 ? i + 1 : 0));
          }
          break;
        case 'ArrowUp':
          e.preventDefault();
          if (open) {
            setFocusIdx((i) => (i > 0 ? i - 1 : filteredOptions.length - 1));
          }
          break;
        case 'Enter':
          e.preventDefault();
          if (open && focusIdx >= 0 && filteredOptions[focusIdx]) {
            select(filteredOptions[focusIdx].value);
          }
          break;
        case 'Escape':
          setOpen(false);
          setKeyword('');
          break;
      }
    },
    [open, focusIdx, filteredOptions, select],
  );

  const dropdown = (
    <div ref={containerRef} className={`relative ${className}`}>
      <div className="relative">
        {/* 已选中：显示标签 */}
        {value && selectedOption && !open ? (
          <button
            type="button"
            disabled={disabled}
            onClick={() => { setOpen(true); setTimeout(() => inputRef.current?.focus(), 0); }}
            className={`${inputBase} cursor-pointer pr-16 text-left truncate`}
          >
            {selectedOption.label}
          </button>
        ) : (
          <input
            ref={inputRef}
            type="text"
            disabled={disabled}
            placeholder={placeholder}
            value={keyword}
            onChange={(e) => handleInput(e.target.value)}
            onFocus={() => setOpen(true)}
            onKeyDown={handleKeyDown}
            className={`${inputBase} pr-16 ${open ? 'border-[var(--ag-border-focus)] shadow-[0_0_0_3px_var(--ag-primary-subtle)]' : ''}`}
          />
        )}
        {/* 右侧图标 */}
        <div className="absolute right-2 top-0 bottom-0 flex items-center gap-1">
          {loading && <Loader2 className="w-3.5 h-3.5 text-text-tertiary animate-spin" />}
          {value && (
            <button type="button" onClick={clear} className="p-0.5 rounded hover:bg-bg-hover text-text-tertiary hover:text-text cursor-pointer">
              <X className="w-3.5 h-3.5" />
            </button>
          )}
          <ChevronDown className={`w-3.5 h-3.5 text-text-tertiary transition-transform ${open ? 'rotate-180' : ''}`} />
        </div>
      </div>
      {/* 下拉列表 */}
      {open && (
        <ul
          ref={listRef}
          role="listbox"
          className="ag-glass-dropdown absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-[10px] py-1"
          style={{ animation: 'ag-scale-in 0.15s ease-out forwards', minWidth: '220px' }}
        >
          {/* 本地过滤搜索框 */}
          {!onSearch && filteredOptions.length > 0 && (
            <li className="px-2 pb-1">
              <div className="relative">
                <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-text-tertiary" />
                <input
                  ref={inputRef}
                  type="text"
                  value={keyword}
                  onChange={(e) => handleInput(e.target.value)}
                  placeholder={placeholder ?? '搜索...'}
                  className="w-full pl-8 pr-2 py-1.5 text-xs bg-transparent border-b border-border outline-none text-text placeholder:text-text-tertiary"
                  autoFocus
                />
              </div>
            </li>
          )}
          {filteredOptions.length === 0 && !loading && (
            <li className="px-3 py-2 text-xs text-text-tertiary text-center">
              {keyword ? '无匹配结果' : '输入关键词搜索'}
            </li>
          )}
          {filteredOptions.map((opt, idx) => {
            const isFocused = idx === focusIdx;
            const isSelected = opt.value === value;
            return (
              <li
                key={opt.value}
                role="option"
                aria-selected={isSelected}
                onMouseEnter={() => setFocusIdx(idx)}
                onClick={() => select(opt.value)}
                className={`cursor-pointer px-3 py-2 text-sm transition-colors ${
                  isFocused ? 'bg-bg-hover text-text' : 'text-text-secondary'
                } ${isSelected ? 'text-primary font-medium' : ''}`}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="truncate">{opt.label}</span>
                  {opt.suffix && <span className="shrink-0 text-xs">{opt.suffix}</span>}
                </div>
                {opt.description && (
                  <div className="text-xs text-text-tertiary truncate">{opt.description}</div>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );

  if (!fieldLabel) return dropdown;

  return (
    <div>
      <label className="block text-xs font-medium text-text-secondary mb-1.5">
        {fieldLabel}
        {required && <span className="text-danger ml-0.5">*</span>}
      </label>
      {dropdown}
    </div>
  );
}
