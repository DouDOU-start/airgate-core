import { useState, useEffect, useRef, type CSSProperties } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Checkbox, Input, Label, ListBox, Select, TextArea, TextField as HeroTextField } from '@heroui/react';
import { ChevronDown } from 'lucide-react';
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
      <HeroTextField fullWidth isDisabled={disabled} isRequired={field.required}>
        <Label>{field.label}</Label>
        <TextArea
          name={field.key}
          placeholder={hint}
          value={value}
          rows={3}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          required={field.required}
        />
      </HeroTextField>
    );
  }

  // text 和 password 都使用 Input
  // 密码字段使用 type="text" + CSS 遮蔽，避免浏览器检测到 password 字段自动填充
  const isPassword = field.type === 'password';
  return (
    <HeroTextField fullWidth isDisabled={disabled} isRequired={field.required}>
      <Label>{field.label}</Label>
      <Input
        name={field.key}
        type="text"
        placeholder={hint}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        autoComplete="off"
        required={field.required}
        style={isPassword ? { WebkitTextSecurity: 'disc', textSecurity: 'disc' } as CSSProperties : undefined}
      />
    </HeroTextField>
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
        className="text-xs font-medium uppercaser"
        style={{ color: 'var(--ag-text-secondary)' }}
      >
        {t('accounts.credentials')}
      </p>

      {accountTypes.length > 0 && mode === 'create' && (
        <>
          <Select
            fullWidth
            selectedKey={selectedType?.key ?? ''}
            onSelectionChange={(key) => onAccountTypeChange(key == null ? '' : String(key))}
          >
            <Label>{t('common.type')}</Label>
            <Select.Trigger>
              <Select.Value>{selectedType?.label ?? ''}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={accountTypes}>
                {(item) => (
                  <ListBox.Item id={item.key} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
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
      <Label
        className="block text-xs font-medium mb-1.5"
        style={{ color: 'var(--ag-text-secondary)' }}
      >
        {t('accounts.groups')}
      </Label>
      <Button
        type="button"
        onPress={() => setOpen(!open)}
        className="w-full justify-between"
        variant="outline"
      >
        <span className="truncate" style={selectedGroups.length === 0 ? { color: 'var(--ag-text-tertiary)' } : undefined}>
          {selectedGroups.length === 0
            ? t('accounts.select_groups')
            : selectedGroups.map((g) => g.name).join('、')}
        </span>
        <ChevronDown className={`w-4 h-4 flex-shrink-0 ml-2 transition-transform duration-200 ${open ? 'rotate-180' : ''}`} style={{ color: 'var(--ag-text-tertiary)' }} />
      </Button>
      {open && (
        <div
          className="absolute z-50 mt-1 w-full rounded-lg shadow-lg max-h-48 overflow-y-auto py-1"
          style={{ borderWidth: '1px', borderStyle: 'solid', borderColor: 'var(--ag-glass-border)', background: 'var(--ag-bg-elevated)' }}
        >
          {groups.map((g) => (
            <Checkbox
              key={g.id}
              className="w-full px-3 py-2"
              isSelected={selectedIds.includes(g.id)}
              onChange={() => toggle(g.id)}
            >
              <span className="inline-flex items-center gap-2">
                <span>{g.name}</span>
                <span className="text-[10px]" style={{ color: 'var(--ag-text-tertiary)' }}>{g.platform}</span>
              </span>
            </Checkbox>
          ))}
        </div>
      )}
    </div>
  );
}
