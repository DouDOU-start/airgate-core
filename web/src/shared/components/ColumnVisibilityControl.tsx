import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Dropdown } from '@heroui/react';
import { Check, Columns3 } from 'lucide-react';

export interface ColumnVisibilityItem {
  key: string;
  label: ReactNode;
  /** 锁定列始终显示、不可隐藏（如吸附在左侧的身份列） */
  locked?: boolean;
}

/** 表格列显隐下拉：多选模式，勾选即显示；锁定列置灰不可操作。 */
export function ColumnVisibilityControl({
  hiddenKeys,
  items,
  onChange,
}: {
  hiddenKeys: ReadonlySet<string>;
  items: ColumnVisibilityItem[];
  onChange: (hiddenKeys: string[]) => void;
}) {
  const { t } = useTranslation();
  const label = t('usage.columns');
  const visibleKeys = new Set(
    items.filter((item) => item.locked || !hiddenKeys.has(item.key)).map((item) => item.key),
  );

  return (
    <Dropdown>
      <Dropdown.Trigger className="button button--sm button--ghost h-8 shrink-0 gap-1.5 whitespace-nowrap px-3">
        <Columns3 className="h-3.5 w-3.5 shrink-0" />
        <span>{label}</span>
      </Dropdown.Trigger>
      <Dropdown.Popover placement="bottom end">
        <Dropdown.Menu
          aria-label={label}
          selectedKeys={visibleKeys}
          selectionMode="multiple"
          onSelectionChange={(selection) => {
            if (selection === 'all') {
              onChange([]);
              return;
            }
            const selected = new Set([...selection].map(String));
            onChange(
              items
                .filter((item) => !item.locked && !selected.has(item.key))
                .map((item) => item.key),
            );
          }}
        >
          {items.map((item) => (
            <Dropdown.Item
              key={item.key}
              id={item.key}
              isDisabled={item.locked}
              textValue={typeof item.label === 'string' ? item.label : item.key}
            >
              <span className="flex items-center justify-between gap-6">
                <span>{item.label}</span>
                {visibleKeys.has(item.key) ? <Check className="h-3.5 w-3.5 text-primary" /> : null}
              </span>
            </Dropdown.Item>
          ))}
        </Dropdown.Menu>
      </Dropdown.Popover>
    </Dropdown>
  );
}
