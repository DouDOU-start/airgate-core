import { type CSSProperties } from 'react';
import { useTranslation } from 'react-i18next';
import { Checkbox, Input, Label, ListBox, Popover, Select, TextArea, TextField as HeroTextField } from '@heroui/react';
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
  showLabel = true,
  selectedIds,
  onChange,
}: {
  groups: { id: number; name: string; platform: string }[];
  showLabel?: boolean;
  selectedIds: number[];
  onChange: (ids: number[]) => void;
}) {
  const { t } = useTranslation();

  if (groups.length === 0) return null;

  const selectedGroups = groups.filter((g) => selectedIds.includes(g.id));
  const selectedLabel = selectedGroups.length === 0
    ? t('accounts.select_groups')
    : selectedGroups.map((g) => g.name).join('、');

  return (
    <div className="select select--full-width">
      {showLabel ? (
        <Label>{t('accounts.groups')}</Label>
      ) : null}
      <Popover>
        <Popover.Trigger
          aria-label={t('accounts.groups')}
          className="select__trigger select__trigger--full-width"
        >
          <span className="select__value">
            <span className={selectedGroups.length === 0 ? 'block min-w-0 truncate text-text-tertiary' : 'block min-w-0 truncate'}>
              {selectedLabel}
            </span>
          </span>
          <ChevronDown className="select__indicator h-4 w-4" />
        </Popover.Trigger>
        <Popover.Content className="select__popover max-h-64 overflow-y-auto" placement="bottom">
          <Popover.Dialog className="p-0">
            <ListBox
              aria-label={t('accounts.groups')}
              items={groups.map((g) => ({ id: String(g.id), name: g.name, platform: g.platform }))}
              selectedKeys={selectedIds.map(String)}
              selectionMode="multiple"
              onSelectionChange={(keys) => {
                if (keys === 'all') {
                  onChange(groups.map((g) => g.id));
                  return;
                }
                onChange(
                  Array.from(keys)
                    .map((key) => Number(key))
                    .filter((id) => Number.isFinite(id)),
                );
              }}
            >
              {(g) => (
                <ListBox.Item id={g.id} textValue={`${g.name} ${g.platform}`}>
                  {({ isSelected }) => (
                    <span className="flex min-w-0 flex-1 items-center gap-2">
                      <Checkbox
                        aria-hidden="true"
                        className="pointer-events-none shrink-0"
                        isSelected={isSelected}
                      >
                        <Checkbox.Control className={isSelected ? 'border-success bg-success text-success-foreground' : undefined}>
                          <Checkbox.Indicator />
                        </Checkbox.Control>
                      </Checkbox>
                      <span className="min-w-0 truncate text-sm text-text">{g.name}</span>
                      <span className="shrink-0 rounded border border-border-subtle px-1.5 py-0 text-[10px] leading-4 text-text-tertiary">
                        {g.platform}
                      </span>
                    </span>
                  )}
                </ListBox.Item>
              )}
            </ListBox>
          </Popover.Dialog>
        </Popover.Content>
      </Popover>
    </div>
  );
}
