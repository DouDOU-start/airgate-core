import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Button, Chip, Description, Input, Label, ListBox, Modal, Select, Spinner, Switch, TextArea, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { ArrowUpDown, Layers, X } from 'lucide-react';
import { groupsApi } from '../../../shared/api/groups';
import type { GroupResp, CreateGroupReq, UpdateGroupReq } from '../../../shared/types';

function parseQuotas(quotas?: Record<string, unknown>): { daily: string; weekly: string; monthly: string } {
  return {
    daily: quotas?.daily ? String(quotas.daily) : '',
    monthly: quotas?.monthly ? String(quotas.monthly) : '',
    weekly: quotas?.weekly ? String(quotas.weekly) : '',
  };
}

function buildQuotas(q: { daily: string; weekly: string; monthly: string }): Record<string, unknown> | undefined {
  const result: Record<string, number> = {};
  if (q.daily && Number(q.daily) > 0) result.daily = Number(q.daily);
  if (q.weekly && Number(q.weekly) > 0) result.weekly = Number(q.weekly);
  if (q.monthly && Number(q.monthly) > 0) result.monthly = Number(q.monthly);
  return Object.keys(result).length > 0 ? result : undefined;
}

export function GroupFormModal({
  open,
  title,
  group,
  onClose,
  onSubmit,
  loading,
  platforms,
  instructionPresets,
}: {
  open: boolean;
  title: string;
  group?: GroupResp;
  onClose: () => void;
  onSubmit: (data: CreateGroupReq | UpdateGroupReq) => void;
  loading: boolean;
  platforms: string[];
  instructionPresets: (platform: string) => string[];
}) {
  const { t } = useTranslation();
  const isEdit = !!group;

  const [form, setForm] = useState({
    force_instructions: group?.force_instructions ?? '',
    is_exclusive: group?.is_exclusive ?? false,
    name: group?.name ?? '',
    note: group?.note ?? '',
    platform: group?.platform ?? '',
    rate_multiplier: group?.rate_multiplier ?? 1,
    sort_weight: group?.sort_weight ?? 0,
    status_visible: group?.status_visible ?? true,
    subscription_type: group?.subscription_type ?? 'standard' as const,
  });
  const [quotas, setQuotas] = useState(parseQuotas(group?.quotas as Record<string, unknown> | undefined));
  const [claudeCodeOnly, setClaudeCodeOnly] = useState(group?.plugin_settings?.claude?.claude_code_only === 'true');
  const [imageEnabled, setImageEnabled] = useState(group?.plugin_settings?.openai?.image_enabled === 'true');
  const [copyFromGroupIds, setCopyFromGroupIds] = useState<number[]>([]);

  const { data: copySourceData } = useQuery({
    queryKey: ['groups-for-copy', form.platform],
    queryFn: () => groupsApi.list({ page: 1, page_size: 100, platform: form.platform }),
    enabled: !isEdit && !!form.platform && open,
  });
  const copySourceGroups: GroupResp[] = copySourceData?.list ?? [];
  const platformOptions = [
    { id: '', label: t('groups.select_platform') },
    ...platforms.map((platform) => ({ id: platform, label: platform })),
  ];
  const selectedPlatformLabel = platformOptions.find((item) => item.id === form.platform)?.label ?? t('groups.select_platform');
  const copyAccountOptions = [
    {
      id: '',
      label: !form.platform
        ? t('groups.copy_accounts_select_platform_first')
        : copySourceGroups.length === 0
          ? t('groups.copy_accounts_empty')
          : t('groups.copy_accounts_placeholder'),
    },
    ...copySourceGroups
      .filter((copyGroup) => !copyFromGroupIds.includes(copyGroup.id))
      .map((copyGroup) => ({
        id: String(copyGroup.id),
        label: `${copyGroup.name} (${t('groups.copy_accounts_count', { count: copyGroup.account_total })})`,
      })),
  ];
  const subscriptionTypeOptions = [
    { id: 'standard', label: t('groups.type_standard') },
    { id: 'subscription', label: t('groups.type_subscription') },
  ];
  const selectedSubscriptionTypeLabel =
    subscriptionTypeOptions.find((item) => item.id === form.subscription_type)?.label ?? t('groups.type_standard');

  const handleSubmit = () => {
    if (!isEdit && (!form.name || !form.platform)) return;

    const pluginSettings: Record<string, Record<string, string>> = {};
    if (form.platform === 'claude') {
      pluginSettings.claude = { claude_code_only: claudeCodeOnly ? 'true' : 'false' };
    }
    if (form.platform === 'openai') {
      pluginSettings.openai = { image_enabled: imageEnabled ? 'true' : 'false' };
    }

    onSubmit({
      ...form,
      force_instructions: form.force_instructions ?? '',
      note: form.note,
      plugin_settings: Object.keys(pluginSettings).length > 0 ? pluginSettings : undefined,
      quotas: form.subscription_type === 'subscription' ? buildQuotas(quotas) : undefined,
      subscription_type: form.subscription_type as 'standard' | 'subscription',
      ...(!isEdit && copyFromGroupIds.length > 0
        ? { copy_accounts_from_group_ids: copyFromGroupIds }
        : {}),
    });
  };

  const presets = instructionPresets(form.platform);
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
              <Modal.Heading>{title}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
      <div className="space-y-4">
        <HeroTextField fullWidth isRequired>
          <Label>{t('common.name')}</Label>
          <div className="relative">
            <Layers className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
            <Input
              className="pl-9"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              required
            />
          </div>
        </HeroTextField>

        {isEdit ? (
          <HeroTextField fullWidth isDisabled>
            <Label>{t('groups.platform')}</Label>
            <Input value={form.platform} disabled />
          </HeroTextField>
        ) : (
          <Select
            fullWidth
            isRequired
            selectedKey={form.platform}
            onSelectionChange={(key) => {
              setForm({ ...form, platform: key == null ? '' : String(key) });
              setCopyFromGroupIds([]);
            }}
          >
            <Label>{t('groups.platform')}</Label>
            <Select.Trigger>
              <Select.Value>{selectedPlatformLabel}</Select.Value>
              <Select.Indicator />
            </Select.Trigger>
            <Select.Popover>
              <ListBox items={platformOptions}>
                {(item) => (
                  <ListBox.Item id={item.id} textValue={item.label}>
                    {item.label}
                  </ListBox.Item>
                )}
              </ListBox>
            </Select.Popover>
          </Select>
        )}

        {!isEdit ? (
          <div>
            <p className="mb-1.5 text-xs font-medium uppercaser text-text-secondary">
              {t('groups.copy_accounts_title')}
            </p>
            {copyFromGroupIds.length > 0 ? (
              <div className="mb-2 flex flex-wrap gap-1.5">
                {copyFromGroupIds.map((groupId) => {
                  const sourceGroup = copySourceGroups.find((item) => item.id === groupId);
                  return (
                    <Chip key={groupId} color="accent" size="sm" variant="soft">
                      {sourceGroup ? sourceGroup.name : `#${groupId}`}
                      <Button
                        isIconOnly
                        aria-label="remove"
                        size="sm"
                        variant="ghost"
                        onPress={() => setCopyFromGroupIds(copyFromGroupIds.filter((id) => id !== groupId))}
                      >
                        <X className="h-3 w-3" />
                      </Button>
                    </Chip>
                  );
                })}
              </div>
            ) : null}
            <Select
              fullWidth
              isDisabled={!form.platform}
              selectedKey=""
              onSelectionChange={(key) => {
                const value = Number(key);
                if (value && !copyFromGroupIds.includes(value)) {
                  setCopyFromGroupIds([...copyFromGroupIds, value]);
                }
              }}
            >
              <Select.Trigger>
                <Select.Value>{copyAccountOptions[0]?.label}</Select.Value>
                <Select.Indicator />
              </Select.Trigger>
              <Select.Popover>
                <ListBox items={copyAccountOptions}>
                  {(item) => (
                    <ListBox.Item id={item.id} textValue={item.label}>
                      {item.label}
                    </ListBox.Item>
                  )}
                </ListBox>
              </Select.Popover>
            </Select>
            <p className="mt-1 text-[11px] text-text-tertiary">{t('groups.copy_accounts_hint')}</p>
          </div>
        ) : null}

        <HeroTextField fullWidth>
          <Label>{t('groups.rate_multiplier')}</Label>
          <Input
            type="number"
            step="0.1"
            value={String(form.rate_multiplier)}
            onChange={(e) => setForm({ ...form, rate_multiplier: Number(e.target.value) })}
          />
        </HeroTextField>

        <Switch
          isSelected={form.is_exclusive}
          onChange={(selected) => setForm({ ...form, is_exclusive: selected })}
        >
          <Switch.Control><Switch.Thumb /></Switch.Control>
          <Switch.Content>{t('groups.exclusive_hint')}</Switch.Content>
        </Switch>

        <Switch
          isSelected={form.status_visible}
          onChange={(selected) => setForm({ ...form, status_visible: selected })}
        >
          <Switch.Control><Switch.Thumb /></Switch.Control>
          <Switch.Content>{t('groups.status_visible_hint')}</Switch.Content>
        </Switch>

        <Select
          fullWidth
          selectedKey={form.subscription_type}
          onSelectionChange={(key) =>
            setForm({ ...form, subscription_type: (key ?? 'standard') as 'standard' | 'subscription' })
          }
        >
          <Label>{t('groups.subscription_type')}</Label>
          <Select.Trigger>
            <Select.Value>{selectedSubscriptionTypeLabel}</Select.Value>
            <Select.Indicator />
          </Select.Trigger>
          <Select.Popover>
            <ListBox items={subscriptionTypeOptions}>
              {(item) => (
                <ListBox.Item id={item.id} textValue={item.label}>
                  {item.label}
                </ListBox.Item>
              )}
            </ListBox>
          </Select.Popover>
        </Select>

        <HeroTextField fullWidth>
          <Label>{t('groups.sort_weight')}</Label>
          <div className="relative">
            <ArrowUpDown className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
            <Input
              className="pl-9"
              type="number"
              value={String(form.sort_weight)}
              onChange={(e) => setForm({ ...form, sort_weight: Number(e.target.value) })}
            />
          </div>
          <Description>{t('groups.sort_weight_hint')}</Description>
        </HeroTextField>

        <HeroTextField fullWidth>
          <Label>{t('groups.note')}</Label>
          <Input
            value={form.note}
            onChange={(e) => setForm({ ...form, note: e.target.value })}
            placeholder={t('groups.note_placeholder')}
          />
          <Description>{t('groups.note_hint')}</Description>
        </HeroTextField>

        {presets.length > 0 ? (
          <div>
            <p className="mb-1.5 text-xs font-medium uppercaser text-text-secondary">
              {t('groups.force_instructions')}
            </p>
            <p className="mb-2 text-[11px] text-text-tertiary">{t('groups.force_instructions_hint')}</p>
            <div className="mb-2 flex flex-wrap gap-2">
              {['', ...presets].map((preset) => (
                <Button
                  key={preset}
                  size="sm"
                  variant={form.force_instructions === preset ? 'primary' : 'secondary'}
                  onPress={() => setForm({ ...form, force_instructions: preset })}
                >
                  {preset || t('groups.instructions_none')}
                </Button>
              ))}
            </div>
            {form.force_instructions && !presets.includes(form.force_instructions) ? (
              <HeroTextField fullWidth>
                <TextArea
                  rows={4}
                  value={form.force_instructions}
                  onChange={(e) => setForm({ ...form, force_instructions: e.target.value })}
                  placeholder={t('groups.instructions_custom_placeholder')}
                />
              </HeroTextField>
            ) : null}
          </div>
        ) : null}

        {form.platform === 'claude' ? (
          <Switch isSelected={claudeCodeOnly} onChange={setClaudeCodeOnly}>
            <Switch.Control><Switch.Thumb /></Switch.Control>
            <Switch.Content>
              <div>
                <div className="text-sm">仅 Claude Code 客户端</div>
                <p className="mt-1 text-[11px] text-text-tertiary">
                  开启后，本分组的账号只接受官方 Claude CLI 发起的流量；非 CLI 请求返回 403。
                </p>
              </div>
            </Switch.Content>
          </Switch>
        ) : null}

        {form.platform === 'openai' ? (
          <Switch isSelected={imageEnabled} onChange={setImageEnabled}>
            <Switch.Control><Switch.Thumb /></Switch.Control>
            <Switch.Content>图片生成</Switch.Content>
          </Switch>
        ) : null}

        {form.subscription_type === 'subscription' ? (
          <div>
            <p className="mb-1.5 text-xs font-medium uppercaser text-text-secondary">
              {t('groups.quotas')}
            </p>
            <p className="mb-2 text-[11px] text-text-tertiary">{t('groups.quota_hint')}</p>
            <div className="grid grid-cols-3 gap-3">
              <HeroTextField fullWidth>
                <Label>{t('groups.quota_daily')}</Label>
                <Input
                  type="number"
                  min="0"
                  value={quotas.daily}
                  onChange={(e) => setQuotas({ ...quotas, daily: e.target.value })}
                />
              </HeroTextField>
              <HeroTextField fullWidth>
                <Label>{t('groups.quota_weekly')}</Label>
                <Input
                  type="number"
                  min="0"
                  value={quotas.weekly}
                  onChange={(e) => setQuotas({ ...quotas, weekly: e.target.value })}
                />
              </HeroTextField>
              <HeroTextField fullWidth>
                <Label>{t('groups.quota_monthly')}</Label>
                <Input
                  type="number"
                  min="0"
                  value={quotas.monthly}
                  onChange={(e) => setQuotas({ ...quotas, monthly: e.target.value })}
                />
              </HeroTextField>
            </div>
          </div>
        ) : null}
      </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button variant="primary" isDisabled={loading} onPress={handleSubmit}>
                {loading ? <Spinner size="sm" /> : null}
                {isEdit ? t('common.save') : t('common.create')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
