import { useMemo, useState, type ReactNode } from 'react';
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

type Translate = (key: string) => string;

const COLUMN_COUNT = 6;

// 价格表单（价格单位 USD / 1M tokens；per_request_price 为 USD / 次）。
// pricing_extra 为 JSON 文本域（服务档倍率 + 长上下文阶梯等长尾维度，可空）。
interface PriceForm {
  model: string;
  input_price: string;
  output_price: string;
  cached_input_price: string;
  cache_creation_price: string;
  cache_creation_1h_price: string;
  per_request_price: string;
  pricing_extra: string;
}

const emptyForm: PriceForm = {
  model: '',
  input_price: '',
  output_price: '',
  cached_input_price: '',
  cache_creation_price: '',
  cache_creation_1h_price: '',
  per_request_price: '',
  pricing_extra: '',
};

type PriceFieldKey =
  | 'input_price' | 'output_price' | 'cached_input_price'
  | 'cache_creation_price' | 'cache_creation_1h_price' | 'per_request_price';

const PRICE_FIELDS: readonly PriceFieldKey[] = [
  'input_price',
  'output_price',
  'cached_input_price',
  'cache_creation_price',
  'cache_creation_1h_price',
  'per_request_price',
];

// 编辑弹窗的价格字段分组（基础 / 缓存 / 计费模式），让长表单有层次。
const PRICE_SECTIONS: { titleKey: string; fields: readonly PriceFieldKey[] }[] = [
  { titleKey: 'model_prices.section_base', fields: ['input_price', 'output_price'] },
  { titleKey: 'model_prices.section_cache', fields: ['cached_input_price', 'cache_creation_price', 'cache_creation_1h_price'] },
  { titleKey: 'model_prices.section_billing_mode', fields: ['per_request_price'] },
];

// pricing_extra 一键模板，方便管理员照着填服务档 / 长上下文。
const PRICING_EXTRA_TEMPLATE = JSON.stringify({
  service_tiers: { priority: 2.0, flex: 0.5 },
  long_context: { threshold_tokens: 272000, input_multiplier: 2.0, output_multiplier: 1.5, cached_multiplier: 2.0 },
}, null, 2);

function fmtPrice(value: number): string {
  if (!value) return '—';
  return `$${value}`;
}

// fmtThreshold 把阈值 token 数缩写为 272K 之类。
function fmtThreshold(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '';
  return value >= 1000 ? `>${value / 1000}K` : `>${value}`;
}

// tierLabel 已知服务档名译为中文（优先 / 弹性），未知档名原样显示。
function tierLabel(name: string, t: Translate): string {
  const key = `model_prices.tier_${name}`;
  const label = t(key);
  return label === key ? name : label;
}

// formatLongContext 把 long_context 阶梯拼成完整 tooltip 文案。
function formatLongContext(extra: Record<string, unknown> | undefined, t: Translate): string {
  const lc = extra?.long_context;
  if (!lc || typeof lc !== 'object' || Array.isArray(lc)) return '';
  const rec = lc as Record<string, unknown>;
  const parts: string[] = [];
  const push = (label: string, v: unknown) => {
    const n = Number(v);
    if (Number.isFinite(n) && n > 0) parts.push(`${label}×${n}`);
  };
  push(t('model_prices.lc_input'), rec.input_multiplier);
  push(t('model_prices.lc_output'), rec.output_multiplier);
  push(t('model_prices.lc_cached'), rec.cached_multiplier);
  const threshold = fmtThreshold(Number(rec.threshold_tokens));
  return [threshold, parts.join(' / ')].filter(Boolean).join(' ');
}

// CacheCell 缓存单价列：缓存读取一行、写入 5m/1h 折成一行；无缓存价显示 —。
// 灰色小字呈现，主视觉留给输入/输出单价。
function CacheCell({ row, t }: { row: ModelPriceResp; t: Translate }) {
  const lines: string[] = [];
  if (row.cached_input_price > 0) {
    lines.push(`${t('model_prices.price_short_cached')} ${fmtPrice(row.cached_input_price)}`);
  }
  const writes: string[] = [];
  if (row.cache_creation_price > 0) {
    writes.push(`${t('model_prices.price_short_write5m')} ${fmtPrice(row.cache_creation_price)}`);
  }
  if (row.cache_creation_1h_price > 0) {
    writes.push(`${t('model_prices.price_short_write1h')} ${fmtPrice(row.cache_creation_1h_price)}`);
  }
  if (writes.length > 0) lines.push(writes.join(' · '));

  if (lines.length === 0) return <span className="text-text-tertiary">—</span>;
  return (
    <div className="flex flex-col gap-0.5 font-mono text-xs tabular-nums text-text-secondary">
      {lines.map((line, i) => <span key={i}>{line}</span>)}
    </div>
  );
}

// SpecialBillingCell 特殊计费列：按次价 / 服务档倍率 / 长上下文阶梯；都没有时显示"标准计费"。
// 同样以低调灰字呈现，不喧宾夺主。
function SpecialBillingCell({ row, t }: { row: ModelPriceResp; t: Translate }) {
  const items: ReactNode[] = [];
  if (row.per_request_price > 0) {
    items.push(
      <span className="font-mono tabular-nums text-warning" key="pr">
        {t('model_prices.price_short_per_request')} {fmtPrice(row.per_request_price)}
      </span>,
    );
  }

  const extra = row.pricing_extra;
  const tiers = extra?.service_tiers;
  if (tiers && typeof tiers === 'object' && !Array.isArray(tiers)) {
    for (const [name, mul] of Object.entries(tiers as Record<string, unknown>)) {
      const n = Number(mul);
      if (Number.isFinite(n) && n > 0) {
        items.push(
          <span key={`t-${name}`}>
            {tierLabel(name, t)} <span className="font-mono tabular-nums">×{n}</span>
          </span>,
        );
      }
    }
  }

  const lcFull = formatLongContext(extra, t);
  if (lcFull) {
    const rec = extra?.long_context as Record<string, unknown> | undefined;
    const short = fmtThreshold(Number(rec?.threshold_tokens));
    items.push(
      <span key="lc" title={lcFull}>
        {t('model_prices.pricing_extra_long_context')} {short}
      </span>,
    );
  }

  if (items.length === 0) {
    return <span className="text-text-tertiary">{t('model_prices.billing_standard')}</span>;
  }
  return <div className="flex flex-col gap-0.5 text-xs text-text-secondary">{items}</div>;
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
    const extra = record.pricing_extra;
    if (extra !== undefined && extra !== null) {
      if (typeof extra !== 'object' || Array.isArray(extra)) throw new Error('bad pricing_extra');
      item.pricing_extra = extra as Record<string, unknown>;
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
      cache_creation_1h_price: String(price.cache_creation_1h_price),
      per_request_price: String(price.per_request_price),
      pricing_extra: price.pricing_extra && Object.keys(price.pricing_extra).length > 0
        ? JSON.stringify(price.pricing_extra, null, 2)
        : '',
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
    // pricing_extra：空文本视为清空（{}）；非空须为合法 JSON 对象。
    const extraText = form.pricing_extra.trim();
    if (extraText === '') {
      payload.pricing_extra = {};
    } else {
      let parsed: unknown;
      try {
        parsed = JSON.parse(extraText);
      } catch {
        toast('error', t('model_prices.pricing_extra_invalid'));
        return;
      }
      if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
        toast('error', t('model_prices.pricing_extra_invalid'));
        return;
      }
      payload.pricing_extra = parsed as Record<string, unknown>;
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

  const priceFieldLabels: Record<PriceFieldKey, string> = {
    input_price: t('model_prices.input_price'),
    output_price: t('model_prices.output_price'),
    cached_input_price: t('model_prices.cached_input_price'),
    cache_creation_price: t('model_prices.cache_creation_price'),
    cache_creation_1h_price: t('model_prices.cache_creation_1h_price'),
    per_request_price: t('model_prices.per_request_price'),
  };

  function renderPriceField(field: PriceFieldKey) {
    return (
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
    );
  }

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
        minWidth={820}
      >
        <CommonTable.Header>
          <CommonTable.Column id="model" style={{ width: 240 }}>{t('model_prices.model')}</CommonTable.Column>
          <CommonTable.Column id="input" style={{ width: 120 }}>
            {t('model_prices.price_short_input')}
            <span className="block text-[10px] font-normal text-text-tertiary">{t('model_prices.unit_per_1m')}</span>
          </CommonTable.Column>
          <CommonTable.Column id="output" style={{ width: 120 }}>
            {t('model_prices.price_short_output')}
            <span className="block text-[10px] font-normal text-text-tertiary">{t('model_prices.unit_per_1m')}</span>
          </CommonTable.Column>
          <CommonTable.Column id="cache" style={{ width: 200 }}>{t('model_prices.col_cache')}</CommonTable.Column>
          <CommonTable.Column id="special_billing">
            {t('model_prices.col_special_billing')}
            <span className="block text-[10px] font-normal text-text-tertiary">{t('model_prices.col_special_billing_hint')}</span>
          </CommonTable.Column>
          <CommonTable.Column id="actions" style={{ width: 168 }}>{t('common.actions')}</CommonTable.Column>
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
                  <span className="font-mono text-sm text-text" title={row.model}>{row.model}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-sm tabular-nums text-text">{fmtPrice(row.input_price)}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <span className="font-mono text-sm tabular-nums text-text">{fmtPrice(row.output_price)}</span>
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <CacheCell row={row} t={t} />
                </CommonTable.Cell>
                <CommonTable.Cell>
                  <SpecialBillingCell row={row} t={t} />
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
                <div className="space-y-5">
                  <HeroTextField fullWidth isRequired>
                    <Label>{t('model_prices.model')}</Label>
                    <Input
                      autoComplete="off"
                      placeholder={t('model_prices.model_placeholder')}
                      value={form.model}
                      onChange={(event) => setForm((prev) => ({ ...prev, model: event.target.value }))}
                    />
                  </HeroTextField>

                  {PRICE_SECTIONS.map((section) => (
                    <div className="space-y-2" key={section.titleKey}>
                      <p className="text-xs font-semibold uppercase tracking-wide text-text-tertiary">
                        {t(section.titleKey)}
                      </p>
                      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                        {section.fields.map((field) => renderPriceField(field))}
                      </div>
                    </div>
                  ))}

                  <div className="space-y-2 border-t border-border pt-4">
                    <div className="flex items-center justify-between gap-2">
                      <p className="text-xs font-semibold uppercase tracking-wide text-text-tertiary">
                        {t('model_prices.section_advanced')}
                      </p>
                      <Button
                        size="sm"
                        variant="ghost"
                        onPress={() => setForm((prev) => ({ ...prev, pricing_extra: PRICING_EXTRA_TEMPLATE }))}
                      >
                        {t('model_prices.pricing_extra_template')}
                      </Button>
                    </div>
                    <p className="text-[11px] leading-4 text-text-tertiary">{t('model_prices.pricing_extra_hint')}</p>
                    <TextArea
                      aria-label={t('model_prices.pricing_extra')}
                      className="w-full font-mono text-xs leading-5"
                      placeholder={'{\n  "service_tiers": { "priority": 2.0, "flex": 0.5 }\n}'}
                      rows={6}
                      value={form.pricing_extra}
                      onChange={(event) => setForm((prev) => ({ ...prev, pricing_extra: event.target.value }))}
                    />
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
