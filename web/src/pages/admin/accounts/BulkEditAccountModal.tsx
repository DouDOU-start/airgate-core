import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Button, Checkbox, Input, Label, ListBox, Modal, Select, Switch, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { Hash, Gauge } from 'lucide-react';
import { groupsApi } from '../../../shared/api/groups';
import { proxiesApi } from '../../../shared/api/proxies';
import { queryKeys } from '../../../shared/queryKeys';
import { FETCH_ALL_PARAMS } from '../../../shared/constants';
import { GroupCheckboxList } from './CredentialForm';
import type { BulkUpdateAccountsReq } from '../../../shared/types';

/**
 * 批量编辑弹窗：每个字段前有「启用」开关，只有启用的字段会进入 patch。
 * 分组为追加模式（add_group_ids）：新勾选的分组会并到账号已有分组中，不会移除。
 */
export function BulkEditAccountModal({
  open,
  count,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  count: number;
  onClose: () => void;
  onSubmit: (data: Omit<BulkUpdateAccountsReq, 'account_ids'>) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();

  // 每个字段独立的「启用」开关
  const [enableStatus, setEnableStatus] = useState(false);
  const [enablePriority, setEnablePriority] = useState(false);
  const [enableConcurrency, setEnableConcurrency] = useState(false);
  const [enableRateMultiplier, setEnableRateMultiplier] = useState(false);
  const [enableGroups, setEnableGroups] = useState(false);
  const [enableProxy, setEnableProxy] = useState(false);

  // 字段值
  const [status, setStatus] = useState<'active' | 'disabled'>('active');
  const [priority, setPriority] = useState(50);
  const [maxConcurrency, setMaxConcurrency] = useState(5);
  const [rateMultiplier, setRateMultiplier] = useState(1);
  const [groupIds, setGroupIds] = useState<number[]>([]);
  const [proxyId, setProxyId] = useState<number | null>(null);

  const { data: groupsData } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
  });

  const { data: proxiesData } = useQuery({
    queryKey: queryKeys.proxiesAll(),
    queryFn: () => proxiesApi.list(FETCH_ALL_PARAMS),
  });

  const hasAnyField =
    enableStatus ||
    enablePriority ||
    enableConcurrency ||
    enableRateMultiplier ||
    enableGroups ||
    enableProxy;

  const handleSubmit = () => {
    const patch: Omit<BulkUpdateAccountsReq, 'account_ids'> = {};
    if (enableStatus) patch.state = status;
    if (enablePriority) patch.priority = priority;
    if (enableConcurrency) patch.max_concurrency = maxConcurrency;
    if (enableRateMultiplier) patch.rate_multiplier = rateMultiplier;
    if (enableGroups) patch.group_ids = groupIds;
    if (enableProxy && proxyId != null) patch.proxy_id = proxyId;
    onSubmit(patch);
  };
  const proxyOptions = [
    { id: '', label: t('accounts.select_proxy') },
    ...(proxiesData?.list ?? []).map((p) => ({
      id: String(p.id),
      label: `${p.name} (${p.protocol}://${p.address}:${p.port})`,
    })),
  ];
  const selectedProxyLabel =
    proxyOptions.find((item) => item.id === (proxyId == null ? '' : String(proxyId)))?.label ?? t('accounts.select_proxy');
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  return (
    <Modal state={modalState}>
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '560px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{`${t('accounts.bulk_update_title')} (${count})`}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
        <div
          className="text-xs px-3 py-2 rounded"
          style={{
            background: 'var(--ag-bg-surface)',
            border: '1px solid var(--ag-glass-border)',
            color: 'var(--ag-text-secondary)',
          }}
        >
          {t('accounts.bulk_update_hint')}
        </div>

        {/* 调度状态 */}
        <FieldRow
          enabled={enableStatus}
          onToggle={setEnableStatus}
          label={t('accounts.enable_dispatch')}
        >
          <Switch
            isSelected={status === 'active'}
            onChange={(on) => setStatus(on ? 'active' : 'disabled')}
          >
            <Switch.Control>
              <Switch.Thumb />
            </Switch.Control>
          </Switch>
        </FieldRow>

        {/* 优先级 */}
        <FieldRow
          enabled={enablePriority}
          onToggle={setEnablePriority}
          label={t('accounts.priority')}
        >
          <HeroTextField fullWidth isDisabled={!enablePriority}>
            <div className="relative">
              <Hash className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
              <Input
                className="pl-9"
                type="number"
                min={0}
                max={999}
                step={1}
                value={String(priority)}
                disabled={!enablePriority}
                onChange={(e) => {
                  const v = Math.round(Number(e.target.value));
                  setPriority(Math.max(0, Math.min(999, v)));
                }}
              />
            </div>
          </HeroTextField>
        </FieldRow>

        {/* 并发数 */}
        <FieldRow
          enabled={enableConcurrency}
          onToggle={setEnableConcurrency}
          label={t('accounts.concurrency')}
        >
          <HeroTextField fullWidth isDisabled={!enableConcurrency}>
            <div className="relative">
              <Gauge className="pointer-events-none absolute left-3 top-1/2 z-10 w-4 h-4 -translate-y-1/2 text-text-tertiary" />
              <Input
                className="pl-9"
                type="number"
                value={String(maxConcurrency)}
                disabled={!enableConcurrency}
                onChange={(e) => setMaxConcurrency(Number(e.target.value))}
              />
            </div>
          </HeroTextField>
        </FieldRow>

        {/* 费率倍率 */}
        <FieldRow
          enabled={enableRateMultiplier}
          onToggle={setEnableRateMultiplier}
          label={t('accounts.rate_multiplier')}
        >
          <HeroTextField fullWidth isDisabled={!enableRateMultiplier}>
            <Input
              type="number"
              step="0.1"
              value={String(rateMultiplier)}
              disabled={!enableRateMultiplier}
              onChange={(e) => setRateMultiplier(Number(e.target.value))}
            />
          </HeroTextField>
        </FieldRow>

        {/* 所属分组（直接替换） */}
        <FieldRow
          enabled={enableGroups}
          onToggle={setEnableGroups}
          label={t('accounts.groups')}
        >
          {enableGroups && (
            <GroupCheckboxList
              groups={groupsData?.list ?? []}
              selectedIds={groupIds}
              onChange={setGroupIds}
            />
          )}
        </FieldRow>

        {/* 代理 */}
        <FieldRow
          enabled={enableProxy}
          onToggle={setEnableProxy}
          label={t('accounts.proxy')}
        >
          <Select
            fullWidth
            selectedKey={proxyId == null ? '' : String(proxyId)}
            isDisabled={!enableProxy}
            onSelectionChange={(key) => setProxyId(key ? Number(key) : null)}
          >
            <Label>{t('accounts.proxy')}</Label>
            <Select.Trigger>
              <Select.Value>{selectedProxyLabel}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={proxyOptions}>
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        </FieldRow>
              </div>
            </Modal.Body>
            <Modal.Footer>
              <div className="flex justify-end gap-2 w-full">
                <Button variant="secondary" onPress={onClose}>
                  {t('common.cancel')}
                </Button>
                <Button variant="primary" onPress={handleSubmit} isDisabled={loading || !hasAnyField} aria-busy={loading}>
                  {t('common.save')}
                </Button>
              </div>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}

function FieldRow({
  enabled,
  onToggle,
  label,
  children,
}: {
  enabled: boolean;
  onToggle: (on: boolean) => void;
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div
      className="flex items-start gap-3 py-2"
      style={{ borderTop: '1px solid var(--ag-border-subtle)' }}
    >
      <Checkbox
        className="shrink-0 pt-2"
        style={{ minWidth: 120 }}
        isSelected={enabled}
        onChange={onToggle}
      >
        <span
          className="text-sm"
          style={{ color: enabled ? 'var(--ag-text)' : 'var(--ag-text-tertiary)' }}
        >
          {label}
        </span>
      </Checkbox>
      <div className="flex-1 min-w-0">{children}</div>
    </div>
  );
}
