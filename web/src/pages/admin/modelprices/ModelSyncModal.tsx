import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Checkbox, Chip, Input, Modal, Spinner, useOverlayState } from '@heroui/react';
import { Search } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { modelPricesApi } from '../../../shared/api/modelPrices';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { queryKeys } from '../../../shared/queryKeys';
import { useToast } from '../../../shared/ui';

const MAX_SELECTION = 500;
const MAX_VISIBLE = 200;

function priceLabel(input: number, output: number, perRequest: number): string {
  if (perRequest > 0) return `$${perRequest.toFixed(4)} / req`;
  return `$${input.toFixed(3)} / $${output.toFixed(3)}`;
}

export function ModelSyncModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();
  const [keyword, setKeyword] = useState('');
  const [showExisting, setShowExisting] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (next) => {
      if (!next) onClose();
    },
  });

  useEffect(() => {
    if (open) {
      setKeyword('');
      setShowExisting(false);
      setSelected(new Set());
    }
  }, [open]);

  const candidatesQuery = useQuery({
    queryKey: queryKeys.modelPrices('sync-candidates'),
    queryFn: modelPricesApi.syncCandidates,
    enabled: open,
    staleTime: 0,
  });

  const filtered = useMemo(() => {
    const term = keyword.trim().toLowerCase();
    return (candidatesQuery.data ?? []).filter((item) => {
      if (!showExisting && item.exists) return false;
      if (!term) return true;
      return item.model.toLowerCase().includes(term) ||
        (item.provider ?? '').toLowerCase().includes(term) ||
        (item.mode ?? '').toLowerCase().includes(term);
    });
  }, [candidatesQuery.data, keyword, showExisting]);

  const visible = filtered.slice(0, MAX_VISIBLE);
  const selectableVisible = visible.filter((item) => !item.exists);
  const allVisibleSelected = selectableVisible.length > 0 &&
    selectableVisible.every((item) => selected.has(item.model));

  const toggleModel = (model: string, checked: boolean) => {
    setSelected((current) => {
      const next = new Set(current);
      if (checked) {
        if (next.size >= MAX_SELECTION) {
          toast('warning', t('model_prices.model_sync_limit', { count: MAX_SELECTION }));
          return current;
        }
        next.add(model);
      } else {
        next.delete(model);
      }
      return next;
    });
  };

  const toggleVisible = (checked: boolean) => {
    setSelected((current) => {
      const next = new Set(current);
      for (const item of selectableVisible) {
        if (!checked) {
          next.delete(item.model);
        } else if (next.size < MAX_SELECTION) {
          next.add(item.model);
        }
      }
      return next;
    });
  };

  const syncMutation = useMutation({
    mutationFn: () => modelPricesApi.syncSelected([...selected]),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.modelPrices() });
      toast('success', t('model_prices.model_sync_success', {
        created: result.created,
        updated: result.updated,
        unchanged: result.unchanged,
      }));
      onClose();
    },
    onError: (error: Error) => toast('error', error.message),
  });

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" size="lg" scroll="inside">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{t('model_prices.model_sync_title')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body className="space-y-3">
              <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                <div className="relative min-w-0 flex-1">
                  <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                  <Input
                    aria-label={t('common.search')}
                    className="pl-9"
                    placeholder={t('model_prices.model_sync_search')}
                    value={keyword}
                    onChange={(event) => setKeyword(event.target.value)}
                  />
                </div>
                <Checkbox isSelected={showExisting} onChange={setShowExisting}>
                  <Checkbox.Control><Checkbox.Indicator /></Checkbox.Control>
                  {t('model_prices.model_sync_show_existing')}
                </Checkbox>
              </div>

              <div className="flex items-center justify-between border-b border-border pb-2 text-xs text-text-secondary">
                <Checkbox isSelected={allVisibleSelected} onChange={toggleVisible}>
                  <Checkbox.Control><Checkbox.Indicator /></Checkbox.Control>
                  {t('model_prices.model_sync_select_visible')}
                </Checkbox>
                <span>{t('model_prices.model_sync_selected', { count: selected.size })}</span>
              </div>

              <div className="min-h-64 overflow-hidden rounded-[var(--radius)] border border-border">
                {candidatesQuery.isLoading ? (
                  <div className="flex min-h-64 items-center justify-center"><Spinner /></div>
                ) : candidatesQuery.isError ? (
                  <div className="flex min-h-64 items-center justify-center px-4 text-sm text-danger">
                    {candidatesQuery.error.message}
                  </div>
                ) : visible.length === 0 ? (
                  <div className="flex min-h-64 items-center justify-center text-sm text-text-tertiary">
                    {t('model_prices.model_sync_empty')}
                  </div>
                ) : (
                  <div className="max-h-[52vh] overflow-y-auto">
                    {visible.map((item) => (
                      <div
                        className={`flex min-h-12 items-center gap-3 border-b border-border px-3 py-2 last:border-b-0 ${
                          item.exists ? 'bg-surface opacity-60' : 'hover:bg-surface-hover'
                        }`}
                        key={item.model}
                      >
                        <Checkbox
                          aria-label={item.model}
                          isDisabled={item.exists}
                          isSelected={selected.has(item.model)}
                          onChange={(checked) => toggleModel(item.model, checked)}
                        >
                          <Checkbox.Control><Checkbox.Indicator /></Checkbox.Control>
                        </Checkbox>
                        <span className="min-w-0 flex-1">
                          <span className="block truncate font-mono text-xs text-text" title={item.model}>{item.model}</span>
                          <span className="block truncate text-[11px] text-text-tertiary">
                            {[item.provider, item.mode].filter(Boolean).join(' / ') || '-'}
                          </span>
                        </span>
                        {item.exists ? <Chip size="sm" variant="soft">{t('model_prices.model_sync_exists')}</Chip> : null}
                        <span className="shrink-0 font-mono text-[11px] text-text-secondary">
                          {priceLabel(item.input_price, item.output_price, item.per_request_price)}
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
              {filtered.length > MAX_VISIBLE ? (
                <p className="text-right text-xs text-text-tertiary">
                  {t('model_prices.model_sync_visible_limit', { shown: MAX_VISIBLE, total: filtered.length })}
                </p>
              ) : null}
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>{t('common.cancel')}</Button>
              <Button
                isDisabled={selected.size === 0 || syncMutation.isPending}
                variant="primary"
                onPress={() => syncMutation.mutate()}
              >
                {syncMutation.isPending ? <Spinner size="sm" /> : null}
                {t('model_prices.model_sync_confirm', { count: selected.size })}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
