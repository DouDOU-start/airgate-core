import { useEffect, useState } from 'react';
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
import { AppWindow, ChevronDown, Link2, ShieldCheck } from 'lucide-react';
import type { OAuthClientResp, UpdateOAuthClientReq } from '../../../shared/types';

const DEFAULT_CALLBACK_PATH = '/api/v1/auth/callback';

// redirect_uris 表单里一行一个，提交时拆分过滤空行
function splitLines(value: string): string[] {
  return value
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean);
}

// AirGate 生态应用默认将 OAuth 回调放在入口地址下的固定路径；特殊应用仍可使用高级配置覆盖。
function buildDefaultRedirectURI(launchURL: string): string {
  const trimmed = launchURL.trim();
  if (!trimmed) return '';
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return '';
    parsed.search = '';
    parsed.hash = '';
    const basePath = parsed.pathname.replace(/\/+$/, '');
    parsed.pathname = `${basePath}${DEFAULT_CALLBACK_PATH}`;
    return parsed.toString();
  } catch {
    return '';
  }
}

function usesAdvancedRedirects(client?: OAuthClientResp): boolean {
  if (!client) return false;
  const generated = buildDefaultRedirectURI(client.launch_url);
  return generated === '' || client.redirect_uris.length !== 1 || client.redirect_uris[0] !== generated;
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

  const buildForm = () => ({
    name: client?.name ?? '',
    description: client?.description ?? '',
    redirect_uris: (client?.redirect_uris ?? []).join('\n'),
    launch_url: client?.launch_url ?? '',
    icon: client?.icon ?? '',
    sort_order: String(client?.sort_order ?? 0),
    first_party: client?.first_party ?? true,
    enabled: client?.enabled ?? true,
    show_in_nav: client?.show_in_nav ?? true,
    advanced_redirects: usesAdvancedRedirects(client),
  });

  const [form, setForm] = useState(buildForm);

  // 弹窗常驻挂载：关闭（含提交成功后父组件收起）时重置表单，避免下次打开残留上次输入
  useEffect(() => {
    if (!open) {
      setForm(buildForm());
    }
  }, [open, client]);

  const handleSubmit = () => {
    const defaultRedirectURI = buildDefaultRedirectURI(form.launch_url);
    onSubmit({
      name: form.name.trim(),
      description: form.description.trim(),
      redirect_uris: form.advanced_redirects ? splitLines(form.redirect_uris) : defaultRedirectURI ? [defaultRedirectURI] : [],
      launch_url: form.launch_url.trim(),
      icon: form.icon.trim(),
      sort_order: Number(form.sort_order) || 0,
      first_party: form.first_party,
      enabled: form.enabled,
      show_in_nav: form.show_in_nav,
    });
  };

  const defaultRedirectURI = buildDefaultRedirectURI(form.launch_url);
  const redirectURIs = form.advanced_redirects ? splitLines(form.redirect_uris) : defaultRedirectURI ? [defaultRedirectURI] : [];
  const canSubmit = form.name.trim() !== '' && redirectURIs.length > 0 && (!form.show_in_nav || form.launch_url.trim() !== '');

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

                <HeroTextField fullWidth isRequired={form.show_in_nav}>
                  <Label>{t('oauth_clients.form_launch_url')}</Label>
                  <div className="relative">
                    <Link2 className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                    <Input
                      className="pl-9"
                      value={form.launch_url}
                      onChange={(e) => setForm({ ...form, launch_url: e.target.value })}
                      placeholder="https://market.example.com"
                      required={form.show_in_nav}
                    />
                  </div>
                  <Description>{t('oauth_clients.form_launch_url_hint')}</Description>
                </HeroTextField>

                <div className="overflow-hidden rounded-xl border border-border bg-surface-secondary/45">
                  <div className="flex items-start gap-3 px-4 py-3.5">
                    <span className="mt-0.5 grid h-8 w-8 shrink-0 place-items-center rounded-lg border border-success/25 bg-success/10 text-success">
                      <ShieldCheck className="h-4 w-4" />
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center justify-between gap-2">
                        <span className="text-sm font-medium text-text">{t('oauth_clients.form_redirect_auto_title')}</span>
                        <button
                          aria-expanded={form.advanced_redirects}
                          className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-primary transition-colors hover:bg-primary/10"
                          type="button"
                          onClick={() => setForm({
                            ...form,
                            advanced_redirects: !form.advanced_redirects,
                            redirect_uris: form.redirect_uris || defaultRedirectURI,
                          })}
                        >
                          {form.advanced_redirects ? t('oauth_clients.form_redirect_simple') : t('oauth_clients.form_redirect_advanced')}
                          <ChevronDown className={`h-3.5 w-3.5 transition-transform ${form.advanced_redirects ? 'rotate-180' : ''}`} />
                        </button>
                      </div>
                      <p className="mt-1 text-xs leading-5 text-text-tertiary">{t('oauth_clients.form_redirect_auto_hint')}</p>
                      {!form.advanced_redirects ? (
                        <code className="mt-2 block overflow-x-auto rounded-lg border border-border/70 bg-surface px-3 py-2 text-xs text-text-secondary">
                          {defaultRedirectURI || t('oauth_clients.form_redirect_pending')}
                        </code>
                      ) : null}
                    </div>
                  </div>
                  {form.advanced_redirects ? (
                    <div className="border-t border-border px-4 py-3.5">
                      <HeroTextField fullWidth isRequired>
                        <Label>{t('oauth_clients.form_redirect_uris')}</Label>
                        <TextArea
                          rows={3}
                          value={form.redirect_uris}
                          onChange={(e) => setForm({ ...form, redirect_uris: e.target.value })}
                          placeholder={'https://market.example.com/api/v1/auth/callback'}
                          required
                        />
                        <Description>{t('oauth_clients.form_redirect_uris_hint')}</Description>
                      </HeroTextField>
                    </div>
                  ) : null}
                </div>

                <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                  <HeroTextField fullWidth>
                    <Label>{t('oauth_clients.form_icon')}</Label>
                    <Input
                      value={form.icon}
                      onChange={(e) => setForm({ ...form, icon: e.target.value })}
                      placeholder="💬"
                    />
                    <Description>{t('oauth_clients.form_icon_hint')}</Description>
                  </HeroTextField>
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
