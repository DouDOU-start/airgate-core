import { useState, useEffect, useRef, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Chip, Label, ListBox, Modal, Select, useOverlayState } from '@heroui/react';
import { Play, RotateCcw, Copy, Check, X } from 'lucide-react';
import { accountsApi } from '../../shared/api/accounts';
import { getToken } from '../../shared/api/client';
import { useClipboard } from '../../shared/hooks/useClipboard';
import type { AccountResp, ModelInfo } from '../../shared/types';

type TestStatus = 'idle' | 'connecting' | 'streaming' | 'success' | 'error';

interface OutputLine {
  text: string;
  color: string; // tailwind text color class
}

interface AccountTestModalProps {
  open: boolean;
  account: AccountResp | null;
  onClose: () => void;
}

export function AccountTestModal({ open, account, onClose }: AccountTestModalProps) {
  const { t } = useTranslation();

  const [models, setModels] = useState<ModelInfo[]>([]);
  const [selectedModel, setSelectedModel] = useState('');
  const [loadingModels, setLoadingModels] = useState(false);

  const [status, setStatus] = useState<TestStatus>('idle');
  const [outputLines, setOutputLines] = useState<OutputLine[]>([]);
  const [streamingContent, setStreamingContent] = useState('');
  const [errorMessage, setErrorMessage] = useState('');
  const [copied, setCopied] = useState(false);

  const terminalRef = useRef<HTMLDivElement>(null);
  const abortRef = useRef<AbortController | null>(null);
  const streamingRef = useRef('');

  // 加载模型列表
  useEffect(() => {
    if (!open || !account) return;
    setLoadingModels(true);
    accountsApi.models(account.id)
      .then((list) => {
        // 过滤掉生图专用模型：测试流程发的是 chat 格式 {messages:[{role:user,content:"hi"}]}，
        // ChatGPT OAuth 对 gpt-image-* 系列的 chat 调用会直接报 "not supported"。
        // 账号能跑普通 chat 就一定能跑图（图像走派生的 image_generation tool 通道），
        // 不必在测试里单独验证生图。
        const items = (list ?? []).filter(
          (m) => !m.id.toLowerCase().startsWith('gpt-image-'),
        );
        setModels(items);
        if (items.length > 0) setSelectedModel(items[0]!.id);
      })
      .catch(() => setModels([]))
      .finally(() => setLoadingModels(false));
  }, [open, account]);

  // 重置状态
  useEffect(() => {
    if (!open) {
      setStatus('idle');
      setOutputLines([]);
      setStreamingContent('');
      setErrorMessage('');
      setSelectedModel('');
      setModels([]);
      setCopied(false);
    }
  }, [open]);

  const scrollToBottom = useCallback(() => {
    requestAnimationFrame(() => {
      if (terminalRef.current) {
        terminalRef.current.scrollTop = terminalRef.current.scrollHeight;
      }
    });
  }, []);

  const addLine = useCallback((text: string, color: string) => {
    setOutputLines((prev) => [...prev, { text, color }]);
    scrollToBottom();
  }, [scrollToBottom]);

  const startTest = useCallback(async () => {
    if (!account) return;

    // 重置
    setOutputLines([]);
    setStreamingContent('');
    streamingRef.current = '';
    setErrorMessage('');
    setStatus('connecting');

    addLine(t('accounts.test_connecting'), 'text-yellow-400');

    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const url = new URL(
        accountsApi.testUrl(account.id),
        window.location.origin,
      );
      const headers: Record<string, string> = { 'Content-Type': 'application/json' };
      const token = getToken();
      if (token) headers['Authorization'] = `Bearer ${token}`;

      const res = await fetch(url.toString(), {
        method: 'POST',
        headers,
        body: JSON.stringify({ model_id: selectedModel }),
        signal: controller.signal,
      });

      if (!res.ok || !res.body) {
        setStatus('error');
        setErrorMessage(`HTTP ${res.status}`);
        addLine(`HTTP ${res.status}`, 'text-red-400');
        return;
      }

      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() ?? '';

        for (const line of lines) {
          const trimmed = line.trim();
          if (!trimmed) continue;

          // 收集本行中所有 SSE data 片段。
          // 正常情况每行只有一个 "data: {...}"，但上游插件可能在 data: 前
          // 写入非 SSE 内容（如原始错误 JSON），导致一行里出现：
          //   {"error":...}data: {"type":"test_complete",...}
          // 因此需要把 "data: " 之后的 JSON 全部提取出来。
          const ssePayloads: string[] = [];
          let rawNonSSE = '';
          const dataPrefix = 'data: ';
          const firstDataIdx = trimmed.indexOf(dataPrefix);

          if (firstDataIdx < 0) {
            // 整行没有 data: 前缀，可能是上游直接写的错误 JSON
            rawNonSSE = trimmed;
          } else {
            if (firstDataIdx > 0) {
              rawNonSSE = trimmed.slice(0, firstDataIdx).trim();
            }
            // 提取所有 data: 片段
            let idx = firstDataIdx;
            while (idx >= 0 && idx < trimmed.length) {
              const payloadStart = idx + dataPrefix.length;
              const nextIdx = trimmed.indexOf(dataPrefix, payloadStart);
              const payloadStr = nextIdx >= 0
                ? trimmed.slice(payloadStart, nextIdx).trim()
                : trimmed.slice(payloadStart).trim();
              if (payloadStr && payloadStr !== '[DONE]') {
                ssePayloads.push(payloadStr);
              }
              idx = nextIdx;
            }
          }

          // 处理非 SSE 的原始错误 JSON（上游插件直写的 error 响应）
          // 仅在没有后续 SSE data 片段时才显示（有 test_complete 时由它统一展示）
          if (rawNonSSE && ssePayloads.length === 0) {
            try {
              const raw = JSON.parse(rawNonSSE);
              let errMsg = '';
              if (raw?.error) {
                errMsg = typeof raw.error === 'string'
                  ? raw.error
                  : raw.error.message || JSON.stringify(raw.error);
              } else if (raw?.message) {
                errMsg = raw.code ? `${raw.code}: ${raw.message}` : raw.message;
              }
              if (errMsg) {
                addLine(errMsg, 'text-red-400');
              }
            } catch {
              // 非 JSON，忽略
            }
          }

          for (const payload of ssePayloads) {
            try {
              const data = JSON.parse(payload);

              // 自定义事件（Core 包装）
              if (data.type === 'test_start') {
                addLine(t('accounts.test_connected'), 'text-green-400');
                addLine(t('accounts.test_model_used', { model: data.model }), 'text-cyan-400');
                addLine(t('accounts.test_sending'), 'text-gray-400');
                addLine(t('accounts.test_response'), 'text-yellow-400');
                setStatus('streaming');
                continue;
              }

              if (data.type === 'test_complete') {
                if (streamingRef.current) {
                  addLine(streamingRef.current, 'text-green-300');
                  streamingRef.current = '';
                  setStreamingContent('');
                }
                if (data.success) {
                  setStatus('success');
                } else {
                  // 失败消息只设置到底部 status 行（带 ✗ 图标，和成功的
                  // ✓ 测试完成! 对称），不要再 addLine 到终端输出区，否则
                  // 同一条错误会被画两次。
                  setStatus('error');
                  setErrorMessage(data.error || t('accounts.test_error'));
                }
                continue;
              }

              // 插件原始 SSE：Responses API 格式
              if (data?.type === 'response.output_text.delta' && data?.delta) {
                streamingRef.current += data.delta;
                setStreamingContent(streamingRef.current);
                scrollToBottom();
                continue;
              }

              // 插件原始 SSE：Chat Completions API 格式
              if (data?.object === 'chat.completion.chunk') {
                const content = data.choices?.[0]?.delta?.content;
                if (content) {
                  streamingRef.current += content;
                  setStreamingContent(streamingRef.current);
                  scrollToBottom();
                }
                continue;
              }

              // 插件原始 SSE：Anthropic Messages API 格式
              if (data?.type === 'content_block_delta' && data?.delta?.type === 'text_delta') {
                const text = data.delta.text;
                if (text) {
                  streamingRef.current += text;
                  setStreamingContent(streamingRef.current);
                  scrollToBottom();
                }
              }
            } catch {
              // 非 JSON，忽略
            }
          }
        }
      }

      // 流结束后，如果仍处于 connecting/streaming 说明没收到 test_complete，
      // 强制标记为错误，避免 UI 卡死。
      setStatus((prev) => {
        if (prev === 'connecting' || prev === 'streaming') {
          const fallbackMsg = buffer.trim() || t('accounts.test_error');
          setErrorMessage(fallbackMsg);
          addLine(fallbackMsg, 'text-red-400');
          return 'error';
        }
        return prev;
      });
    } catch (err) {
      if ((err as Error).name === 'AbortError') return;
      setStatus('error');
      const msg = (err as Error).message;
      setErrorMessage(msg);
      addLine(msg, 'text-red-400');
    }
  }, [account, selectedModel, addLine, scrollToBottom, t]);

  const handleClose = () => {
    abortRef.current?.abort();
    onClose();
  };

  const clipboardCopy = useClipboard();
  const handleCopy = async () => {
    const text = outputLines.map((l) => l.text).join('\n') + (streamingContent ? '\n' + streamingContent : '');
    await clipboardCopy(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) handleClose();
    },
  });

  if (!account) return null;

  const canStart = status !== 'connecting' && status !== 'streaming' && !!selectedModel;
  const modelOptions = loadingModels
    ? [{ id: '', label: t('common.loading') }]
    : models.map((m) => ({ id: m.id, label: m.name || m.id }));
  const selectedModelLabel = modelOptions.find((item) => item.id === selectedModel)?.label ?? '';
  const isRunning = status === 'connecting' || status === 'streaming';

  return (
    <Modal state={modalState}>
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '560px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{t('accounts.test_modal_title')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                {/* 账号信息卡片 */}
                <div className="flex items-center gap-3 p-3 rounded-lg bg-[var(--ag-bg-surface)] border border-[var(--ag-glass-border)]">
                  <div className="flex-1 min-w-0">
                    <div className="font-medium text-sm text-[var(--ag-text)] truncate">
                      {account.name}
                    </div>
                    <div className="flex items-center gap-2 mt-1">
                      <Chip color="default" size="sm" variant="soft">{account.platform.toUpperCase()}</Chip>
                      {account.type && <Chip color="accent" size="sm" variant="soft">{account.type}</Chip>}
                    </div>
                  </div>
                </div>

                {/* 模型选择 */}
                <Select
                  fullWidth
                  selectedKey={selectedModel}
                  onSelectionChange={(key) => setSelectedModel(key == null ? '' : String(key))}
                  isDisabled={isRunning}
                >
                  <Label>{t('accounts.select_model')}</Label>
                  <Select.Trigger>
                    <Select.Value>{selectedModelLabel}</Select.Value>
                    <Select.Indicator />
                  </Select.Trigger>
                  <Select.Popover>
                    <ListBox items={modelOptions}>
                      {(item) => (
                        <ListBox.Item id={item.id} textValue={item.label}>
                          {item.label}
                        </ListBox.Item>
                      )}
                    </ListBox>
                  </Select.Popover>
                </Select>

                {/* 终端输出区域 */}
                <div className="relative group">
                  <div
                    ref={terminalRef}
                    className="bg-gray-900 rounded-lg border border-gray-700 p-4 font-mono text-xs leading-relaxed overflow-y-auto"
                    style={{ minHeight: 120, maxHeight: 240 }}
                  >
                    {status === 'idle' && outputLines.length === 0 ? (
                      <span className="text-gray-500">{t('accounts.test_ready')}</span>
                    ) : (
                      <>
                        {outputLines.map((line, i) => (
                          <div key={i} className={line.color}>{line.text}</div>
                        ))}
                        {streamingContent && (
                          <span className="text-green-400">
                            {streamingContent}
                            <span className="animate-pulse">_</span>
                          </span>
                        )}
                        {status === 'success' && (
                          <div className="text-green-400 mt-1">
                            <Check className="w-3.5 h-3.5 inline mr-1" />
                            {t('accounts.test_done')}
                          </div>
                        )}
                        {status === 'error' && (
                          <div className="text-red-400 mt-1">
                            <X className="w-3.5 h-3.5 inline mr-1" />
                            {errorMessage || t('accounts.test_error')}
                          </div>
                        )}
                      </>
                    )}
                  </div>

                  {/* 复制按钮 */}
                  {outputLines.length > 0 && (
                    <Button
                      aria-label={t('common.copy')}
                      className="absolute top-2 right-2 opacity-0 group-hover:opacity-100 transition-opacity"
                      isIconOnly
                      size="sm"
                      variant="secondary"
                      onPress={handleCopy}
                    >
                      {copied ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
                    </Button>
                  )}
                </div>
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={handleClose}>
                {t('common.close')}
              </Button>
              <Button
                variant={status === 'error' ? 'danger' : 'primary'}
                onPress={startTest}
                isDisabled={!canStart}
                aria-busy={isRunning}
              >
                {status === 'idle' || status === 'connecting' || status === 'streaming'
                  ? <Play className="w-3.5 h-3.5" />
                  : <RotateCcw className="w-3.5 h-3.5" />
                }
                {status === 'success' || status === 'error'
                  ? t('accounts.retry')
                  : t('accounts.start_test')
                }
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
