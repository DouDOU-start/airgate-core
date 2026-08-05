import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Input,
  Label,
  ListBox,
  Modal,
  Select,
  Spinner,
  TextField as HeroTextField,
  useOverlayState,
} from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { Globe } from 'lucide-react';
import type {
  CreateProxyReq,
  ProxyProtocol,
  ProxyResp,
  ProxyStatus,
  UpdateProxyReq,
} from '../../../shared/types';

export function ProxyFormModal({
  open,
  title,
  proxy,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  title: string;
  proxy?: ProxyResp;
  onClose: () => void;
  onSubmit: (data: CreateProxyReq | UpdateProxyReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const isEdit = !!proxy;

  const buildForm = () => ({
    name: proxy?.name ?? '',
    protocol: (proxy?.protocol ?? 'http') as ProxyProtocol,
    address: proxy?.address ?? '',
    port: proxy?.port != null ? String(proxy.port) : '',
    username: proxy?.username ?? '',
    password: '',
    status: (proxy?.status ?? 'active') as ProxyStatus,
  });

  const [form, setForm] = useState(buildForm);

  useEffect(() => {
    if (!open) {
      setForm(buildForm());
    }
  }, [open, proxy]);

  const protocolOptions = [
    { id: 'http', label: 'HTTP' },
    { id: 'socks5', label: 'SOCKS5' },
  ];
  const statusOptions = [
    { id: 'active', label: t('proxies.status_active') },
    { id: 'disabled', label: t('proxies.status_disabled') },
  ];
  const selectedProtocolLabel =
    protocolOptions.find((item) => item.id === form.protocol)?.label ?? 'HTTP';
  const selectedStatusLabel =
    statusOptions.find((item) => item.id === form.status)?.label ?? t('proxies.status_active');

  const handleSubmit = () => {
    if (!form.name.trim() || !form.address.trim() || !form.port) return;
    const port = Number(form.port);
    if (!Number.isFinite(port) || port <= 0 || port > 65535) return;

    if (isEdit) {
      const payload: UpdateProxyReq = {
        name: form.name.trim(),
        protocol: form.protocol,
        address: form.address.trim(),
        port,
        username: form.username.trim() || undefined,
        status: form.status,
      };
      // 密码留空 = 不修改
      if (form.password) {
        payload.password = form.password;
      }
      onSubmit(payload);
      return;
    }

    onSubmit({
      name: form.name.trim(),
      protocol: form.protocol,
      address: form.address.trim(),
      port,
      username: form.username.trim() || undefined,
      password: form.password || undefined,
    } satisfies CreateProxyReq);
  };

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '540px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{title}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <HeroTextField fullWidth isRequired>
                  <Label>{t('proxies.name')}</Label>
                  <div className="relative">
                    <Globe className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                    <Input
                      className="pl-9"
                      value={form.name}
                      onChange={(e) => setForm({ ...form, name: e.target.value })}
                      required
                    />
                  </div>
                </HeroTextField>

                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <div>
                    <Label className="mb-1.5 block">{t('proxies.protocol')}</Label>
                    <Select
                      aria-label={t('proxies.protocol')}
                      fullWidth
                      selectedKey={form.protocol}
                      onSelectionChange={(key) => {
                        if (key == null) return;
                        setForm({ ...form, protocol: String(key) as ProxyProtocol });
                      }}
                    >
                      <Select.Trigger>
                        <Select.Value>{selectedProtocolLabel}</Select.Value>
                        <Select.Indicator />
                      </Select.Trigger>
                      <Select.Popover>
                        <ListBox items={protocolOptions}>
                          {(item) => (
                            <ListBox.Item id={item.id} textValue={item.label}>
                              {item.label}
                            </ListBox.Item>
                          )}
                        </ListBox>
                      </Select.Popover>
                    </Select>
                  </div>

                  {isEdit && (
                    <div>
                      <Label className="mb-1.5 block">{t('proxies.status')}</Label>
                      <Select
                        aria-label={t('proxies.status')}
                        fullWidth
                        selectedKey={form.status}
                        onSelectionChange={(key) => {
                          if (key == null) return;
                          setForm({ ...form, status: String(key) as ProxyStatus });
                        }}
                      >
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
                    </div>
                  )}
                </div>

                <div className="grid grid-cols-1 gap-4 sm:grid-cols-[1fr_120px]">
                  <HeroTextField fullWidth isRequired>
                    <Label>{t('proxies.address')}</Label>
                    <Input
                      value={form.address}
                      onChange={(e) => setForm({ ...form, address: e.target.value })}
                      placeholder="1.2.3.4"
                      required
                    />
                  </HeroTextField>
                  <HeroTextField fullWidth isRequired>
                    <Label>{t('proxies.port')}</Label>
                    <Input
                      inputMode="numeric"
                      value={form.port}
                      onChange={(e) => setForm({ ...form, port: e.target.value })}
                      placeholder="7890"
                      required
                    />
                  </HeroTextField>
                </div>

                <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                  <HeroTextField fullWidth>
                    <Label>{t('proxies.username')}</Label>
                    <Input
                      value={form.username}
                      onChange={(e) => setForm({ ...form, username: e.target.value })}
                      autoComplete="off"
                    />
                  </HeroTextField>
                  <HeroTextField fullWidth>
                    <Label>
                      {t('proxies.password')}
                      {isEdit ? (
                        <span className="ml-1 text-xs font-normal text-text-tertiary">
                          ({t('proxies.password_keep_hint')})
                        </span>
                      ) : null}
                    </Label>
                    <Input
                      type="password"
                      value={form.password}
                      onChange={(e) => setForm({ ...form, password: e.target.value })}
                      autoComplete="new-password"
                    />
                  </HeroTextField>
                </div>
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
