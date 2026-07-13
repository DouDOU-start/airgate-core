import { useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Button, Chip, EmptyState, Input, Label, ListBox, Modal, Select,
  Spinner, TextField as HeroTextField, ToggleButton, ToggleButtonGroup,
  useOverlayState,
} from '@heroui/react';
import { Check, Inbox, Pencil, Plus, RefreshCw, Search, Trash2, X } from 'lucide-react';
import { modelPricesApi, modelTagsApi } from '../../shared/api/modelPrices';
import { queryKeys } from '../../shared/queryKeys';
import { useCrudMutation } from '../../shared/hooks/useCrudMutation';
import { usePagination } from '../../shared/hooks/usePagination';
import { useDebouncedValue } from '../../shared/hooks/useDebouncedValue';
import { useToast } from '../../shared/ui';
import { getTotalPages } from '../../shared/utils/pagination';
import { NativeSwitch } from '../../shared/components/NativeSwitch';
import { TablePaginationFooter } from '../../shared/components/TablePaginationFooter';
import { DialogTriggerShim } from '../../shared/components/DialogTriggerShim';
import type { CreateModelPriceReq, ModelPriceResp, ModelTagResp } from '../../shared/types';
import { ConfirmDialog } from '../../shared/components/ConfirmDialog';

type Translate = (key: string) => string;

// 价格表单（价格单位 USD / 1M tokens；per_request_price 为 USD / 次）。
// 特殊计费不再让管理员手写 JSON：服务档倍率（priority/flex）与长上下文阶梯
// 拆成独立数字字段，提交时拼回 pricing_extra；表单未覆盖的扩展键原样保留。
interface PriceForm {
  model: string;
  input_price: string;
  output_price: string;
  cached_input_price: string;
  cache_creation_price: string;
  cache_creation_1h_price: string;
  per_request_price: string;
  video_per_second: string;
  tier_priority: string;
  tier_flex: string;
  lc_threshold: string;
  lc_input: string;
  lc_output: string;
  lc_cached: string;
}

const emptyForm: PriceForm = {
  model: '',
  input_price: '',
  output_price: '',
  cached_input_price: '',
  cache_creation_price: '',
  cache_creation_1h_price: '',
  per_request_price: '',
  video_per_second: '',
  tier_priority: '',
  tier_flex: '',
  lc_threshold: '',
  lc_input: '',
  lc_output: '',
  lc_cached: '',
};

type ExtraFieldKey = 'tier_priority' | 'tier_flex' | 'lc_threshold' | 'lc_input' | 'lc_output' | 'lc_cached';

// numToField 把已有扩展值转回表单字符串（>0 才回填，0/缺失视为未设置）。
function numToField(v: unknown): string {
  const n = Number(v);
  return Number.isFinite(n) && n > 0 ? String(n) : '';
}

// asRecord 宽松取对象字段：非对象（含数组）一律按空对象处理。
function asRecord(v: unknown): Record<string, unknown> {
  return v !== null && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
}

type PriceFieldKey =
  | 'input_price' | 'output_price' | 'cached_input_price'
  | 'cache_creation_price' | 'cache_creation_1h_price' | 'per_request_price';

// token 计费模式下的价格字段（按次计费时这些字段清零且不显示）。
const TOKEN_PRICE_FIELDS: readonly PriceFieldKey[] = [
  'input_price',
  'output_price',
  'cached_input_price',
  'cache_creation_price',
  'cache_creation_1h_price',
];

// 编辑弹窗的 token 价格字段分组（基础 / 缓存），让长表单有层次。
const PRICE_SECTIONS: { titleKey: string; fields: readonly PriceFieldKey[] }[] = [
  { titleKey: 'model_prices.section_base', fields: ['input_price', 'output_price'] },
  { titleKey: 'model_prices.section_cache', fields: ['cached_input_price', 'cache_creation_price', 'cache_creation_1h_price'] },
];

// 计费方式：按 Token（默认）或按次一口价。后端 per_request>0 时短路 token 计价，
// 服务档/长上下文也不参与，故按次模式下相关配置一律隐藏并清零。
type BillingMode = 'token' | 'per_request' | 'video_per_second';

function fmtPrice(value: number): string {
  if (!value) return '—';
  return `$${value}`;
}

// fmtPricePerM 单价单位为 USD / 1M tokens 的场景（缓存读取/写入），补上 /1M 后缀。
function fmtPricePerM(value: number): string {
  if (!value) return '—';
  return `${fmtPrice(value)}/1M`;
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

// longContextLine 长上下文阶梯独立行：阈值 + 各维度倍率全部写明，
// 悬浮补充计费语义（完整 prompt 超阈值时单价按倍率放大）。
function longContextLine(row: ModelPriceResp, t: Translate): ReactNode {
  const lc = row.pricing_extra?.long_context;
  if (!lc || typeof lc !== 'object' || Array.isArray(lc)) return null;
  const rec = lc as Record<string, unknown>;
  const threshold = fmtThreshold(Number(rec.threshold_tokens));
  const parts: ReactNode[] = [];
  const push = (key: string, label: string, v: unknown) => {
    const n = Number(v);
    if (!Number.isFinite(n) || n <= 0) return;
    parts.push(
      <span className="whitespace-nowrap" key={key}>
        <span className="text-text-tertiary">{label} </span>
        <span className="font-medium">×{n}</span>
      </span>,
    );
  };
  push('in', t('model_prices.lc_input'), rec.input_multiplier);
  push('out', t('model_prices.lc_output'), rec.output_multiplier);
  push('cached', t('model_prices.lc_cached'), rec.cached_multiplier);
  if (!threshold || parts.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-x-1" title={t('model_prices.long_context_hint')}>
      <span className="whitespace-nowrap text-text-tertiary">
        {t('model_prices.pricing_extra_long_context')} <span className="font-medium text-text-secondary">{threshold}</span>：
      </span>
      {parts.map((p, i) => (i === 0 ? p : [<span className="text-text-tertiary/60" key={`s${i}`}> · </span>, p]))}
    </div>
  );
}

// —— 价目卡片展示层（纯展示子组件，导出供视觉校验/测试复用）——

// PriceStat 卡片主视觉：$ 弱化、数字大字加重、/1M 单位小字；无值显示 —。
function PriceStat({ label, value }: { label: string; value: number }) {
  return (
    <div>
      <div className="text-[11px] text-text-tertiary">{label}</div>
      <div className="mt-0.5 font-mono tabular-nums">
        {value > 0 ? (
          <>
            <span className="text-sm text-text-tertiary">$</span>
            <span className="text-xl font-semibold tracking-tight text-text">{value}</span>
            <span className="ml-1 text-[10px] text-text-tertiary">/1M</span>
          </>
        ) : (
          <span className="text-xl text-text-tertiary">—</span>
        )}
      </div>
    </div>
  );
}

// cacheLine 缓存单价行：读 / 写5m / 写1h 拼成一行安静的等宽小字；无缓存价返回 null。
function cacheLine(row: ModelPriceResp, t: Translate): ReactNode {
  const parts: ReactNode[] = [];
  const push = (key: string, label: string, value: number) => {
    if (value <= 0) return;
    parts.push(
      <span className="whitespace-nowrap" key={key}>
        <span className="text-text-tertiary">{label} </span>
        <span className="font-medium">{fmtPricePerM(value)}</span>
      </span>,
    );
  };
  push('r', t('model_prices.price_short_cached'), row.cached_input_price);
  push('w5', t('model_prices.price_short_write5m'), row.cache_creation_price);
  push('w1h', t('model_prices.price_short_write1h'), row.cache_creation_1h_price);
  if (parts.length === 0) return null;
  return parts.map((p, i) => (i === 0 ? p : [<span className="text-text-tertiary/60" key={`s${i}`}> · </span>, p]));
}

// specialLine 特殊计费行：按次价（warning）/ 服务档倍率；长上下文阶梯独立成行（longContextLine）。
function specialLine(row: ModelPriceResp, t: Translate): ReactNode {
  const parts: ReactNode[] = [];
  if (row.per_request_price > 0) {
    parts.push(
      <span className="whitespace-nowrap font-medium text-warning" key="pr">
        {t('model_prices.price_short_per_request')} {fmtPrice(row.per_request_price)}
      </span>,
    );
  }
  const video = row.pricing_extra?.video;
  if (video && typeof video === 'object' && !Array.isArray(video)) {
    const perSecond = Number((video as Record<string, unknown>).per_second);
    if (Number.isFinite(perSecond) && perSecond > 0) {
      parts.push(
        <span className="whitespace-nowrap font-medium text-warning" key="vps">
          {t('model_prices.price_short_per_second')} {fmtPrice(perSecond)}
        </span>,
      );
    }
  }
  const tiers = row.pricing_extra?.service_tiers;
  if (tiers && typeof tiers === 'object' && !Array.isArray(tiers)) {
    for (const [name, mul] of Object.entries(tiers as Record<string, unknown>)) {
      const n = Number(mul);
      if (Number.isFinite(n) && n > 0) {
        parts.push(
          <span className="whitespace-nowrap" key={`t-${name}`}>
            <span className="text-text-tertiary">{tierLabel(name, t)} </span>
            <span className="font-medium">×{n}</span>
          </span>,
        );
      }
    }
  }
  if (parts.length === 0) return null;
  return parts.map((p, i) => (i === 0 ? p : [<span className="text-text-tertiary/60" key={`s${i}`}> · </span>, p]));
}

// PriceCard 官网风价格卡片：模型名 + 家族标签、输入/输出大字单价为主视觉，
// 缓存/特殊计费仅在有值时以小字行出现，底部编辑/删除操作。
export function PriceCard({ onDelete, onEdit, onToggleMarketVisible, row, t, togglingMarketVisible }: {
  onDelete: () => void;
  onEdit: () => void;
  onToggleMarketVisible: (visible: boolean) => void;
  row: ModelPriceResp;
  t: Translate;
  togglingMarketVisible?: boolean;
}) {
  const cache = cacheLine(row, t);
  const special = specialLine(row, t);
  const longContext = longContextLine(row, t);
  return (
    <div className="flex flex-col rounded-[var(--ag-radius-lg)] border border-border bg-surface p-5 transition-colors hover:border-text-tertiary/50">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate font-mono text-sm font-medium text-text" title={row.model}>{row.model}</div>
        </div>
        <div className="flex items-center gap-2">
          {row.tag ? <Chip size="sm" variant="soft">{row.tag.name}</Chip> : null}
          <span
            className="inline-flex items-center gap-1 text-[11px] text-text-tertiary"
            title={t('model_prices.market_visible_hint')}
          >
            {t('model_prices.market_visible_short')}
            <NativeSwitch
              ariaLabel={t('model_prices.market_visible')}
              isDisabled={togglingMarketVisible}
              isSelected={row.market_visible}
              onChange={onToggleMarketVisible}
            />
          </span>
        </div>
      </div>
      <div className="mt-4 grid grid-cols-2 gap-3">
        <PriceStat label={t('model_prices.price_short_input')} value={row.input_price} />
        <PriceStat label={t('model_prices.price_short_output')} value={row.output_price} />
      </div>
      {(cache || special || longContext) ? (
        <div className="mt-3 space-y-1 font-mono text-xs tabular-nums text-text-secondary">
          {cache ? <div className="flex flex-wrap gap-x-1">{cache}</div> : null}
          {special ? <div className="flex flex-wrap gap-x-1">{special}</div> : null}
          {longContext}
        </div>
      ) : null}
      <div aria-hidden className="min-h-4 flex-1" />
      <div className="flex items-center justify-end gap-1 border-t border-border pt-3">
        <Button size="sm" variant="ghost" onPress={onEdit}>
          <Pencil className="h-3.5 w-3.5" />
          {t('common.edit')}
        </Button>
        <Button className="text-danger" size="sm" variant="ghost" onPress={onDelete}>
          <Trash2 className="h-3.5 w-3.5" />
          {t('common.delete')}
        </Button>
      </div>
    </div>
  );
}

export default function ModelPricesPage() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const { page, setPage, pageSize, setPageSize } = usePagination(20, 'admin.model-prices');
  const [keyword, setKeyword] = useState('');
  const debouncedKeyword = useDebouncedValue(keyword.trim(), 250);
  // 标签过滤（null = 全部）：点标签管理块里的标签名切换。
  const [tagFilter, setTagFilter] = useState<number | null>(null);

  const [modalOpen, setModalOpen] = useState(false);
  const [editingPrice, setEditingPrice] = useState<ModelPriceResp | null>(null);
  const [form, setForm] = useState<PriceForm>(emptyForm);
  // 表单未覆盖的 pricing_extra 内容（未知顶层键 / priority、flex 之外的服务档名），
  // 编辑时暂存、保存时原样拼回，保证表单化不丢数据。
  const [extraRest, setExtraRest] = useState<Record<string, unknown>>({});
  const [tiersRest, setTiersRest] = useState<Record<string, unknown>>({});
  // 服务档倍率 / 长上下文各自独立开关：关闭时不显示字段、保存时不写入对应配置；
  // 编辑已配置的模型时自动打开对应开关。
  const [tiersEnabled, setTiersEnabled] = useState(false);
  const [lcEnabled, setLcEnabled] = useState(false);
  // 计费方式切换：token（按量）/ per_request（按次一口价）。
  const [billingMode, setBillingMode] = useState<BillingMode>('token');
  // 表单选中的标签 ID（0 = 无标签）。
  const [formTagID, setFormTagID] = useState(0);
  // 标签管理块的交互状态：新建输入 / 行内重命名 / 删除确认目标。
  const [newTagName, setNewTagName] = useState('');
  const [editingTag, setEditingTag] = useState<{ id: number; name: string } | null>(null);
  const [deleteTagTarget, setDeleteTagTarget] = useState<ModelTagResp | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ModelPriceResp | null>(null);

  const listQuery = useMemo(() => ({
    page,
    page_size: pageSize,
    keyword: debouncedKeyword || undefined,
    tag_id: tagFilter ?? undefined,
  }), [page, pageSize, debouncedKeyword, tagFilter]);

  const { data, isLoading, refetch } = useQuery({
    queryKey: queryKeys.modelPrices(listQuery),
    queryFn: () => modelPricesApi.list(listQuery),
    placeholderData: keepPreviousData,
  });

  const tagsQuery = useQuery({ queryKey: queryKeys.modelTags(), queryFn: modelTagsApi.list });
  const tags = useMemo(() => tagsQuery.data ?? [], [tagsQuery.data]);

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

  // 广场可见开关：卡片上直接点击，不弹窗、不出成功提示（同渠道 key 启停交互）。
  const marketVisibleMutation = useMutation({
    mutationFn: ({ id, market_visible }: { id: number; market_visible: boolean }) =>
      modelPricesApi.update(id, { market_visible }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.modelPrices() });
    },
    onError: (err: Error) => toast('error', err.message),
  });

  // 标签重命名/删除会改动卡片上的标签显示，一并失效价目列表。
  const invalidateTagsAndPrices = () => {
    queryClient.invalidateQueries({ queryKey: queryKeys.modelTags() });
    queryClient.invalidateQueries({ queryKey: queryKeys.modelPrices() });
  };

  const createTagMutation = useCrudMutation({
    mutationFn: (name: string) => modelTagsApi.create(name),
    successMessage: t('model_prices.tag_create_success'),
    queryKey: queryKeys.modelTags(),
    onSuccess: () => setNewTagName(''),
  });

  const renameTagMutation = useCrudMutation({
    mutationFn: ({ id, name }: { id: number; name: string }) => modelTagsApi.update(id, name),
    successMessage: t('model_prices.tag_update_success'),
    queryKey: queryKeys.modelTags(),
    onSuccess: () => {
      setEditingTag(null);
      invalidateTagsAndPrices();
    },
  });

  const deleteTagMutation = useCrudMutation({
    mutationFn: (id: number) => modelTagsApi.delete(id),
    successMessage: t('model_prices.tag_delete_success'),
    queryKey: queryKeys.modelTags(),
    onSuccess: (_, id) => {
      setDeleteTagTarget(null);
      // 删除的正是当前筛选标签时回到「全部」，避免列表停在空筛选
      setTagFilter((prev) => (prev === id ? null : prev));
      invalidateTagsAndPrices();
    },
  });

  function submitNewTag() {
    const name = newTagName.trim();
    if (!name) return;
    createTagMutation.mutate(name);
  }

  function commitRenameTag() {
    if (!editingTag) return;
    const name = editingTag.name.trim();
    if (!name) return;
    renameTagMutation.mutate({ id: editingTag.id, name });
  }

  function openCreate() {
    setEditingPrice(null);
    setForm(emptyForm);
    setExtraRest({});
    setTiersRest({});
    setTiersEnabled(false);
    setLcEnabled(false);
    setBillingMode('token');
    setFormTagID(0);
    setModalOpen(true);
  }

  function openEdit(price: ModelPriceResp) {
    const extra = asRecord(price.pricing_extra);
    const { long_context: lcRaw, service_tiers: tiersRaw, video: videoRaw, ...restExtra } = extra;
    const { flex, priority, ...restTiers } = asRecord(tiersRaw);
    const lc = asRecord(lcRaw);
    const videoPerSecond = numToField(asRecord(videoRaw).per_second);

    setEditingPrice(price);
    setExtraRest(restExtra);
    setTiersRest(restTiers);
    setTiersEnabled(
      Object.keys(restTiers).length > 0
      || numToField(priority) !== ''
      || numToField(flex) !== '',
    );
    setLcEnabled(numToField(lc.threshold_tokens) !== '');
    setBillingMode(price.per_request_price > 0 ? 'per_request' : videoPerSecond !== '' ? 'video_per_second' : 'token');
    setFormTagID(price.tag?.id ?? 0);
    setForm({
      model: price.model,
      input_price: String(price.input_price),
      output_price: String(price.output_price),
      cached_input_price: String(price.cached_input_price),
      cache_creation_price: String(price.cache_creation_price),
      cache_creation_1h_price: String(price.cache_creation_1h_price),
      per_request_price: String(price.per_request_price),
      video_per_second: videoPerSecond,
      tier_priority: numToField(priority),
      tier_flex: numToField(flex),
      lc_threshold: numToField(lc.threshold_tokens),
      lc_input: numToField(lc.input_multiplier),
      lc_output: numToField(lc.output_multiplier),
      lc_cached: numToField(lc.cached_multiplier),
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

    // 按次计费：只收按次价（必须 >0），token 价格全部清零；
    // 后端 per_request>0 时短路 token 计价，服务档/长上下文不参与，一并不写入。
    if (billingMode === 'per_request') {
      const raw = form.per_request_price.trim();
      const num = raw === '' ? 0 : Number(raw);
      if (!Number.isFinite(num) || num <= 0) {
        toast('error', t('model_prices.per_request_required'));
        return;
      }
      payload.per_request_price = num;
      for (const field of TOKEN_PRICE_FIELDS) payload[field] = 0;
      payload.pricing_extra = { ...extraRest };
      payload.tag_id = formTagID;
      if (editingPrice) {
        updateMutation.mutate({ id: editingPrice.id, payload });
      } else {
        createMutation.mutate(payload);
      }
      return;
    }

    // 视频按秒计费（任务子系统）：只收每秒单价（必须 >0），写入
    // pricing_extra.video.per_second；token 价格与按次价全部清零。
    if (billingMode === 'video_per_second') {
      const raw = form.video_per_second.trim();
      const num = raw === '' ? 0 : Number(raw);
      if (!Number.isFinite(num) || num <= 0) {
        toast('error', t('model_prices.video_per_second_required'));
        return;
      }
      payload.per_request_price = 0;
      for (const field of TOKEN_PRICE_FIELDS) payload[field] = 0;
      payload.pricing_extra = { ...extraRest, video: { per_second: num } };
      payload.tag_id = formTagID;
      if (editingPrice) {
        updateMutation.mutate({ id: editingPrice.id, payload });
      } else {
        createMutation.mutate(payload);
      }
      return;
    }

    // 按 Token 计费：收 token 价格字段，按次价清零。
    for (const field of TOKEN_PRICE_FIELDS) {
      const raw = form[field];
      const num = raw === '' ? 0 : Number(raw);
      if (!Number.isFinite(num) || num < 0) {
        toast('error', t('model_prices.price_invalid'));
        return;
      }
      payload[field] = num;
    }
    payload.per_request_price = 0;
    payload.tag_id = formTagID;
    // 服务档倍率 / 长上下文按各自开关独立写入：开关关闭即不写对应键（等于清空），
    // 表单未覆盖的未知顶层扩展键（extraRest）始终原样保留。
    const parseExtraField = (field: ExtraFieldKey): number | null => {
      const raw = form[field].trim();
      const num = raw === '' ? 0 : Number(raw);
      return Number.isFinite(num) && num >= 0 ? num : null;
    };
    const extra: Record<string, unknown> = { ...extraRest };
    if (tiersEnabled) {
      const priority = parseExtraField('tier_priority');
      const flex = parseExtraField('tier_flex');
      if (priority === null || flex === null) {
        toast('error', t('model_prices.price_invalid'));
        return;
      }
      const tiers: Record<string, unknown> = { ...tiersRest };
      if (priority > 0) tiers.priority = priority;
      if (flex > 0) tiers.flex = flex;
      if (Object.keys(tiers).length > 0) extra.service_tiers = tiers;
    }
    if (lcEnabled) {
      const threshold = parseExtraField('lc_threshold');
      const inMul = parseExtraField('lc_input');
      const outMul = parseExtraField('lc_output');
      const cachedMul = parseExtraField('lc_cached');
      if (threshold === null || inMul === null || outMul === null || cachedMul === null) {
        toast('error', t('model_prices.price_invalid'));
        return;
      }
      if (threshold > 0) {
        // 后端要求 long_context 四字段齐全，倍率留空按 ×1（标准价）落库。
        extra.long_context = {
          threshold_tokens: threshold,
          input_multiplier: inMul > 0 ? inMul : 1,
          output_multiplier: outMul > 0 ? outMul : 1,
          cached_multiplier: cachedMul > 0 ? cachedMul : 1,
        };
      }
    }
    payload.pricing_extra = extra;
    if (editingPrice) {
      updateMutation.mutate({ id: editingPrice.id, payload });
    } else {
      createMutation.mutate(payload);
    }
  }

  const saving = createMutation.isPending || updateMutation.isPending;
  const priceDialogState = useOverlayState({
    isOpen: modalOpen,
    onOpenChange: (open) => {
      if (!open) closeModal();
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

  // renderExtraField 特殊计费数字字段：留空表示不启用（placeholder「不设」）。
  function renderExtraField(field: ExtraFieldKey, label: string) {
    return (
      <HeroTextField fullWidth key={field}>
        <Label>{label}</Label>
        <Input
          min={0}
          placeholder={t('model_prices.not_set')}
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
          <Button variant="primary" onPress={openCreate}>
            <Plus className="h-4 w-4" />
            {t('model_prices.create')}
          </Button>
        </div>
      </div>

      {/* 标签管理：模型管理下的一小块（新增 / 重命名 / 删除，含模型计数；点标签名筛选模型） */}
      <div className="mb-4 flex flex-wrap items-center gap-2 rounded-[var(--ag-radius-lg)] border border-border bg-surface px-4 py-2.5">
        <span className="text-xs font-medium text-text-tertiary">{t('model_prices.tags_title')}</span>
        <button
          className={`rounded-[var(--radius)] border px-2 py-1 text-xs transition-colors ${
            tagFilter == null
              ? 'border-primary bg-primary-subtle font-medium text-primary'
              : 'border-border bg-bg text-text-secondary hover:text-text'
          }`}
          type="button"
          onClick={() => {
            setPage(1);
            setTagFilter(null);
          }}
        >
          {t('common.all')}
        </button>
        {tags.map((tag) => (editingTag?.id === tag.id ? (
          <span className="inline-flex items-center gap-1" key={tag.id}>
            <Input
              autoFocus
              aria-label={t('model_prices.tag')}
              style={{ width: 120 }}
              value={editingTag.name}
              onChange={(event) => setEditingTag({ id: tag.id, name: event.target.value })}
              onKeyDown={(event) => {
                if (event.key === 'Enter') commitRenameTag();
                if (event.key === 'Escape') setEditingTag(null);
              }}
            />
            <Button isIconOnly aria-label={t('common.save')} size="sm" variant="ghost" onPress={commitRenameTag}>
              <Check className="h-3.5 w-3.5" />
            </Button>
            <Button isIconOnly aria-label={t('common.cancel')} size="sm" variant="ghost" onPress={() => setEditingTag(null)}>
              <X className="h-3.5 w-3.5" />
            </Button>
          </span>
        ) : (
          <span
            className={`inline-flex items-center gap-1.5 rounded-[var(--radius)] border px-2 py-1 text-xs ${
              tagFilter === tag.id ? 'border-primary bg-primary-subtle' : 'border-border bg-bg'
            }`}
            key={tag.id}
          >
            <button
              className="inline-flex items-center gap-1.5"
              title={t('model_prices.tag_filter_hint')}
              type="button"
              onClick={() => {
                setPage(1);
                setTagFilter(tagFilter === tag.id ? null : tag.id);
              }}
            >
              <span className={`font-medium ${tagFilter === tag.id ? 'text-primary' : 'text-text'}`}>{tag.name}</span>
              <span className="font-mono tabular-nums text-text-tertiary">{tag.model_count}</span>
            </button>
            <button
              aria-label={t('common.edit')}
              className="text-text-tertiary transition-colors hover:text-text"
              type="button"
              onClick={() => setEditingTag({ id: tag.id, name: tag.name })}
            >
              <Pencil className="h-3 w-3" />
            </button>
            <button
              aria-label={t('common.delete')}
              className="text-text-tertiary transition-colors hover:text-danger"
              type="button"
              onClick={() => setDeleteTagTarget(tag)}
            >
              <Trash2 className="h-3 w-3" />
            </button>
          </span>
        )))}
        <span className="inline-flex items-center gap-1">
          <Input
            aria-label={t('model_prices.tag_add_placeholder')}
            placeholder={t('model_prices.tag_add_placeholder')}
            style={{ width: 120 }}
            value={newTagName}
            onChange={(event) => setNewTagName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') submitNewTag();
            }}
          />
          <Button
            isIconOnly
            aria-label={t('model_prices.tag_add')}
            isDisabled={createTagMutation.isPending || !newTagName.trim()}
            size="sm"
            variant="ghost"
            onPress={submitNewTag}
          >
            <Plus className="h-3.5 w-3.5" />
          </Button>
        </span>
      </div>

      {isLoading ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 6 }, (_, i) => (
            <div className="animate-pulse rounded-[var(--ag-radius-lg)] border border-border bg-surface p-5" key={i}>
              <div className="h-4 w-2/3 rounded bg-border/60" />
              <div className="mt-2 h-3 w-16 rounded bg-border/40" />
              <div className="mt-5 grid grid-cols-2 gap-3">
                <div className="h-8 rounded bg-border/50" />
                <div className="h-8 rounded bg-border/50" />
              </div>
              <div className="mt-6 h-8 rounded bg-border/30" />
            </div>
          ))}
        </div>
      ) : rows.length === 0 ? (
        <div className="rounded-[var(--ag-radius-lg)] border border-border bg-surface py-16">
          <EmptyState>
            <span className="flex h-10 w-10 items-center justify-center rounded-full border border-border bg-surface">
              <Inbox className="h-5 w-5 text-text-tertiary" />
            </span>
            <div className="text-sm text-text-secondary">{t('common.no_data')}</div>
            <div className="text-xs text-text-tertiary">{t('model_prices.empty_hint')}</div>
          </EmptyState>
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {rows.map((row) => (
            <PriceCard
              key={row.id}
              row={row}
              t={t}
              togglingMarketVisible={marketVisibleMutation.isPending && marketVisibleMutation.variables?.id === row.id}
              onDelete={() => setDeleteTarget(row)}
              onEdit={() => openEdit(row)}
              onToggleMarketVisible={(visible) => marketVisibleMutation.mutate({ id: row.id, market_visible: visible })}
            />
          ))}
        </div>
      )}

      <div className="mt-4 rounded-[var(--ag-radius-lg)] border border-border bg-surface px-4 py-2">
        <TablePaginationFooter
          page={page}
          pageSize={pageSize}
          setPage={setPage}
          setPageSize={setPageSize}
          total={total}
          totalPages={totalPages}
        />
      </div>

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

                  <HeroTextField fullWidth>
                    <Label>{t('model_prices.tag')}</Label>
                    <Select
                      aria-label={t('model_prices.tag')}
                      fullWidth
                      selectedKey={String(formTagID)}
                      onSelectionChange={(key) => setFormTagID(key == null ? 0 : Number(key))}
                    >
                      <Select.Trigger>
                        <Select.Value>
                          {formTagID > 0
                            ? tags.find((tag) => tag.id === formTagID)?.name ?? String(formTagID)
                            : t('model_prices.tag_none')}
                        </Select.Value>
                        <Select.Indicator />
                      </Select.Trigger>
                      <Select.Popover>
                        <ListBox>
                          <ListBox.Item id="0" textValue={t('model_prices.tag_none')}>
                            {t('model_prices.tag_none')}
                          </ListBox.Item>
                          {tags.map((tag) => (
                            <ListBox.Item id={String(tag.id)} key={tag.id} textValue={tag.name}>
                              {tag.name}
                            </ListBox.Item>
                          ))}
                        </ListBox>
                      </Select.Popover>
                    </Select>
                  </HeroTextField>

                  <div className="space-y-2">
                    <p className="text-xs font-semibold uppercase tracking-wide text-text-tertiary">
                      {t('model_prices.section_billing_mode')}
                    </p>
                    <ToggleButtonGroup
                      disallowEmptySelection
                      selectedKeys={[billingMode]}
                      selectionMode="single"
                      onSelectionChange={(keys) => {
                        const key = [...keys][0];
                        if (key === 'token' || key === 'per_request' || key === 'video_per_second') setBillingMode(key);
                      }}
                    >
                      <ToggleButton id="token">{t('model_prices.billing_mode_token')}</ToggleButton>
                      <ToggleButton id="per_request">{t('model_prices.billing_mode_per_request')}</ToggleButton>
                      <ToggleButton id="video_per_second">{t('model_prices.billing_mode_video')}</ToggleButton>
                    </ToggleButtonGroup>
                  </div>

                  {billingMode === 'per_request' ? (
                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                      {renderPriceField('per_request_price')}
                    </div>
                  ) : billingMode === 'video_per_second' ? (
                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                      <HeroTextField fullWidth>
                        <Label>
                          {t('model_prices.video_per_second')}
                          <span className="ml-1 text-[10px] font-normal text-text-tertiary">
                            {t('model_prices.unit_per_second')}
                          </span>
                        </Label>
                        <Input
                          min={0}
                          placeholder="0"
                          step="any"
                          type="number"
                          value={form.video_per_second}
                          onChange={(event) => setForm((prev) => ({ ...prev, video_per_second: event.target.value }))}
                        />
                      </HeroTextField>
                    </div>
                  ) : PRICE_SECTIONS.map((section) => (
                    <div className="space-y-2" key={section.titleKey}>
                      <p className="text-xs font-semibold uppercase tracking-wide text-text-tertiary">
                        {t(section.titleKey)}
                      </p>
                      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                        {section.fields.map((field) => renderPriceField(field))}
                      </div>
                    </div>
                  ))}

                  {billingMode === 'token' ? <div className="space-y-3 border-t border-border pt-4">
                    {Object.keys(extraRest).length > 0 || Object.keys(tiersRest).length > 0 ? (
                      <p className="text-[11px] leading-4 text-warning">{t('model_prices.extra_preserved')}</p>
                    ) : null}
                    <div className="space-y-2">
                      <div className="flex items-center justify-between gap-2">
                        <p className="text-xs font-semibold uppercase tracking-wide text-text-tertiary">
                          {t('model_prices.section_service_tiers')}
                        </p>
                        <NativeSwitch
                          ariaLabel={t('model_prices.section_service_tiers')}
                          isSelected={tiersEnabled}
                          onChange={setTiersEnabled}
                        />
                      </div>
                      {tiersEnabled ? (
                        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                          {renderExtraField('tier_priority', `${t('model_prices.tier_priority')} ×`)}
                          {renderExtraField('tier_flex', `${t('model_prices.tier_flex')} ×`)}
                        </div>
                      ) : null}
                    </div>
                    <div className="space-y-2 border-t border-border pt-3">
                      <div className="flex items-center justify-between gap-2">
                        <p className="text-xs font-semibold uppercase tracking-wide text-text-tertiary">
                          {t('model_prices.pricing_extra_long_context')}
                        </p>
                        <NativeSwitch
                          ariaLabel={t('model_prices.pricing_extra_long_context')}
                          isSelected={lcEnabled}
                          onChange={setLcEnabled}
                        />
                      </div>
                      {lcEnabled ? (
                        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                          {renderExtraField('lc_threshold', t('model_prices.lc_threshold'))}
                          {renderExtraField('lc_input', `${t('model_prices.lc_input')} ×`)}
                          {renderExtraField('lc_output', `${t('model_prices.lc_output')} ×`)}
                          {renderExtraField('lc_cached', `${t('model_prices.lc_cached')} ×`)}
                        </div>
                      ) : null}
                    </div>
                  </div> : null}
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

      {/* 删除标签确认 */}
      <ConfirmDialog
        open={!!deleteTagTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteTagTarget(null);
        }}
        title={t('model_prices.tag_delete_title')}
        description={t('model_prices.tag_delete_confirm', { name: deleteTagTarget?.name })}
        status="warning"
        loading={deleteTagMutation.isPending}
        onConfirm={() => deleteTagTarget && deleteTagMutation.mutate(deleteTagTarget.id)}
      />

      {/* 删除确认 */}
      <ConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        title={t('model_prices.delete_title')}
        description={t('model_prices.delete_confirm', { model: deleteTarget?.model })}
        loading={deleteMutation.isPending}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
      />
    </div>
  );
}
