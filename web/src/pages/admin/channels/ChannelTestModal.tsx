import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Input, Modal, Spinner, useOverlayState } from '@heroui/react';
import { CircleCheck, CircleX, DownloadCloud, Play, Plus, Square, X, Zap } from 'lucide-react';
import { channelsApi } from '../../../shared/api/channels';
import { isAbortError } from '../../../shared/api/client';
import { modelPricesApi } from '../../../shared/api/modelPrices';
import { queryKeys } from '../../../shared/queryKeys';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import { useToast } from '../../../shared/ui';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import type { ChannelKeyResp, ChannelKeyReq } from '../../../shared/types';

type ModelTestState =
  | { status: 'testing' }
  | { status: 'ok'; latencyMs: number }
  | { status: 'fail'; error: string };

// 模型草稿：弹窗内编辑的字段（保存时以 partial update 提交，与编辑表单互不覆盖）。
// mapping 键 = 公开模型名（左列），值 = 渠道（上游）模型名（右列）；
// 右列 UI 上默认回显同名，保存时同名/空值不落库（原名透传），键不在清单内的条目丢弃。
interface ModelsDraft {
  models: string[];
  mapping: Record<string, string>;
}

function draftFromKey(key: ChannelKeyResp): ModelsDraft {
  return {
    models: key.models ?? [],
    mapping: { ...(key.model_mapping ?? {}) },
  };
}

// 归一化映射：仅保留清单内、值非空且与公开名不同的条目（即保存载荷）
function normalizedMapping(draft: ModelsDraft): Record<string, string> {
  const result: Record<string, string> = {};
  for (const model of draft.models) {
    const target = (draft.mapping[model] ?? '').trim();
    if (target && target !== model) result[model] = target;
  }
  return result;
}

// 拉全量模型目录名（价目表按 page_size≤100 分页，翻页收集）：公开模型必须已在模型管理建价
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

// 草稿签名：用于脏检测（映射按归一化结果比较，敲空格不算修改）
function draftSignature(draft: ModelsDraft): string {
  return JSON.stringify({
    models: draft.models,
    mapping: normalizedMapping(draft),
  });
}

// 模型名行内编辑：本地暂存输入，失焦/回车提交；onCommit 返回 false（空值/重名/不在目录）回退原值。
// 提交制而非受控直改，避免逐键改名导致映射键迁移和测试结果失配的中间态。
function ModelNameField({
  ariaLabel,
  value,
  tone,
  onCommit,
}: {
  ariaLabel: string;
  value: string;
  tone?: 'danger' | 'warning';
  onCommit: (next: string) => boolean;
}) {
  const [text, setText] = useState(value);

  useEffect(() => setText(value), [value]);

  function commit() {
    if (text.trim() === value) {
      setText(value);
      return;
    }
    if (!onCommit(text)) setText(value);
  }

  // Tailwind 按字面量扫描类名，tone 样式须写全
  const toneClass = tone === 'danger' ? ' border-danger text-danger' : tone === 'warning' ? ' border-warning text-warning' : '';

  return (
    <Input
      aria-label={ariaLabel}
      className={`min-w-0 flex-1 font-mono text-xs${toneClass}`}
      value={text}
      onBlur={commit}
      onChange={(event) => setText(event.target.value)}
      onKeyDown={(event) => {
        if (event.key === 'Enter') (event.target as HTMLInputElement).blur();
      }}
    />
  );
}

/**
 * 模型与测试弹窗：单把 key 的模型清单与模型映射（行内）在此管理（编辑表单不再承载），
 * 并支持逐个测试指定模型 /「测试全部」串行跑完整个清单（可中途停止）。
 * 每次测试都走后端 POST /channels/keys/:id/test（真实 relay 请求），未保存的新模型也可先测再存；
 * 成功会刷新该 key 的 response_time / 自动禁用恢复。
 */
export function ChannelTestModal({
  channelKey,
  onClose,
}: {
  channelKey: ChannelKeyResp | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const { toast } = useToast();
  const queryClient = useQueryClient();

  const [draft, setDraft] = useState<ModelsDraft>({ models: [], mapping: {} });
  // 脏检测基线：打开时取渠道当前值，保存成功后前移
  const [baseline, setBaseline] = useState('');
  const [modelInput, setModelInput] = useState('');
  const [results, setResults] = useState<Record<string, ModelTestState>>({});
  const [runningAll, setRunningAll] = useState(false);
  // 测试端点（仅 openai 协议渠道展示切换）：gpt 系上游可能只实现其中一个端点
  const [testEndpoint, setTestEndpoint] = useState<'chat_completions' | 'responses'>('chat_completions');
  // 「测试全部」的取消标记：关闭弹窗或点停止后，串行循环在下一轮退出
  const cancelRef = useRef(false);
  // 在途测试请求的中断器：停止/关窗时 abort，立即掐断当前请求
  //（后端用请求 ctx 直发上游，前端断开会一路取消到上游连接）。
  const abortRef = useRef<AbortController | null>(null);

  // 模型目录（模型管理已建价的模型名）：公开模型只能取自目录，缺的提醒去创建
  const catalogQuery = useQuery({
    queryKey: queryKeys.modelPrices('catalog-names'),
    queryFn: fetchCatalogNames,
    enabled: !!channelKey,
    staleTime: 60_000,
  });
  const catalog = useMemo(() => new Set(catalogQuery.data ?? []), [catalogQuery.data]);
  // 目录加载失败/未完成时跳过校验，不阻塞编辑
  const catalogReady = catalogQuery.isSuccess;

  // 每次打开（channelKey 引用变化）重置草稿与上一轮结果
  useEffect(() => {
    if (channelKey) {
      const next = draftFromKey(channelKey);
      setDraft(next);
      setBaseline(draftSignature(next));
      setModelInput('');
      setResults({});
      setRunningAll(false);
      setTestEndpoint('chat_completions');
      cancelRef.current = false;
    }
  }, [channelKey]);

  const dirty = useMemo(() => draftSignature(draft) !== baseline, [draft, baseline]);

  const anySingleTesting = Object.values(results).some((item) => item.status === 'testing');
  const busy = runningAll || anySingleTesting;

  const okCount = Object.values(results).filter((item) => item.status === 'ok').length;
  const failCount = Object.values(results).filter((item) => item.status === 'fail').length;

  // ==================== 模型清单编辑 ====================

  // 添加输入的目录匹配：按关键词就地过滤（含子串、不区分大小写），排除已添加
  const suggestions = useMemo(() => {
    const keyword = modelInput.trim().toLowerCase();
    if (!keyword || !catalogReady) return [];
    return (catalogQuery.data ?? [])
      .filter((name) => name.toLowerCase().includes(keyword) && !draft.models.includes(name))
      .slice(0, 20);
  }, [modelInput, catalogReady, catalogQuery.data, draft.models]);

  // 批量添加：逐项精确校验目录，不在目录的整体提醒去创建
  function addModels(raw: string) {
    const parts = raw.split(/[\n,]+/).map((item) => item.trim()).filter(Boolean);
    if (parts.length === 0) return;
    const rejected = catalogReady ? parts.filter((part) => !catalog.has(part)) : [];
    const accepted = parts.filter((part) => !rejected.includes(part));
    if (accepted.length > 0) {
      setDraft((prev) => {
        const next = [...prev.models];
        for (const part of accepted) {
          if (!next.includes(part)) next.push(part);
        }
        return { ...prev, models: next };
      });
      setModelInput('');
    }
    if (rejected.length > 0) {
      toast('error', t('channels.model_not_in_catalog_list', { models: rejected.join(', ') }));
    }
  }

  // 回车/添加按钮：精确命中直接加；唯一匹配加匹配项；多个/零匹配交给下方匹配区
  function handleAddCommit() {
    const raw = modelInput.trim();
    if (!raw) return;
    if (/[\n,]/.test(raw) || !catalogReady || catalog.has(raw)) {
      addModels(raw);
      return;
    }
    const only = suggestions[0];
    if (suggestions.length === 1 && only) addModels(only);
  }

  function removeModel(model: string) {
    setDraft((prev) => ({
      ...prev,
      models: prev.models.filter((item) => item !== model),
    }));
  }

  // 改名 = 换公开名：渠道（上游）名保持不变——无显式映射时补一条旧名映射；
  // 旧名的测试结果不再适用（改名后该行回到未测试态）。
  function renameModel(oldName: string, rawNext: string): boolean {
    const next = rawNext.trim();
    if (!next) return false;
    if (next === oldName) return true;
    if (draft.models.includes(next)) {
      toast('error', t('channels.model_name_duplicate'));
      return false;
    }
    if (catalogReady && !catalog.has(next)) {
      toast('error', t('channels.model_not_in_catalog_list', { models: next }));
      return false;
    }
    setDraft((prev) => {
      const mapping = { ...prev.mapping };
      const upstream = mapping[oldName] ?? oldName;
      delete mapping[oldName];
      if (upstream !== next) mapping[next] = upstream;
      return {
        ...prev,
        models: prev.models.map((item) => (item === oldName ? next : item)),
        mapping,
      };
    });
    return true;
  }

  function handleModelInputKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'Enter' || event.key === ',') {
      event.preventDefault();
      handleAddCommit();
    }
  }

  // 拉取上游模型列表：整组替换草稿（未保存前可关闭弹窗放弃）
  const fetchModelsMutation = useMutation({
    mutationFn: () => channelsApi.fetchModels(channelKey!.id),
    onSuccess: (resp) => {
      setDraft((prev) => ({ ...prev, models: resp.models }));
      toast('success', t('channels.fetch_models_success', { count: resp.models.length }));
    },
    onError: (err: Error) => toast('error', err.message),
  });

  const saveMutation = useCrudMutation({
    mutationFn: (data: ChannelKeyReq) => channelsApi.updateKey(channelKey!.id, data),
    successMessage: t('channels.update_success'),
    queryKey: queryKeys.channels(),
    extraQueryKeys: [queryKeys.channelKeys()],
    onSuccess: () => handleClose(),
  });

  function handleSave() {
    if (draft.models.length === 0) {
      toast('error', t('channels.models_required'));
      return;
    }
    saveMutation.mutate({
      models: draft.models,
      model_mapping: normalizedMapping(draft),
    });
  }

  // ==================== 测试 ====================

  async function testOne(model: string, signal: AbortSignal): Promise<void> {
    if (!channelKey) return;
    setResults((prev) => ({ ...prev, [model]: { status: 'testing' } }));
    try {
      const resp = await channelsApi.test(
        channelKey.id,
        { model, ...(channelKey.type === 'openai_compatible' ? { endpoint: testEndpoint } : {}) },
        { signal },
      );
      setResults((prev) => ({ ...prev, [model]: { status: 'ok', latencyMs: resp.latency_ms } }));
    } catch (err) {
      // 主动取消不算失败：清掉 testing 态，该行回到未测试
      if (isAbortError(err)) {
        setResults((prev) => {
          const next = { ...prev };
          delete next[model];
          return next;
        });
        return;
      }
      setResults((prev) => ({ ...prev, [model]: { status: 'fail', error: (err as Error).message } }));
    }
  }

  async function handleTestOne(model: string) {
    const ac = new AbortController();
    abortRef.current = ac;
    await testOne(model, ac.signal);
    queryClient.invalidateQueries({ queryKey: queryKeys.channels() });
    queryClient.invalidateQueries({ queryKey: queryKeys.channelKeys() });
  }

  async function handleTestAll() {
    cancelRef.current = false;
    const ac = new AbortController();
    abortRef.current = ac;
    setRunningAll(true);
    setResults({});
    // 串行逐个测试，避免并发打爆上游限流
    for (const model of draft.models) {
      if (cancelRef.current) break;
      await testOne(model, ac.signal);
    }
    setRunningAll(false);
    queryClient.invalidateQueries({ queryKey: queryKeys.channels() });
    queryClient.invalidateQueries({ queryKey: queryKeys.channelKeys() });
  }

  // 停止：置取消标记（循环下一轮退出）+ 掐断当前在途请求（否则要等它自然返回）
  function handleStop() {
    cancelRef.current = true;
    abortRef.current?.abort();
  }

  function handleClose() {
    handleStop();
    onClose();
  }

  const dialogState = useOverlayState({
    isOpen: !!channelKey,
    onOpenChange: (open) => {
      if (!open) handleClose();
    },
  });

  return (
    <Modal state={dialogState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>
                {t('channels.test_title', {
                  channel: channelKey?.channel_name || '',
                  key: channelKey?.name || channelKey?.api_key_hint || '',
                })}
              </Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="ag-channel-test-toolbar mb-3 flex items-center gap-2">
                {runningAll ? (
                  <Button size="sm" variant="secondary" onPress={handleStop}>
                    <Square className="h-3.5 w-3.5" />
                    {t('channels.test_stop')}
                  </Button>
                ) : (
                  <Button
                    isDisabled={busy || draft.models.length === 0}
                    size="sm"
                    variant="primary"
                    onPress={handleTestAll}
                  >
                    <Play className="h-3.5 w-3.5" />
                    {t('channels.test_all')}
                  </Button>
                )}
                <Button
                  isDisabled={busy || fetchModelsMutation.isPending}
                  size="sm"
                  variant="secondary"
                  onPress={() => fetchModelsMutation.mutate()}
                >
                  {fetchModelsMutation.isPending ? <Spinner size="sm" /> : <DownloadCloud className="h-3.5 w-3.5" />}
                  {t('channels.fetch_models')}
                </Button>
                {channelKey?.type === 'openai_compatible' ? (
                  // openai 协议有 chat_completions / responses 两个端点，部分上游只实现其一
                  <div
                    className="ag-channel-test-endpoints flex items-center gap-0.5 rounded-[var(--radius)] border border-border p-0.5"
                    title={t('channels.test_endpoint_hint')}
                  >
                    {(['chat_completions', 'responses'] as const).map((ep) => (
                      <button
                        className={`rounded-[calc(var(--radius)-2px)] px-2 py-1 font-mono text-xs transition-colors ${
                          testEndpoint === ep ? 'bg-primary-subtle font-medium text-primary' : 'text-text-tertiary hover:text-text'
                        }`}
                        key={ep}
                        type="button"
                        onClick={() => setTestEndpoint(ep)}
                      >
                        {ep === 'chat_completions' ? 'completions' : 'responses'}
                      </button>
                    ))}
                  </div>
                ) : null}
                {runningAll ? <Spinner size="sm" /> : null}
                {okCount + failCount > 0 ? (
                  <span className="ag-channel-test-summary ml-auto text-xs text-text-secondary">
                    {t('channels.test_summary', { ok: okCount, fail: failCount })}
                  </span>
                ) : null}
              </div>

              <div className="ag-channel-test-add-row mb-2 flex items-center gap-2">
                <Input
                  aria-label={t('channels.public_model')}
                  className="flex-1 font-mono text-xs"
                  placeholder={t('channels.add_model_placeholder')}
                  value={modelInput}
                  onChange={(event) => setModelInput(event.target.value)}
                  onKeyDown={handleModelInputKeyDown}
                />
                <Button
                  isDisabled={modelInput.trim() === ''}
                  size="sm"
                  variant="secondary"
                  onPress={handleAddCommit}
                >
                  <Plus className="h-3.5 w-3.5" />
                  {t('channels.add_row')}
                </Button>
              </div>

              {/* 关键词就地匹配目录（不用下拉）：点匹配项添加，多个时可全部添加 */}
              {modelInput.trim() && catalogReady ? (
                suggestions.length > 0 ? (
                  <div className="mb-2 flex flex-wrap items-center gap-1.5">
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
                    {suggestions.length > 1 ? (
                      <Button size="sm" variant="ghost" onPress={() => addModels(suggestions.join(','))}>
                        {t('channels.add_all_matches', { count: suggestions.length })}
                      </Button>
                    ) : null}
                  </div>
                ) : (
                  <p className="mb-2 text-xs text-warning">{t('channels.model_no_match')}</p>
                )
              ) : null}

              <div className="ag-channel-test-model-header mb-1 flex items-center gap-2 px-3 text-xs text-text-tertiary">
                <span className="flex-1">{t('channels.public_model')}</span>
                <span className="flex-1">{t('channels.channel_model')}</span>
                <span aria-hidden="true" className="w-[7.25rem] shrink-0" />
              </div>

              <div className="max-h-[45vh] overflow-y-auto rounded-[var(--radius)] border border-border">
                {draft.models.length === 0 ? (
                  <p className="px-3 py-6 text-center text-xs text-text-tertiary">{t('channels.models_empty')}</p>
                ) : (
                  draft.models.map((model) => {
                    const state = results[model];
                    const notInCatalog = catalogReady && !catalog.has(model);
                    return (
                      <div className="border-b border-border px-3 py-2 last:border-b-0" key={model}>
                        <div className="ag-channel-test-model-row flex items-center gap-2">
                          <span className="flex min-w-0 flex-1 items-center gap-1">
                            <ModelNameField
                              ariaLabel={t('channels.public_model')}
                              tone={state?.status === 'fail' ? 'danger' : notInCatalog ? 'warning' : undefined}
                              value={model}
                              onCommit={(next) => renameModel(model, next)}
                            />
                            {state?.status === 'testing' ? <Spinner size="sm" /> : null}
                            {state?.status === 'ok' ? (
                              <>
                                <CircleCheck className="h-3.5 w-3.5 shrink-0 text-success" />
                                <span className="shrink-0 font-mono text-xs text-success">{state.latencyMs}ms</span>
                              </>
                            ) : null}
                            {state?.status === 'fail' ? <CircleX className="h-3.5 w-3.5 shrink-0 text-danger" /> : null}
                          </span>
                          <Input
                            aria-label={`${model} ${t('channels.channel_model')}`}
                            className="min-w-0 flex-1 font-mono text-xs"
                            value={draft.mapping[model] ?? model}
                            onBlur={() => {
                              // 失焦归一：去空白；空值或与公开名相同 = 原名透传，不留映射条目
                              setDraft((prev) => {
                                if (!(model in prev.mapping)) return prev;
                                const raw = (prev.mapping[model] ?? '').trim();
                                const mapping = { ...prev.mapping };
                                if (raw === '' || raw === model) {
                                  delete mapping[model];
                                } else {
                                  mapping[model] = raw;
                                }
                                return { ...prev, mapping };
                              });
                            }}
                            onChange={(event) => {
                              setDraft((prev) => ({
                                ...prev,
                                mapping: { ...prev.mapping, [model]: event.target.value },
                              }));
                            }}
                          />
                          <Button
                            isDisabled={busy}
                            size="sm"
                            variant="secondary"
                            onPress={() => void handleTestOne(model)}
                          >
                            <Zap className="h-3.5 w-3.5" />
                            {t('common.test')}
                          </Button>
                          <Button
                            isIconOnly
                            aria-label={t('common.delete')}
                            isDisabled={busy}
                            size="sm"
                            variant="ghost"
                            onPress={() => removeModel(model)}
                          >
                            <X className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                        {notInCatalog ? (
                          <div className="mt-1 text-xs text-warning">{t('channels.model_not_in_catalog')}</div>
                        ) : null}
                        {state?.status === 'fail' ? (
                          <div className="mt-1 line-clamp-2 break-all text-xs text-danger" title={state.error}>
                            {state.error}
                          </div>
                        ) : null}
                      </div>
                    );
                  })
                )}
              </div>
            </Modal.Body>
            <Modal.Footer>
              {dirty ? (
                <span className="mr-auto text-xs text-warning">{t('channels.unsaved_changes')}</span>
              ) : null}
              <Button variant="secondary" onPress={handleClose}>
                {t('common.close')}
              </Button>
              <Button isDisabled={!dirty || saveMutation.isPending} variant="primary" onPress={handleSave}>
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
