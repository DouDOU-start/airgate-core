import { useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Input } from '@heroui/react';
import { Plus, X } from 'lucide-react';

// ==================== 标签输入（models / tags 共用） ====================

export function TagInput({
  ariaLabel,
  placeholder,
  value,
  onChange,
}: {
  ariaLabel: string;
  placeholder?: string;
  value: string[];
  onChange: (next: string[]) => void;
}) {
  const [draft, setDraft] = useState('');

  function commit(raw: string) {
    // 支持一次粘贴多个（逗号/换行/空白分隔），去重后追加
    const parts = raw.split(/[\n,]+/).map((item) => item.trim()).filter(Boolean);
    if (parts.length === 0) return;
    const next = [...value];
    for (const part of parts) {
      if (!next.includes(part)) next.push(part);
    }
    onChange(next);
    setDraft('');
  }

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'Enter' || event.key === ',') {
      event.preventDefault();
      commit(draft);
      return;
    }
    if (event.key === 'Backspace' && draft === '' && value.length > 0) {
      onChange(value.slice(0, -1));
    }
  }

  return (
    <div className="flex min-h-10 flex-wrap items-center gap-1.5 rounded-[var(--field-radius)] border border-border bg-transparent px-2.5 py-1.5">
      {value.map((tag) => (
        <span
          key={tag}
          className="inline-flex items-center gap-1 rounded-md bg-accent-soft px-1.5 py-0.5 font-mono text-xs text-accent-soft-foreground"
        >
          <span className="max-w-[240px] truncate" title={tag}>{tag}</span>
          <button
            aria-label={`remove ${tag}`}
            className="shrink-0 opacity-70 hover:opacity-100"
            type="button"
            onClick={() => onChange(value.filter((item) => item !== tag))}
          >
            <X className="h-3 w-3" />
          </button>
        </span>
      ))}
      <input
        aria-label={ariaLabel}
        className="min-w-[160px] flex-1 bg-transparent py-0.5 text-sm text-text outline-none placeholder:text-text-tertiary"
        placeholder={placeholder}
        value={draft}
        onBlur={() => commit(draft)}
        onChange={(event) => setDraft(event.target.value)}
        onKeyDown={handleKeyDown}
      />
    </div>
  );
}

// ==================== 键值对行编辑器（模型映射 / 参数覆写 / Header 覆写共用） ====================

export interface KVRow {
  key: string;
  value: string;
}

// kvRowsToRecord 行 → 对象：键去首尾空白，空键行忽略；重复键后者覆盖前者。
export function kvRowsToRecord(rows: KVRow[], mapValue: (raw: string) => unknown = (raw) => raw): Record<string, unknown> {
  const result: Record<string, unknown> = {};
  for (const row of rows) {
    const key = row.key.trim();
    if (!key) continue;
    result[key] = mapValue(row.value);
  }
  return result;
}

// recordToKVRows 对象 → 行：非字符串值序列化为 JSON 文本回显。
export function recordToKVRows(record: Record<string, unknown> | null | undefined): KVRow[] {
  return Object.entries(record ?? {}).map(([key, value]) => ({
    key,
    value: typeof value === 'string' ? value : JSON.stringify(value),
  }));
}

export function KeyValueEditor({
  ariaLabel,
  keyPlaceholder,
  valuePlaceholder,
  rows,
  onChange,
}: {
  ariaLabel: string;
  keyPlaceholder: string;
  valuePlaceholder: string;
  rows: KVRow[];
  onChange: (next: KVRow[]) => void;
}) {
  const { t } = useTranslation();

  function updateRow(index: number, patch: Partial<KVRow>) {
    onChange(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  }

  return (
    <div className="space-y-1.5">
      {rows.map((row, index) => (
        // 行无稳定业务主键，索引即身份（增删只在尾部/原位），用 index 作 key 可接受
        <div key={index} className="flex items-center gap-2">
          <Input
            aria-label={`${ariaLabel} key`}
            className="flex-1 font-mono text-xs"
            placeholder={keyPlaceholder}
            value={row.key}
            onChange={(event) => updateRow(index, { key: event.target.value })}
          />
          <Input
            aria-label={`${ariaLabel} value`}
            className="flex-1 font-mono text-xs"
            placeholder={valuePlaceholder}
            value={row.value}
            onChange={(event) => updateRow(index, { value: event.target.value })}
          />
          <Button
            isIconOnly
            aria-label={t('common.delete')}
            size="sm"
            variant="ghost"
            onPress={() => onChange(rows.filter((_, i) => i !== index))}
          >
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>
      ))}
      <Button size="sm" variant="secondary" onPress={() => onChange([...rows, { key: '', value: '' }])}>
        <Plus className="h-3.5 w-3.5" />
        {t('channels.add_row')}
      </Button>
    </div>
  );
}
