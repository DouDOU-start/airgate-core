import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  AlertDialog, Button, EmptyState, Input, Label, Modal, Spinner, TextArea,
  TextField as HeroTextField, useOverlayState,
} from '@heroui/react';
import { FileUp, Pencil, Plus, RefreshCw, Search, Trash2 } from 'lucide-react';
import { modelPricesApi } from '../../shared/api/modelPrices';
import { queryKeys } from '../../shared/queryKeys';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { usePagination } from '../../shared/hooks/usePagination';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { useToast } from '../../shared/ui';
import { getTotalPages } from '../../shared/utils/pagination';
import { CommonTable } from '../../shared/components/CommonTable';
import { TableLoadingRow } from '../../shared/components/TableLoadingRow';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import type { CreateModelPriceReq, ImportModelPriceItem, ModelPriceResp } from '../../shared/types';

const COLUMN_COUNT = 8;

// 价格表单（价格单位 USD / 1M tokens；per_request_price 为 USD / 次）
interface PriceForm {
  model: string;
  input_price: string;
  output_price: string;
  cached_input_price: string;
  cache_creation_price: string;
  per_request_price: string;
}

const emptyForm: PriceForm = {
  model: '',
  input_price: '',
  output_price: '',
  cached_input_price: '',
  cache_creation_price: '',
  per_request_price: '',
};

const PRICE_FIELDS = [
  'input_price',
  'output_price',
  'cached_input_price',
  'cache_creation_price',
  'per_request_price',
] as const;

function fmtPrice(value: number): string {
  if (!value) return '-';
  return `$${value}`;
}

// 归一导入 JSON：支持 {items:[...]} 或直接数组两种形态
function normalizeImportItems(raw: string): ImportModelPriceItem[] {
  const parsed: unknown = JSON.parse(raw);
  const list = Array.isArray(parsed)
    ? parsed
    : (parsed !== null && typeof parsed === 'object' && Array.isArray((parsed as { items?: unknown }).items))
      ? (parsed as { items: unknown[] }).items
      : null;
  if (!list) throw new Error('not a list');

  return list.map((entry) => {
    if (entry === null || typeof entry !== 'object' || Array.isArray(entry)) throw new Error('bad item');
    const record = entry as Record<string, unknown>;
    if (typeof record.model !== 'string' || !record.model.trim()) throw new Error('missing model');
    const item: ImportModelPriceItem = { model: record.model.trim() };
    for (const field of PRICE_FIELDS) {
      const value = record[field];
      if (value === undefined || value === null) continue;
      const num = Number(value);
      if (!Number.isFinite(num) || num < 0) throw new Error(`bad ${field}`);
      item[field] = num;
    }
    return item;
  });
}

export default function ModelPricesPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const { page, setPage, pageSize, setPageSize } = usePagination(20, 'admin.model-prices');
  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword.trim(), 250);

  const [modalOpen, setModalOpen] = useState(false);
  const [editingPrice, setEditingPrice] = useState<ModelPriceResp | null>(null);
  const [form, setForm] = useState<PriceForm>(emptyForm);
  const [deleteTarget, setDeleteTarget] = useState<ModelPriceResp | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState('');
  const [importError, setImportError] = useState('');

  const listQuery = useMemo(() => ({
    page,
    page_size: pageSize,
    keyword: debouncedKeyword || undefined,
  }), [page, pageSize, debouncedKeyword]);

  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.modelPrices(listQuery),
    queryFn: () => modelPricesApi.list(listQuery),
    placeholderData: keepPreviousData,
  });

  const rows = data?.list ?? [];
  const total = data?.total ?? 0;
  const totalPages = getTotalPages(total, pageSize);

  const createMutation = useCrudMutation({
    mutationFn: (payload: CreateModelPriceReq) => modelPricesApi.create(payload),
    successMessage: t('model_prices.create_success'),
    queryKey: queryKeys.modelPrices(),
    onSuccess: () => closeModal(),
  });

  const updateMutation = useCrudMutation({
    mutationFn: ({ id, payload }: { id: number; payload: CreateModelPriceReq }) =>
      modelPricesApi.update(id, payload),
    successMessage: t('model_prices.update_success'),
    queryKey: queryKeys.modelPrices(),
    onSuccess: () => closeModal(),
  });

  const deleteMutation = useCrudMutation({
    mutationFn: (id: number) => modelPricesApi.delete(id),
    successMessage: t('model_prices.delete_success'),
    queryKey: queryKeys.modelPrices(),
    onSuccess: () => setDeleteTarget(null),
  });

  const importMutation = useMutation({
    mutationFn: (items: ImportModelPriceItem[]) => modelPricesApi.import({ items }),
    onSuccess: (resp) => {
      toast('success', t('model_prices.import_success', { created: resp.created, updated: resp.updated }));
      queryClient.invalidateQueries({ queryKey: queryKeys.modelPrices() });
      setImportOpen(false);
      setImportText('');
      setImportError('');
    },
    onError: (err: Error) => toast('error', err.message),
  });

  function openCreate() {
    setEditingPrice(null);
    setForm(emptyForm);
    setModalOpen(true);
  }

  function openEdit(price: ModelPriceResp) {
    setEditingPrice(price);
    setForm({
      model: price.model,
      input_price: String(price.input_price),
      output_price: String(price.output_price),
      cached_input_price: String(price.cached_input_price),
      cache_creation_price: String(price.cache_creation_price),
      per_request_price: String(price.per_request_price),
    });
    setModalOpen(true);
  }

  function closeModal() {
    setModalOpen(false);
    setEditingPrice(null);
    setForm(emptyForm);
  }

  function handleSubmit() {
    if (!form.model.trim()) {
      toast('error', t('common.fill_required'));
      return;
    }
    const payload: CreateModelPriceReq = { model: form.model.trim() };
    for (const field of PRICE_FIELDS) {
      const raw = form[field];
      const num = raw === '' ? 0 : Number(raw);
      if (!Number.isFinite(num) || num < 0) {
        toast('error', t('model_prices.price_invalid'));
        return;
      }
      payload[field] = num;
    }
    if (editingPrice) {
      updateMutation.mutate({ id: editingPrice.id, payload });
    } else {
      createMutation.mutate(payload);
    }
  }

  function handleImport() {
    let items: ImportModelPriceItem[];
    try {
      items = normalizeImportItems(importText);
    } catch {
      setImportError(t('model_prices.import_invalid'));
      return;
    }
    if (items.length === 0) {
      setImportError(t('model_prices.import_empty'));
      return;
    }
    setImportError('');
    importMutation.mutate(items);
  }

  const saving = createMutation.isPending || updateMutation.isPending;
  const priceDialogState = useOverlayState({
    isOpen: modalOpen,
    onOpenChange: (open) => {
      if (!open) closeModal();
    },
  });
  const importDialogState = useOverlayState({
    isOpen: importOpen,
    onOpenChange: (open) => {
      if (!open) setImportOpen(false);
    },
  });

  const priceFieldLabels: Record<(typeof PRICE_FIELDS)[number], string> = {
    input_price: t('model_prices.input_price'),
    output_price: t('model_prices.output_price'),
    cached_input_price: t('model_prices.cached_input_price'),
    cache_creation_price: t('model_prices.cache_creation_price'),
    per_request_price: t('model_prices.per_request_price'),
  };

  return (
    <div>
      {/* 筛选 + 工具栏 */}
      <div className="mb-5 flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="relative w-full sm:w-56">
          <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
          <Input
            aria-label={t('common.search')}
            className="pl-9"
            placeholder={t('model_prices.search_placeholder')}
            value={keyword}
            onChange={(event) => {
              setKeyword(event.target.value);
              setPage(1);
            }}
          />
        </div>
        <div className="ml-auto flex items-center gap-2">
          <Button
            isIconOnly
            aria-label={t('common.refresh', 'Refresh')}
            size="md"
            variant="ghost"
            onPress={() => refetch()}
          >
            <RefreshCw className="h-4 w-4" />
          </Button>
          <Button variant="secondary" onPress={() => setImportOpen(true)}>
            <FileUp className="h-4 w-4" />
            {t('model_prices.import')}
          </Button>
          <Button variant="primary" onPress={openCreate}>
            <Plus className="h-4 w-4" />
            {t('model_prices.create')}
          </Button>
        </div>
      </div>

      <CommonTable
        ariaLabel={t('model_prices.title')}
        footer={(
          <TablePaginationFooter
            page={page}
            pageSize={pageSize}
            setPage={setPage}
            setPageSize={setPageSize}
            total={total}
            totalPages={totalPages}
          />
        )}
        minWidth={960}
      >
        <CommonTable.Header>
          <CommonTable.Column id="id" style={{ width: 64 }}>
            {t('common.id')}
          </CommonTable.Column>
          <CommonTable.Column id="model">{t('model_prices.model')}</CommonTable.Column>
          <CommonTable.Column id="input_price">
            {t('model_prices.input_price')}
            <span className="block text-[10px] font-normal text-text-tertiary">{t('model_prices.unit_per_1m')}</span>
          </CommonTable.Column>
          <CommonTable.Column id="output_price">
            {t('model_prices.output_price')}
            <span className="block text-[10px] font-normal text-text-tertiary">{t('model_prices.unit_per_1m')}</span>
          </CommonTable.Column>
          <CommonTable.Column id="cached_input_price">
            {t('model_prices.cached_input_price')}
            <span className="block text-[10px] font-normal text-text-tertiary">{t('model_prices.unit_per_1m')}</span>
          </CommonTable.Column>
          <CommonTable.Column id="cache_creation_price">
            {t('model_prices.cache_creation_price')}
            <span className="block text-[10px] font-normal text-text-tertiary">{t('model_prices.unit_per_1m')}</span>
          </CommonTable.Column>
          <CommonTable.Column id="per_request_price">
            {t('model_prices.per_request_price')}
            <span className="block text-[10px] font-normal text-text-tertiary">{t('model_prices.unit_per_request')}</span>
          </CommonTable.Column>
          <CommonTable.Column id="actions">{t('common.actions')}</CommonTable.Column>
        </CommonTable.Header>
        <CommonTable.Body>
          {isLoading ? (
            <TableLoadingRow colSpan={COLUMN_COUNT} />
          ) : rows.length === 0 ? (
            <CommonTable.Row id="empty">
              <CommonTable.Cell colSpan={COLUMN_COUNT}>
                <EmptyState>
                  <div className="text-sm text-default-500">{t('common.no_data')}</div>
                </EmptyState>
              </CommonTable.Cell>
            </CommonTable.Row>
          ) : (
            rows.map((row) => (
              <CommonTable.Row id={String(row.id)} key={row.id}>
                <CommonTable.Cell>
                  <span className="font-mono text-text-tertiary">{row.id}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-text" title={row.model}>{row.model}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-text-secondary">{fmtPrice(row.input_price)}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-text-secondary">{fmtPrice(row.output_price)}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-text-secondary">{fmtPrice(row.cached_input_price)}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-text-secondary">{fmtPrice(row.cache_creation_price)}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-text-secondary">{fmtPrice(row.per_request_price)}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <div className="ag-table-row-actions flex justify-center gap-1">
                    <Button size="sm" variant="secondary" onPress={() => openEdit(row)}>
                      <Pencil className="h-3.5 w-3.5" />
                      {t('common.edit')}
                    </Button>
                    <Button
                      className="text-danger"
                      size="sm"
                      variant="danger-soft"
                      onPress={() => setDeleteTarget(row)}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                      {t('common.delete')}
                    </Button>
                  </div>
                </CommonTable.Cell>
              </CommonTable.Row>
            ))
          )}
        </CommonTable.Body>
      </CommonTable>

      {/* 创建/编辑弹窗 */}
      <Modal state={priceDialogState}>
        <DialogTriggerShim />
        <Modal.Backdrop>
          <Modal.Container placement="center" scroll="inside" size="md">
            <Modal.Dialog className="ag-elevation-modal">
              <Modal.Header>
                <Modal.Heading>{editingPrice ? t('model_prices.edit') : t('model_prices.create')}</Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                <div className="space-y-4">
                  <HeroTextField fullWidth isRequired>
                    <Label>{t('model_prices.model')}</Label>
                    <Input
                      autoComplete="off"
                      placeholder={t('model_prices.model_placeholder')}
                      value={form.model}
                      onChange={(event) => setForm((prev) => ({ ...prev, model: event.target.value }))}
                    />
                  </HeroTextField>
                  <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                    {PRICE_FIELDS.map((field) => (
                      <HeroTextField fullWidth key={field}>
                        <Label>
                          {priceFieldLabels[field]}
                          <span className="ml-1 text-[10px] font-normal text-text-tertiary">
                            {field === 'per_request_price'
                              ? t('model_prices.unit_per_request')
                              : t('model_prices.unit_per_1m')}
                          </span>
                        </Label>
                        <Input
                          min={0}
                          placeholder="0"
                          step="any"
                          type="number"
                          value={form[field]}
                          onChange={(event) => setForm((prev) => ({ ...prev, [field]: event.target.value }))}
                        />
                      </HeroTextField>
                    ))}
                  </div>
                </div>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="secondary" onPress={closeModal}>
                  {t('common.cancel')}
                </Button>
                <Button isDisabled={saving} variant="primary" onPress={handleSubmit}>
                  {saving ? <Spinner size="sm" /> : null}
                  {editingPrice ? t('common.save') : t('common.create')}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      {/* 批量导入弹窗 */}
      <Modal state={importDialogState}>
        <DialogTriggerShim />
        <Modal.Backdrop>
          <Modal.Container placement="center" scroll="inside" size="lg">
            <Modal.Dialog className="ag-elevation-modal">
              <Modal.Header>
                <Modal.Heading>{t('model_prices.import_title')}</Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                <div className="space-y-2">
                  <p className="text-xs text-text-tertiary">{t('model_prices.import_hint')}</p>
                  <TextArea
                    aria-label={t('model_prices.import_title')}
                    className={`w-full font-mono text-xs leading-5${importError ? ' border-danger' : ''}`}
                    placeholder={'[\n  { "model": "gpt-4o", "input_price": 2.5, "output_price": 10 }\n]'}
                    rows={12}
                    value={importText}
                    onChange={(event) => setImportText(event.target.value)}
                  />
                  {importError ? <p className="text-xs text-danger">{importError}</p> : null}
                </div>
              </Modal.Body>
              <Modal.Footer>
                <Button variant="secondary" onPress={() => setImportOpen(false)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  isDisabled={importMutation.isPending}
                  variant="primary"
                  onPress={handleImport}
                >
                  {importMutation.isPending ? <Spinner size="sm" /> : null}
                  {t('model_prices.import')}
                </Button>
              </Modal.Footer>
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>

      {/* 删除确认 */}
      <AlertDialog
        isOpen={!!deleteTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
      >
        <DialogTriggerShim />
        <AlertDialog.Backdrop>
          <AlertDialog.Container placement="center" size="sm">
            <AlertDialog.Dialog className="ag-elevation-modal">
              <AlertDialog.Header>
                <AlertDialog.Icon status="danger" />
                <AlertDialog.Heading>{t('model_prices.delete_title')}</AlertDialog.Heading>
              </AlertDialog.Header>
              <AlertDialog.Body>{t('model_prices.delete_confirm', { model: deleteTarget?.model })}</AlertDialog.Body>
              <AlertDialog.Footer>
                <Button variant="secondary" onPress={() => setDeleteTarget(null)}>
                  {t('common.cancel')}
                </Button>
                <Button
                  aria-busy={deleteMutation.isPending}
                  isDisabled={deleteMutation.isPending}
                  variant="danger"
                  onPress={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
                >
                  {deleteMutation.isPending ? <Spinner size="sm" /> : null}
                  {t('common.confirm')}
                </Button>
              </AlertDialog.Footer>
            </AlertDialog.Dialog>
          </AlertDialog.Container>
        </AlertDialog.Backdrop>
      </AlertDialog>
    </div>
  );
}
