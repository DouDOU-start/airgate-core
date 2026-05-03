import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Description, Input, Label, ListBox, Modal, Select, Spinner, TextArea, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { Key } from 'lucide-react';
import { parseIpList, formatIpList } from '../../../shared/utils/ip';
import { AirGateDatePicker } from '../../../shared/components/AirGateDatePicker';
import type { APIKeyResp, UpdateAPIKeyReq, GroupResp } from '../../../shared/types';

interface EditKeyModalProps {
  open: boolean;
  apiKey: APIKeyResp;
  groups: GroupResp[];
  onClose: () => void;
  onSubmit: (data: UpdateAPIKeyReq) => void;
  loading: boolean;
}

export function EditKeyModal({ open, apiKey, groups, onClose, onSubmit, loading }: EditKeyModalProps) {
  const { t } = useTranslation();
  const [groupId, setGroupId] = useState<number>(apiKey.group_id ?? 0);
  const [form, setForm] = useState<UpdateAPIKeyReq>({
    expires_at: apiKey.expires_at,
    max_concurrency: apiKey.max_concurrency,
    name: apiKey.name,
    quota_usd: apiKey.quota_usd,
    sell_rate: apiKey.sell_rate,
    status: apiKey.status as 'active' | 'disabled',
  });
  const [ipWhitelist, setIpWhitelist] = useState(formatIpList(apiKey.ip_whitelist));
  const [ipBlacklist, setIpBlacklist] = useState(formatIpList(apiKey.ip_blacklist));

  const handleSubmit = () => {
    onSubmit({
      ...form,
      group_id: groupId !== apiKey.group_id ? groupId : undefined,
      ip_blacklist: parseIpList(ipBlacklist),
      ip_whitelist: parseIpList(ipWhitelist),
    });
  };

  const groupOptions = [
    { id: '0', label: t('api_keys.group_unbound') },
    ...groups.map((group) => ({
      id: String(group.id),
      label: `${group.name} (${group.platform}) · ${group.rate_multiplier}x`,
    })),
  ];
  const selectedGroupLabel = groupOptions.find((item) => item.id === String(groupId))?.label ?? t('api_keys.group_unbound');
  const statusOptions = [
    { id: 'active', label: t('status.active') },
    { id: 'disabled', label: t('status.disabled') },
  ];
  const selectedStatusLabel = statusOptions.find((item) => item.id === (form.status ?? 'active'))?.label ?? t('status.active');
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
              <Modal.Heading>{t('api_keys.edit')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
      <div className="space-y-4">
        <HeroTextField fullWidth>
          <Label>{t('common.name')}</Label>
          <div className="relative">
            <Key className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
            <Input
              className="pl-9"
              value={form.name ?? ''}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
            />
          </div>
        </HeroTextField>

        <Select
          fullWidth
          selectedKey={String(groupId)}
          onSelectionChange={(key) => setGroupId(key == null ? 0 : Number(key))}
        >
          <Label>{t('api_keys.group')}</Label>
          <Select.Trigger>
            <Select.Value>{selectedGroupLabel}</Select.Value>
            <Select.Indicator />
          </Select.Trigger>
          <Select.Popover>
            <ListBox items={groupOptions}>
              {(item) => (
                <ListBox.Item id={item.id} textValue={item.label}>
                  {item.label}
                </ListBox.Item>
              )}
            </ListBox>
          </Select.Popover>
        </Select>

        <HeroTextField fullWidth>
          <Label>{t('api_keys.quota_label')}</Label>
          <Input
            type="number"
            step="0.01"
            min="0"
            value={String(form.quota_usd ?? 0)}
            onChange={(e) => setForm({ ...form, quota_usd: Number(e.target.value) })}
          />
          <Description>{t('api_keys.quota_hint')}</Description>
        </HeroTextField>

        <HeroTextField fullWidth>
          <Label>{t('api_keys.sell_rate_label', '销售倍率')}</Label>
          <Input
            type="number"
            step="0.01"
            min="0"
            value={String(form.sell_rate ?? 0)}
            onChange={(e) => setForm({ ...form, sell_rate: Number(e.target.value) })}
          />
          <Description>{t('api_keys.sell_rate_hint', '留空或 0 表示按平台原价计费')}</Description>
        </HeroTextField>

        <HeroTextField fullWidth>
          <Label>{t('api_keys.max_concurrency_label', '最大并发数')}</Label>
          <Input
            type="number"
            step="1"
            min="0"
            value={String(form.max_concurrency ?? 0)}
            onChange={(e) => setForm({ ...form, max_concurrency: Number(e.target.value) })}
          />
          <Description>{t('api_keys.max_concurrency_hint', '留空或 0 表示不限制')}</Description>
        </HeroTextField>

        <AirGateDatePicker
          description={t('api_keys.expire_hint')}
          label={t('api_keys.expire_time')}
          value={form.expires_at ? form.expires_at.split('T')[0] : ''}
          onChange={(value) => setForm({ ...form, expires_at: value ? `${value}T23:59:59Z` : '' })}
        />

        <Select
          fullWidth
          selectedKey={form.status ?? 'active'}
          onSelectionChange={(key) =>
            setForm({ ...form, status: (key ?? 'active') as 'active' | 'disabled' })
          }
        >
          <Label>{t('common.status')}</Label>
          <Select.Trigger>
            <Select.Value>{selectedStatusLabel}</Select.Value>
            <Select.Indicator />
          </Select.Trigger>
          <Select.Popover>
            <ListBox items={statusOptions}>
              {(item) => (
                <ListBox.Item id={item.id} textValue={item.label}>
                  {item.label}
                </ListBox.Item>
              )}
            </ListBox>
          </Select.Popover>
        </Select>

        <HeroTextField fullWidth>
          <Label>{t('api_keys.ip_whitelist')}</Label>
          <TextArea
            className="font-mono"
            placeholder={t('api_keys.ip_placeholder')}
            value={ipWhitelist}
            onChange={(e) => setIpWhitelist(e.target.value)}
            rows={2}
          />
        </HeroTextField>

        <HeroTextField fullWidth>
          <Label>{t('api_keys.ip_blacklist')}</Label>
          <TextArea
            className="font-mono"
            placeholder={t('api_keys.ip_placeholder')}
            value={ipBlacklist}
            onChange={(e) => setIpBlacklist(e.target.value)}
            rows={2}
          />
        </HeroTextField>
      </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button variant="primary" isDisabled={loading} onPress={handleSubmit}>
                {loading ? <Spinner size="sm" /> : null}
                {t('common.save')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
