import { useState, useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown } from 'lucide-react';
import { Input, Textarea, Select } from '../../../shared/components/Input';
import {
  getSchemaAccountTypes,
  getSchemaSelectedAccountType,
  getSchemaVisibleFields,
} from './accountUtils';
import type { CredentialField, CredentialSchemaResp } from '../../../shared/types';

// ==================== 凭证字段渲染 ====================

export function CredentialFieldInput({
  field,
  value,
  onChange,
  disabled,
  placeholder,
}: {
  field: CredentialField;
  value: string;
  onChange: (val: string) => void;
  disabled?: boolean;
  placeholder?: string;
}) {
  const hint = placeholder ?? field.placeholder;

  if (field.type === 'textarea') {
    return (
      <Textarea
        label={field.label}
        required={field.required}
        placeholder={hint}
        value={value}
        rows={3}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
      />
    );
  }

  // text 和 password 都使用 Input
  // 密码字段使用 type="text" + CSS 遮蔽，避免浏览器检测到 password 字段自动填充
  const isPassword = field.type === 'password';
  return (
    <Input
      label={field.label}
      type="text"
      required={field.required}
      placeholder={hint}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={disabled}
      autoComplete="off"
      style={isPassword ? { WebkitTextSecurity: 'disc', textSecurity: 'disc' } as React.CSSProperties : undefined}
    />
  );
}

export function SchemaCredentialsForm({
  schema,
  accountType,
  onAccountTypeChange,
  credentials,
  onCredentialsChange,
  mode = 'create',
}: {
  schema: CredentialSchemaResp;
  accountType: string;
  onAccountTypeChange: (type: string) => void;
  credentials: Record<string, string>;
  onCredentialsChange: (credentials: Record<string, string>) => void;
  mode?: 'create' | 'edit';
}) {
  const { t } = useTranslation();
  const accountTypes = getSchemaAccountTypes(schema);
  const selectedType = getSchemaSelectedAccountType(schema, accountType);
  const visibleFields = getSchemaVisibleFields(schema, accountType);

  return (
    <div
      className="space-y-4 pt-4"
      style={{ borderTop: '1px solid var(--ag-border)' }}
    >
      <p
        className="text-xs font-medium uppercase tracking-wider"
        style={{ color: 'var(--ag-text-secondary)' }}
      >
        {t('accounts.credentials')}
      </p>

      {accountTypes.length > 0 && mode === 'create' && (
        <>
          <Select
            label={t('common.type')}
            value={selectedType?.key ?? ''}
            onChange={(e) => onAccountTypeChange(e.target.value)}
            options={accountTypes.map((item) => ({
              value: item.key,
              label: item.label,
            }))}
          />
          {selectedType?.description && (
            <p className="text-xs text-text-tertiary -mt-2">
              {selectedType.description}
            </p>
          )}
        </>
      )}

      {visibleFields
        .filter((field) => !(mode === 'edit' && field.edit_disabled))
        .map((field) => (
          <CredentialFieldInput
            key={field.key}
            field={field}
            value={credentials[field.key] ?? ''}
            onChange={(val) =>
              onCredentialsChange({ ...credentials, [field.key]: val })
            }
            placeholder={mode === 'edit' && field.type === 'password' ? t('accounts.leave_empty_to_keep') : undefined}
          />
        ))}
    </div>
  );
}

// ==================== 分组多选 ====================

export function GroupCheckboxList({
  groups,
  selectedIds,
  onChange,
}: {
  groups: { id: number; name: string; platform: string }[];
  selectedIds: number[];
  onChange: (ids: number[]) => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, []);

  if (groups.length === 0) return null;

  const toggle = (id: number) => {
    onChange(
      selectedIds.includes(id)
        ? selectedIds.filter((v) => v !== id)
        : [...selectedIds, id],
    );
  };

  const selectedGroups = groups.filter((g) => selectedIds.includes(g.id));

  return (
    <div ref={ref} className="relative">
      <label
        className="block text-xs font-medium mb-1.5"
        style={{ color: 'var(--ag-text-secondary)' }}
      >
        {t('accounts.groups')}
      </label>
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="flex items-center justify-between w-full px-3 py-2 rounded-lg border text-sm text-left transition-colors"
        style={{ borderColor: 'var(--ag-glass-border)', background: 'var(--ag-bg-surface)', color: 'var(--ag-text)' }}
      >
        <span className="truncate" style={selectedGroups.length === 0 ? { color: 'var(--ag-text-tertiary)' } : undefined}>
          {selectedGroups.length === 0
            ? t('accounts.select_groups')
            : selectedGroups.map((g) => g.name).join('、')}
        </span>
        <ChevronDown className={`w-4 h-4 flex-shrink-0 ml-2 transition-transform duration-200 ${open ? 'rotate-180' : ''}`} style={{ color: 'var(--ag-text-tertiary)' }} />
      </button>
      {open && (
        <div
          className="absolute z-50 mt-1 w-full rounded-lg border shadow-lg max-h-48 overflow-y-auto py-1"
          style={{ borderColor: 'var(--ag-glass-border)', background: 'var(--ag-bg-elevated)' }}
        >
          {groups.map((g) => (
            <button
              key={g.id}
              type="button"
              onClick={() => toggle(g.id)}
              className="flex items-center gap-2 w-full px-3 py-2 text-sm hover:bg-bg-hover transition-colors text-left"
              style={{ color: 'var(--ag-text)' }}
            >
              <span
                className="flex items-center justify-center w-4 h-4 rounded border flex-shrink-0 transition-colors"
                style={{
                  borderColor: selectedIds.includes(g.id) ? 'var(--ag-primary)' : 'var(--ag-glass-border)',
                  background: selectedIds.includes(g.id) ? 'var(--ag-primary)' : 'transparent',
                }}
              >
                {selectedIds.includes(g.id) && (
                  <svg className="w-3 h-3 text-white" viewBox="0 0 12 12" fill="none"><path d="M2.5 6l2.5 2.5 4.5-5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
                )}
              </span>
              <span>{g.name}</span>
              <span className="text-[10px]" style={{ color: 'var(--ag-text-tertiary)' }}>{g.platform}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
