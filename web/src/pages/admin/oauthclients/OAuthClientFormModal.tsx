import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Description,
  Input,
  Label,
  Modal,
  Spinner,
  TextArea,
  TextField as HeroTextField,
  useOverlayState,
} from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { NativeSwitch } from '../../../shared/components/NativeSwitch';
import { AppWindow } from 'lucide-react';
import type { OAuthClientResp, UpdateOAuthClientReq } from '../../../shared/types';

// redirect_uris 表单里一行一个，提交时拆分过滤空行
function splitLines(value: string): string[] {
  return value
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean);
}

export function OAuthClientFormModal({
  open,
  title,
  client,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  title: string;
  client?: OAuthClientResp;
  onClose: () => void;
  onSubmit: (data: UpdateOAuthClientReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();

  const [form, setForm] = useState({
    name: client?.name ?? '',
    description: client?.description ?? '',
    redirect_uris: (client?.redirect_uris ?? []).join('\n'),
    launch_url: client?.launch_url ?? '',
    icon: client?.icon ?? '',
    sort_order: String(client?.sort_order ?? 0),
    first_party: client?.first_party ?? true,
    enabled: client?.enabled ?? true,
    show_in_nav: client?.show_in_nav ?? true,
  });

  const handleSubmit = () => {
    onSubmit({
      name: form.name.trim(),
      description: form.description.trim(),
      redirect_uris: splitLines(form.redirect_uris),
      launch_url: form.launch_url.trim(),
      icon: form.icon.trim(),
      sort_order: Number(form.sort_order) || 0,
      first_party: form.first_party,
      enabled: form.enabled,
      show_in_nav: form.show_in_nav,
    });
  };

  const canSubmit = form.name.trim() !== '' && splitLines(form.redirect_uris).length > 0;

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
            style={{ maxWidth: '640px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{title}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <HeroTextField fullWidth isRequired>
                  <Label>{t('oauth_clients.form_name')}</Label>
                  <div className="relative">
                    <AppWindow className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                    <Input
                      className="pl-9"
                      value={form.name}
                      onChange={(e) => setForm({ ...form, name: e.target.value })}
                      placeholder={t('oauth_clients.form_name_placeholder')}
                      required
                    />
                  </div>
                </HeroTextField>

                <HeroTextField fullWidth>
                  <Label>{t('oauth_clients.form_description')}</Label>
                  <Input
                    value={form.description}
                    onChange={(e) => setForm({ ...form, description: e.target.value })}
                  />
                </HeroTextField>

                <HeroTextField fullWidth isRequired>
                  <Label>{t('oauth_clients.form_redirect_uris')}</Label>
                  <TextArea
                    rows={3}
                    value={form.redirect_uris}
                    onChange={(e) => setForm({ ...form, redirect_uris: e.target.value })}
                    placeholder={'https://chat.example.com/oauth/callback'}
                    required
                  />
                  <Description>{t('oauth_clients.form_redirect_uris_hint')}</Description>
                </HeroTextField>

                <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                  <HeroTextField fullWidth>
                    <Label>{t('oauth_clients.form_launch_url')}</Label>
                    <Input
                      value={form.launch_url}
                      onChange={(e) => setForm({ ...form, launch_url: e.target.value })}
                      placeholder="https://chat.example.com"
                    />
                    <Description>{t('oauth_clients.form_launch_url_hint')}</Description>
                  </HeroTextField>
                  <HeroTextField fullWidth>
                    <Label>{t('oauth_clients.form_icon')}</Label>
                    <Input
                      value={form.icon}
                      onChange={(e) => setForm({ ...form, icon: e.target.value })}
                      placeholder="💬"
                    />
                    <Description>{t('oauth_clients.form_icon_hint')}</Description>
                  </HeroTextField>
                </div>

                <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                  <HeroTextField fullWidth>
                    <Label>{t('oauth_clients.form_sort_order')}</Label>
                    <Input
                      type="number"
                      value={form.sort_order}
                      onChange={(e) => setForm({ ...form, sort_order: e.target.value })}
                    />
                    <Description>{t('oauth_clients.form_sort_order_hint')}</Description>
                  </HeroTextField>
                </div>

                <div className="space-y-3 pt-1 border-t border-border">
                  <NativeSwitch
                    isSelected={form.first_party}
                    label={(
                      <>
                        <span className="text-sm font-medium text-text">{t('oauth_clients.form_first_party')}</span>
                        <span className="block text-xs text-text-tertiary">{t('oauth_clients.form_first_party_hint')}</span>
                      </>
                    )}
                    onChange={(v) => setForm({ ...form, first_party: v })}
                  />
                  <NativeSwitch
                    isSelected={form.show_in_nav}
                    label={(
                      <>
                        <span className="text-sm font-medium text-text">{t('oauth_clients.form_show_in_nav')}</span>
                        <span className="block text-xs text-text-tertiary">{t('oauth_clients.form_show_in_nav_hint')}</span>
                      </>
                    )}
                    onChange={(v) => setForm({ ...form, show_in_nav: v })}
                  />
                  <NativeSwitch
                    isSelected={form.enabled}
                    label={(
                      <span className="text-sm font-medium text-text">{t('oauth_clients.form_enabled')}</span>
                    )}
                    onChange={(v) => setForm({ ...form, enabled: v })}
                  />
                </div>
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button
                aria-busy={loading}
                isDisabled={loading || !canSubmit}
                variant="primary"
                onPress={handleSubmit}
              >
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
