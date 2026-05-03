import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, ComboBox, Description, Input, Label, ListBox, Modal, Spinner, TextArea, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { Key } from 'lucide-react';
import { parseIpList } from '../../../shared/utils/ip';
import { useAuth } from '../../../app/providers/AuthProvider';
import { AirGateDatePicker } from '../../../shared/components/AirGateDatePicker';
import type { CreateAPIKeyReq, GroupResp } from '../../../shared/types';

interface CreateKeyModalProps {
  open: boolean;
  groups: GroupResp[];
  onClose: () => void;
  onSubmit: (data: CreateAPIKeyReq) => void;
  loading: boolean;
}

const defaultForm: CreateAPIKeyReq = {
  expires_at: '',
  group_id: 0,
  max_concurrency: 0,
  name: '',
  quota_usd: 0,
  sell_rate: 0,
};

export function CreateKeyModal({ open, groups, onClose, onSubmit, loading }: CreateKeyModalProps) {
  const { t } = useTranslation();
  const { user } = useAuth();
  const [form, setForm] = useState<CreateAPIKeyReq>(defaultForm);
  const [ipWhitelist, setIpWhitelist] = useState('');
  const [ipBlacklist, setIpBlacklist] = useState('');

  const handleClose = () => {
    setForm(defaultForm);
    setIpWhitelist('');
    setIpBlacklist('');
    onClose();
  };

  const handleSubmit = () => {
    if (!form.name || !form.group_id) return;
    onSubmit({
      ...form,
      expires_at: form.expires_at || undefined,
      ip_blacklist: parseIpList(ipBlacklist),
      ip_whitelist: parseIpList(ipWhitelist),
      max_concurrency: form.max_concurrency ?? 0,
      quota_usd: form.quota_usd || undefined,
      sell_rate: form.sell_rate || undefined,
    });
  };

  const groupOptions = groups.map((group) => {
    const override = user?.group_rates?.[group.id];
    const hasOverride = override != null && override > 0 && override !== group.rate_multiplier;
    return {
      id: String(group.id),
      label: (
        <div className="flex min-w-0 items-center justify-between gap-2">
          <span className="truncate">{group.name} ({group.platform})</span>
          <span className="shrink-0 text-xs text-text-tertiary">
            {hasOverride ? (
              <>
                <span className="line-through opacity-60">{group.rate_multiplier}x</span>{' '}
                <span className="font-medium text-primary">{override}x</span>
              </>
            ) : (
              <>{group.rate_multiplier}x 倍率</>
            )}
          </span>
        </div>
      ),
      textValue: `${group.name} ${group.platform}`,
    };
  });
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) handleClose();
    },
  });

  return (
    <Modal state={modalState}>
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
            <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '720px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{t('api_keys.create')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="ag-form-scroll-safe">
                <div className="grid grid-cols-1 gap-x-8 gap-y-6 md:grid-cols-2">
                  <div className="space-y-5">
                    <HeroTextField fullWidth isRequired>
                      <Label>{t('common.name')}</Label>
                      <div className="relative">
                        <Key className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                        <Input
                          className="pl-9"
                          value={form.name}
                          onChange={(e) => setForm({ ...form, name: e.target.value })}
                          placeholder={t('api_keys.name_placeholder')}
                          required
                        />
                      </div>
                    </HeroTextField>

                    <ComboBox
                      allowsCustomValue
                      fullWidth
                      inputValue={groups.find((group) => group.id === form.group_id)?.name ?? ''}
                      isRequired
                      items={groupOptions}
                      menuTrigger="focus"
                      selectedKey={form.group_id ? String(form.group_id) : null}
                      onSelectionChange={(key) => setForm({ ...form, group_id: key == null ? 0 : Number(key) })}
                    >
                      <Label>{t('api_keys.group')}</Label>
                      <ComboBox.InputGroup>
                        <Input placeholder={t('api_keys.select_group')} required />
                        <ComboBox.Trigger />
                      </ComboBox.InputGroup>
                      <ComboBox.Popover>
                        <ListBox items={groupOptions}>
                          {(item) => (
                            <ListBox.Item id={item.id} textValue={item.textValue}>
                              {item.label}
                            </ListBox.Item>
                          )}
                        </ListBox>
                      </ComboBox.Popover>
                    </ComboBox>

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
                  </div>

                  <div className="space-y-5">
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
                </div>
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={handleClose}>
                {t('common.cancel')}
              </Button>
              <Button variant="primary" isDisabled={loading} onPress={handleSubmit}>
                {loading ? <Spinner size="sm" /> : null}
                {t('common.create')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
