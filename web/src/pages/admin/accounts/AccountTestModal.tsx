import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Label,
  ListBox,
  Modal,
  Select,
  Spinner,
  Tabs,
  TextArea,
  TextField as HeroTextField,
  useOverlayState,
} from '@heroui/react';
import { Play, RotateCcw } from 'lucide-react';
import { accountsApi, type AccountTestEvent, type AccountTestModel } from '../../../shared/api/accounts';
import { pluginsApi } from '../../../shared/api/plugins';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import type { AccountResp } from '../../../shared/types';
import { AccountSummaryCard } from './AccountSummaryCard';

type Status = 'idle' | 'connecting' | 'success' | 'error';
type AccountTestMode = 'normal' | 'overage';

type Line = { text: string; cls: string };

type MediaItem = {
  kind: 'image' | 'video';
  url: string;
};

const videoDurations = Array.from({ length: 15 }, (_, index) => String(index + 1));
const videoAspectRatios = ['1:1', '16:9', '9:16', '4:3', '3:4', '3:2', '2:3'];
const accountTestTransformCapability = 'account_test_transform.v1';

function supports1080pVideo(modelId: string) {
  const base = modelId.trim().toLowerCase().split('/').pop();
  return base === 'grok-imagine-video-1.5' || base === 'grok-imagine-video-1.5-preview';
}

function isSafeMediaUrl(kind: MediaItem['kind'], value: string) {
  if (kind === 'image' && value.startsWith('data:image/')) return true;
  try {
    const parsed = new URL(value);
    return parsed.protocol === 'https:' || parsed.protocol === 'http:';
  } catch {
    return false;
  }
}

/**
 * 账号连通性测试（对齐 sub2api AccountTestModal）：
 * 选模型 → POST /accounts/:id/test SSE → 终端式输出。
 */
export function AccountTestModal({
  account,
  onClose,
}: {
  account: AccountResp | null;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const open = !!account;
  const isCodexAccount = account?.platform.toLowerCase() === 'codex';
  const [models, setModels] = useState<AccountTestModel[]>([]);
  const [modelId, setModelId] = useState('');
  const [prompt, setPrompt] = useState('');
  const [videoDuration, setVideoDuration] = useState('1');
  const [videoAspectRatio, setVideoAspectRatio] = useState('16:9');
  const [videoResolution, setVideoResolution] = useState('720p');
  const [loadingModels, setLoadingModels] = useState(false);
  const [status, setStatus] = useState<Status>('idle');
  const [lines, setLines] = useState<Line[]>([]);
  const [streamText, setStreamText] = useState('');
  const [errorMsg, setErrorMsg] = useState('');
  const [mediaItems, setMediaItems] = useState<MediaItem[]>([]);
  const [mediaStatus, setMediaStatus] = useState('');
  const [testMode, setTestMode] = useState<AccountTestMode>('normal');
  const [overageAvailable, setOverageAvailable] = useState(false);
  const terminalRef = useRef<HTMLDivElement | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  const statusRef = useRef<Status>('idle');
  statusRef.current = status;

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (next) => {
      if (!next) {
        // 测试进行中不允许关闭
        if (statusRef.current === 'connecting') return;
        abortRef.current?.abort();
        onClose();
      }
    },
  });

  useEffect(() => {
    if (!account) {
      setModels([]);
      setModelId('');
      setPrompt('');
      setVideoDuration('1');
      setVideoAspectRatio('16:9');
      setVideoResolution('720p');
      setStatus('idle');
      setLines([]);
      setStreamText('');
      setErrorMsg('');
      setMediaItems([]);
      setMediaStatus('');
      setTestMode('normal');
      setOverageAvailable(false);
      abortRef.current?.abort();
      return;
    }
    let cancelled = false;
    setLoadingModels(true);
    setStatus('idle');
    setLines([]);
    setStreamText('');
    setErrorMsg('');
    setMediaItems([]);
    setMediaStatus('');
    setTestMode('normal');
    setOverageAvailable(false);
    setVideoDuration('1');
    setVideoAspectRatio('16:9');
    setVideoResolution('720p');
    if (account.platform.toLowerCase() === 'codex') {
      void pluginsApi
        .list()
        .then((plugins) => {
          if (cancelled) return;
          setOverageAvailable(plugins.some((plugin) => (
            plugin.running && plugin.capabilities.includes(accountTestTransformCapability)
          )));
        })
        .catch(() => {
          if (!cancelled) setOverageAvailable(false);
        });
    }
    void accountsApi
      .testModels(account.id)
      .then((list) => {
        if (cancelled) return;
        setModels(list || []);
        if (list && list.length > 0) {
          const sonnet = list.find((m) => m.id.toLowerCase().includes('sonnet'));
          const selected = sonnet || list[0];
          setModelId(selected?.id || '');
          setPrompt(selected?.default_prompt || '');
        } else {
          setModelId('');
          setPrompt('');
        }
      })
      .catch(() => {
        if (!cancelled) {
          setModels([]);
          setModelId('');
          setPrompt('');
        }
      })
      .finally(() => {
        if (!cancelled) setLoadingModels(false);
      });
    return () => {
      cancelled = true;
      abortRef.current?.abort();
    };
  }, [account?.id]);

  useEffect(() => {
    if (terminalRef.current) {
      terminalRef.current.scrollTop = terminalRef.current.scrollHeight;
    }
  }, [lines, streamText, status]);

  const addLine = (text: string, cls = 'text-gray-300') => {
    setLines((prev) => [...prev, { text, cls }]);
  };

  const handleEvent = (ev: AccountTestEvent) => {
    switch (ev.type) {
      case 'test_start':
        addLine(t('accounts.test_using_model', { model: ev.model || modelId }), 'text-blue-400');
        break;
      case 'content':
        if (ev.text) setStreamText((prev) => prev + ev.text);
        break;
      case 'media_status':
        setMediaStatus(
          ev.status === 'done'
            ? t('accounts.test_video_ready')
            : t('accounts.test_video_generating'),
        );
        break;
      case 'media':
        if (
          ev.url
          && (ev.media_kind === 'image' || ev.media_kind === 'video')
          && isSafeMediaUrl(ev.media_kind, ev.url)
        ) {
          const next = { kind: ev.media_kind, url: ev.url } satisfies MediaItem;
          setMediaItems((prev) => (
            prev.some((item) => item.kind === next.kind && item.url === next.url)
              ? prev
              : [...prev, next]
          ));
          if (ev.media_kind === 'video') setMediaStatus(t('accounts.test_video_ready'));
        }
        break;
      case 'test_complete':
        setStatus('success');
        break;
      case 'error':
        setStatus('error');
        setErrorMsg(ev.error || t('accounts.test_failed'));
        // 上游原文可能多行：整段写入终端，不再重复加「错误：」前缀到每行
        addLine(ev.error || t('accounts.test_failed'), 'text-red-400 whitespace-pre-wrap break-all');
        break;
      default:
        break;
    }
  };

  const startTest = async () => {
    if (!account || !modelId || status === 'connecting') return;
    const activeModel = models.find((model) => model.id === modelId);
    const effectivePrompt = prompt.trim() || activeModel?.default_prompt || '';
    const isVideoTest = activeModel?.kind === 'video';
    abortRef.current?.abort();
    const ac = new AbortController();
    abortRef.current = ac;

    setStatus('connecting');
    setLines([]);
    setStreamText('');
    setErrorMsg('');
    setMediaItems([]);
    setMediaStatus('');
    addLine(t('accounts.test_starting', { name: account.name }), 'text-blue-400');
    addLine(t('accounts.test_account_type', { type: account.type }), 'text-gray-400');
    if (isCodexAccount && overageAvailable) {
      addLine(t('accounts.test_using_mode', {
        mode: t(`accounts.test_mode_${testMode}`),
      }), 'text-gray-400');
    }
    addLine(t('accounts.test_using_prompt', { prompt: effectivePrompt }), 'text-gray-400 whitespace-pre-wrap break-all');
    if (isVideoTest) {
      addLine(t('accounts.test_video_settings', {
        duration: videoDuration,
        aspectRatio: videoAspectRatio,
        resolution: videoResolution,
      }), 'text-gray-400');
    }
    addLine('', 'text-gray-300');

    try {
      const reader = await accountsApi.testStream(
        account.id,
        {
          model_id: modelId,
          prompt: effectivePrompt,
          test_mode: isCodexAccount && overageAvailable ? testMode : 'normal',
          ...(isVideoTest ? {
            duration: Number(videoDuration),
            aspect_ratio: videoAspectRatio,
            resolution: videoResolution,
          } : {}),
        },
        { signal: ac.signal },
      );
      const decoder = new TextDecoder();
      let buffer = '';
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const parts = buffer.split('\n');
        buffer = parts.pop() || '';
        for (const line of parts) {
          if (!line.startsWith('data: ')) continue;
          const jsonStr = line.slice(6).trim();
          if (!jsonStr) continue;
          try {
            handleEvent(JSON.parse(jsonStr) as AccountTestEvent);
          } catch {
            // 忽略格式异常的流式事件，继续读取后续内容
          }
        }
      }
      setStatus((prev) => (prev === 'connecting' ? 'success' : prev));
    } catch (err) {
      if ((err as Error)?.name === 'AbortError') return;
      setStatus('error');
      const msg = (err as Error)?.message || t('accounts.test_failed');
      setErrorMsg(msg);
      addLine(msg, 'text-red-400 whitespace-pre-wrap break-all');
    }
  };

  const handleClose = () => {
    if (status === 'connecting') return;
    abortRef.current?.abort();
    onClose();
  };

  const selectedModel = models.find((model) => model.id === modelId);
  const selectedKind = selectedModel?.kind || 'text';
  const videoResolutions = supports1080pVideo(modelId)
    ? ['480p', '720p', '1080p']
    : ['480p', '720p'];

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" size="md" scroll="inside">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{t('accounts.test_title')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body className="space-y-4">
              {account ? (
                <AccountSummaryCard account={account} />
              ) : null}

              {isCodexAccount && overageAvailable ? (
                <div className="ag-account-test-mode flex items-center justify-between gap-3 rounded-lg border border-default-200 bg-default-50 px-3 py-2.5">
                  <div className="min-w-0">
                    <Label>{t('accounts.test_mode')}</Label>
                    <p className="mt-0.5 text-xs text-text-tertiary">
                      {t('accounts.test_mode_hint')}
                    </p>
                  </div>
                  <Tabs
                    className="ag-segmented-tabs ag-segmented-tabs-compact shrink-0"
                    selectedKey={testMode}
                    onSelectionChange={(key) => {
                      setTestMode(key as AccountTestMode);
                      setStatus('idle');
                      setLines([]);
                      setStreamText('');
                      setErrorMsg('');
                      setMediaItems([]);
                      setMediaStatus('');
                    }}
                  >
                    <Tabs.List>
                      <Tabs.Tab id="normal" isDisabled={status === 'connecting'}>
                        <Tabs.Indicator />
                        <span>{t('accounts.test_mode_normal')}</span>
                      </Tabs.Tab>
                      <Tabs.Tab id="overage" isDisabled={status === 'connecting'}>
                        <Tabs.Separator />
                        <Tabs.Indicator />
                        <span>{t('accounts.test_mode_overage')}</span>
                      </Tabs.Tab>
                    </Tabs.List>
                  </Tabs>
                </div>
              ) : null}

              <div className="space-y-1.5">
                <Label>{t('accounts.test_select_model')}</Label>
                <Select
                  selectedKey={modelId || undefined}
                  onSelectionChange={(key) => {
                    const nextId = String(key);
                    const nextModel = models.find((model) => model.id === nextId);
                    setModelId(nextId);
                    setPrompt(nextModel?.default_prompt || '');
                    setStatus('idle');
                    setLines([]);
                    setStreamText('');
                    setErrorMsg('');
                    setMediaItems([]);
                    setMediaStatus('');
                    setVideoDuration('1');
                    setVideoAspectRatio('16:9');
                    setVideoResolution('720p');
                  }}
                  isDisabled={loadingModels || status === 'connecting' || models.length === 0}
                  placeholder={loadingModels ? t('common.loading') : t('accounts.test_select_model')}
                >
                  <Select.Trigger>
                    <Select.Value />
                    <Select.Indicator />
                  </Select.Trigger>
                  <Select.Popover>
                    <ListBox>
                      {models.map((m) => (
                        <ListBox.Item
                          key={m.id}
                          id={m.id}
                          textValue={m.display_name && m.display_name !== m.id
                            ? `${m.display_name} · ${m.id}`
                            : m.id}
                        >
                          <span className="flex min-w-0 flex-1 items-center gap-2">
                            <span className="flex min-w-0 flex-1 flex-col">
                              <span className="truncate">{m.display_name || m.id}</span>
                              {m.display_name && m.display_name !== m.id ? (
                                <span className="truncate font-mono text-[10px] text-text-tertiary">{m.id}</span>
                              ) : null}
                            </span>
                            <span className="ml-auto shrink-0 rounded-full border border-default-200 bg-default-100 px-1.5 py-0.5 text-[10px] font-medium text-default-500">
                              {t(`accounts.test_kind_${m.kind || 'text'}`)}
                            </span>
                          </span>
                          <ListBox.ItemIndicator />
                        </ListBox.Item>
                      ))}
                    </ListBox>
                  </Select.Popover>
                </Select>
              </div>

              <div className="space-y-1.5">
                <HeroTextField fullWidth isDisabled={status === 'connecting'}>
                  <div className="ag-account-test-prompt-header flex items-center justify-between gap-3">
                    <Label>{t('accounts.test_prompt')}</Label>
                    <div className="ag-account-test-prompt-actions flex items-center gap-1.5">
                      <span className="text-[11px] font-medium text-text-tertiary">
                        {t(`accounts.test_kind_${selectedKind}`)}
                      </span>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="h-6 min-w-0 px-2 text-xs"
                        isDisabled={status === 'connecting' || !selectedModel?.default_prompt || prompt === selectedModel.default_prompt}
                        onPress={() => setPrompt(selectedModel?.default_prompt || '')}
                      >
                        <RotateCcw className="h-3 w-3" />
                        {t('accounts.test_restore_default_prompt')}
                      </Button>
                    </div>
                  </div>
                  <TextArea
                    rows={3}
                    maxLength={2000}
                    value={prompt}
                    onChange={(event) => setPrompt(event.target.value)}
                    placeholder={t('accounts.test_prompt_placeholder')}
                  />
                </HeroTextField>
                <p className="text-xs leading-5 text-text-tertiary">
                  {selectedKind === 'text'
                    ? t('accounts.test_prompt_hint')
                    : t('accounts.test_media_cost_hint')}
                </p>
              </div>

              {selectedKind === 'video' ? (
                <section className="space-y-3 rounded-xl border border-default-200 bg-default-50 p-3">
                  <div>
                    <h3 className="text-sm font-semibold text-text-primary">
                      {t('accounts.test_video_options')}
                    </h3>
                    <p className="mt-0.5 text-xs text-text-tertiary">
                      {t('accounts.test_video_options_hint')}
                    </p>
                  </div>
                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                    <div className="space-y-1.5">
                      <Label>{t('accounts.test_video_duration')}</Label>
                      <Select
                        selectedKey={videoDuration}
                        onSelectionChange={(key) => setVideoDuration(String(key))}
                        isDisabled={status === 'connecting'}
                      >
                        <Select.Trigger>
                          <Select.Value />
                          <Select.Indicator />
                        </Select.Trigger>
                        <Select.Popover>
                          <ListBox>
                            {videoDurations.map((duration) => (
                              <ListBox.Item key={duration} id={duration} textValue={t('accounts.test_video_duration_value', { duration })}>
                                {t('accounts.test_video_duration_value', { duration })}
                                <ListBox.ItemIndicator />
                              </ListBox.Item>
                            ))}
                          </ListBox>
                        </Select.Popover>
                      </Select>
                    </div>

                    <div className="space-y-1.5">
                      <Label>{t('accounts.test_video_aspect_ratio')}</Label>
                      <Select
                        selectedKey={videoAspectRatio}
                        onSelectionChange={(key) => setVideoAspectRatio(String(key))}
                        isDisabled={status === 'connecting'}
                      >
                        <Select.Trigger>
                          <Select.Value />
                          <Select.Indicator />
                        </Select.Trigger>
                        <Select.Popover>
                          <ListBox>
                            {videoAspectRatios.map((ratio) => (
                              <ListBox.Item key={ratio} id={ratio} textValue={ratio}>
                                {ratio}
                                <ListBox.ItemIndicator />
                              </ListBox.Item>
                            ))}
                          </ListBox>
                        </Select.Popover>
                      </Select>
                    </div>

                    <div className="space-y-1.5">
                      <Label>{t('accounts.test_video_resolution')}</Label>
                      <Select
                        selectedKey={videoResolution}
                        onSelectionChange={(key) => setVideoResolution(String(key))}
                        isDisabled={status === 'connecting'}
                      >
                        <Select.Trigger>
                          <Select.Value />
                          <Select.Indicator />
                        </Select.Trigger>
                        <Select.Popover>
                          <ListBox>
                            {videoResolutions.map((resolution) => (
                              <ListBox.Item key={resolution} id={resolution} textValue={resolution}>
                                {resolution}
                                <ListBox.ItemIndicator />
                              </ListBox.Item>
                            ))}
                          </ListBox>
                        </Select.Popover>
                      </Select>
                    </div>
                  </div>
                </section>
              ) : null}

              {mediaItems.length > 0 || mediaStatus ? (
                <section className="space-y-3 rounded-xl border border-default-200 bg-default-50 p-3">
                  <div className="flex items-center justify-between gap-3">
                    <span className="text-sm font-semibold text-text-primary">
                      {t('accounts.test_media_preview')}
                    </span>
                    {mediaStatus ? (
                      <span className="flex items-center gap-1.5 text-xs text-text-tertiary">
                        {mediaItems.length === 0 ? <Spinner size="sm" /> : null}
                        {mediaStatus}
                      </span>
                    ) : null}
                  </div>
                  {mediaItems.length > 0 ? (
                    <div className="grid gap-3">
                      {mediaItems.map((item, index) => (
                        <div
                          key={`${item.kind}-${index}`}
                          className="overflow-hidden rounded-lg border border-default-200 bg-black"
                        >
                          {item.kind === 'image' ? (
                            <img
                              src={item.url}
                              alt={t('accounts.test_generated_image', { index: index + 1 })}
                              className="max-h-80 w-full object-contain"
                            />
                          ) : (
                            <video
                              src={item.url}
                              controls
                              playsInline
                              preload="metadata"
                              className="max-h-80 w-full bg-black"
                            />
                          )}
                          <div className="border-t border-white/10 bg-gray-950 px-3 py-2 text-right">
                            <a
                              href={item.url}
                              target="_blank"
                              rel="noreferrer"
                              className="text-xs font-medium text-blue-400 hover:text-blue-300"
                            >
                              {t('accounts.test_open_media')}
                            </a>
                          </div>
                        </div>
                      ))}
                    </div>
                  ) : null}
                </section>
              ) : null}

              <div
                ref={terminalRef}
                className="ag-account-test-terminal max-h-[260px] min-h-[140px] overflow-y-auto rounded-xl border border-gray-700 bg-gray-950 p-4 font-mono text-sm"
              >
                {status === 'idle' ? (
                  <div className="flex items-center gap-2 text-gray-500">
                    <Play className="h-3.5 w-3.5" />
                    <span>{t('accounts.test_ready')}</span>
                  </div>
                ) : null}
                {status === 'connecting' && lines.length === 0 ? (
                  <div className="flex items-center gap-2 text-yellow-400">
                    <Spinner size="sm" />
                    <span>{t('accounts.test_connecting')}</span>
                  </div>
                ) : null}
                {lines.map((line, i) => (
                  <div key={i} className={line.cls}>
                    {line.text || '\u00a0'}
                  </div>
                ))}
                {streamText ? (
                  <div className="text-green-400">
                    {streamText}
                    {status === 'connecting' ? <span className="animate-pulse">_</span> : null}
                  </div>
                ) : null}
                {status === 'success' ? (
                  <div className="mt-3 flex items-center gap-2 border-t border-gray-700 pt-3 text-green-400">
                    {t('accounts.test_completed')}
                  </div>
                ) : null}
                {status === 'error' && errorMsg && !lines.some((l) => l.text === errorMsg) ? (
                  <div className="mt-3 whitespace-pre-wrap break-all border-t border-gray-700 pt-3 text-red-400">
                    {errorMsg}
                  </div>
                ) : null}
              </div>
            </Modal.Body>
            <Modal.Footer className="flex justify-end gap-2">
              <Button variant="secondary" isDisabled={status === 'connecting'} onPress={handleClose}>
                {t('common.close')}
              </Button>
              <Button
                variant="primary"
                isDisabled={status === 'connecting' || !modelId}
                onPress={() => void startTest()}
              >
                {status === 'connecting' ? (
                  <Spinner size="sm" />
                ) : status === 'idle' ? (
                  <Play className="h-3.5 w-3.5" />
                ) : (
                  <RotateCcw className="h-3.5 w-3.5" />
                )}
                {status === 'connecting'
                  ? t('accounts.test_running')
                  : status === 'idle'
                    ? t('accounts.test_start')
                    : t('accounts.test_retry')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
