import { useEffect, useMemo, useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Input, Modal, Spinner, useOverlayState } from '@heroui/react';
import { Plus, X } from 'lucide-react';
import { accountsApi } from '../../../shared/api/accounts';
import { modelPricesApi } from '../../../shared/api/modelPrices';
import { queryKeys } from '../../../shared/queryKeys';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { useToast } from '../../../shared/ui';
import type { AccountResp } from '../../../shared/types';

function modelsFromAccount(account: AccountResp): string[] {
  if (Array.isArray(account.models) && account.models.length > 0) {
    return [...account.models];
  }
  const raw = account.extra?.models;
  if (Array.isArray(raw)) {
    return raw.map((item) => String(item).trim()).filter(Boolean);
  }
  return [];
}

async function fetchCatalogNames(): Promise<string[]> {
  const names: string[] = [];
  let page = 1;
  for (;;) {
    const resp = await modelPricesApi.list({ page, page_size: 100 });
    for (const item of resp.list) names.push(item.model);
    if (resp.list.length === 0 || page * 100 >= resp.total) break;
    page += 1;
  }
  return names;
}

/**
 * 账号可服务模型白名单（写入 extra.models，与渠道「模型」配置同构）。
 * - 单账号：打开时回填当前白名单
 * - 多账号批量：覆盖写入同一清单；空列表=清除白名单，回退平台默认
 */
export function AccountModelsModal({
  accounts,
  onClose,
}: {
  accounts: AccountResp[];
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const open = accounts.length > 0;
  const isBulk = accounts.length > 1;
  const single = accounts.length === 1 ? accounts[0] : null;

  const [models, setModels] = useState<string[]>([]);
  const [modelInput, setModelInput] = useState('');

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (next) => {
      if (!next) onClose();
    },
  });

  useEffect(() => {
    if (!open) {
      setModels([]);
      setModelInput('');
      return;
    }
    if (single) {
      setModels(modelsFromAccount(single));
    } else {
      setModels([]);
    }
    setModelInput('');
  }, [open, single?.id, accounts.length]);

  const catalogQuery = useQuery({
    queryKey: queryKeys.modelPrices('catalog-names'),
    queryFn: fetchCatalogNames,
    enabled: open,
    staleTime: 60_000,
  });
  const catalog = useMemo(() => new Set(catalogQuery.data ?? []), [catalogQuery.data]);
  const catalogReady = catalogQuery.isSuccess;

  const suggestions = useMemo(() => {
    const keyword = modelInput.trim().toLowerCase();
    if (!keyword || !catalogReady) return [];
    return (catalogQuery.data ?? [])
      .filter((name) => name.toLowerCase().includes(keyword) && !models.includes(name))
      .slice(0, 12);
  }, [modelInput, catalogReady, catalogQuery.data, models]);

  const addModels = (raw: string) => {
    const parts = raw
      .split(/[\n,]+/)
      .map((item) => item.trim())
      .filter(Boolean);
    if (parts.length === 0) return;
    const rejected: string[] = [];
    setModels((prev) => {
      const next = [...prev];
      for (const part of parts) {
        if (catalogReady && !catalog.has(part)) {
          rejected.push(part);
          continue;
        }
        if (!next.includes(part)) next.push(part);
      }
      return next;
    });
    if (rejected.length > 0) {
      toast('error', t('accounts.model_not_in_catalog_list', { models: rejected.join(', ') }));
    }
    setModelInput('');
  };

  const removeModel = (name: string) => {
    setModels((prev) => prev.filter((item) => item !== name));
  };

  const fillPlatformDefaults = async () => {
    if (!single) return;
    try {
      const list = await accountsApi.testModels(single.id);
      const ids = (list ?? []).map((m) => m.id).filter(Boolean);
      setModels(ids);
      toast('success', t('accounts.models_filled_defaults', { count: ids.length }));
    } catch (err) {
      toast('error', err instanceof Error ? err.message : t('accounts.models_fill_failed'));
    }
  };

  const saveMutation = useMutation({
    mutationFn: async () => {
      const payload = [...models];
      if (isBulk) {
        return accountsApi.bulkUpdate({
          account_ids: accounts.map((a) => a.id),
          models: payload,
        });
      }
      if (!single) throw new Error('no account');
      return accountsApi.update(single.id, { models: payload });
    },
    onSuccess: (resp) => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.accounts() });
      if (isBulk && resp && typeof resp === 'object' && 'success' in resp) {
        const bulk = resp as { success: number; failed: number };
        if (bulk.failed > 0) {
          toast(
            bulk.success > 0 ? 'warning' : 'error',
            t('accounts.bulk_partial', { success: bulk.success, failed: bulk.failed }),
          );
        } else {
          toast('success', t('accounts.models_save_success'));
        }
      } else {
        toast('success', t('accounts.models_save_success'));
      }
      onClose();
    },
    onError: (err) => {
      toast('error', err instanceof Error ? err.message : t('accounts.models_save_failed'));
    },
  });

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter' || event.key === ',') {
      event.preventDefault();
      addModels(modelInput);
    }
  };

  const title = isBulk
    ? t('accounts.models_bulk_title', { count: accounts.length })
    : t('accounts.models_title', { name: single?.name || single?.id });

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" size="md" scroll="inside">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{title}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body className="space-y-3">
              <p className="text-xs text-text-tertiary">
                {isBulk ? t('accounts.models_bulk_hint') : t('accounts.models_hint')}
              </p>

              <div className="flex items-center gap-2">
                <Input
                  aria-label={t('accounts.models')}
                  className="flex-1 font-mono text-xs"
                  placeholder={t('accounts.add_model_placeholder')}
                  value={modelInput}
                  onChange={(e) => setModelInput(e.target.value)}
                  onKeyDown={handleKeyDown}
                />
                <Button
                  isDisabled={!modelInput.trim()}
                  size="sm"
                  variant="secondary"
                  onPress={() => addModels(modelInput)}
                >
                  <Plus className="h-3.5 w-3.5" />
                  {t('accounts.add_model')}
                </Button>
              </div>

              {modelInput.trim() && catalogReady ? (
                suggestions.length > 0 ? (
                  <div className="flex flex-wrap items-center gap-1.5">
                    {suggestions.map((name) => (
                      <button
                        key={name}
                        className="rounded-md bg-accent-soft px-1.5 py-0.5 font-mono text-xs text-accent-soft-foreground hover:opacity-80"
                        type="button"
                        onClick={() => addModels(name)}
                      >
                        {name}
                      </button>
                    ))}
                  </div>
                ) : (
                  <p className="text-xs text-warning">{t('accounts.model_no_match')}</p>
                )
              ) : null}

              <div className="max-h-[40vh] min-h-[8rem] overflow-y-auto rounded-[var(--radius)] border border-border">
                {models.length === 0 ? (
                  <p className="px-3 py-6 text-center text-xs text-text-tertiary">
                    {t('accounts.models_empty')}
                  </p>
                ) : (
                  models.map((model) => {
                    const notInCatalog = catalogReady && !catalog.has(model);
                    return (
                      <div
                        className="flex items-center gap-2 border-b border-border px-3 py-2 last:border-b-0"
                        key={model}
                      >
                        <span
                          className={`min-w-0 flex-1 truncate font-mono text-xs ${
                            notInCatalog ? 'text-warning' : 'text-text'
                          }`}
                          title={model}
                        >
                          {model}
                        </span>
                        <Button
                          isIconOnly
                          size="sm"
                          variant="ghost"
                          aria-label={t('common.delete')}
                          onPress={() => removeModel(model)}
                        >
                          <X className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    );
                  })
                )}
              </div>
            </Modal.Body>
            <Modal.Footer className="flex flex-wrap gap-2">
              {single ? (
                <Button
                  className="mr-auto"
                  size="sm"
                  variant="secondary"
                  onPress={() => void fillPlatformDefaults()}
                >
                  {t('accounts.models_fill_defaults')}
                </Button>
              ) : (
                <span className="mr-auto" />
              )}
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button
                variant="primary"
                isDisabled={saveMutation.isPending}
                onPress={() => saveMutation.mutate()}
              >
                {saveMutation.isPending ? <Spinner size="sm" /> : null}
                {t('common.save')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
