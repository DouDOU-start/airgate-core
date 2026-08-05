import { useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Modal, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { AlertTriangle, Copy } from 'lucide-react';
import { useToast } from '../../../shared/ui';
import { useClipboard } from '../../../shared/hooks/useClipboard';
import { useSiteSettings } from '../../../app/providers/SiteSettingsProvider';
import { apikeysApi } from '../../../shared/api/apikeys';
import type { APIKeyResp } from '../../../shared/types';

type UseKeyTab = 'claude' | 'codex' | 'desktop' | 'grok';
type UseKeyShell = 'unix' | 'cmd' | 'powershell';

function normalizeBaseUrl(baseUrl: string): string {
  return baseUrl.trim().replace(/\/+$/, '');
}

function toResponsesBaseUrl(baseUrl: string): string {
  const normalized = normalizeBaseUrl(baseUrl);
  return /\/v1$/i.test(normalized) ? normalized : `${normalized}/v1`;
}

function toTomlString(value: string): string {
  return JSON.stringify(value);
}

// 渠道化后任意入口协议均可用，配置内容只取决于客户端类型（tab），与分组无关。
function getUseKeyConfig(
  baseUrl: string,
  tab: UseKeyTab,
  shell: UseKeyShell,
  apiKey: string,
  siteName: string,
  t: (key: string) => string,
): { files: Array<{ path: string; content: string; hint?: string }> } {
  // Claude Desktop — 3P profile 配置
  if (tab === 'desktop') {
    const profileJson = JSON.stringify({
      inferenceProvider: 'gateway',
      inferenceGatewayBaseUrl: baseUrl,
      inferenceGatewayAuthScheme: 'bearer',
      inferenceGatewayApiKey: apiKey,
    }, null, 2);
    const configPath = shell === 'unix'
      ? '~/Library/Application Support/Claude/claude_desktop_config.json'
      : '%LOCALAPPDATA%\\Claude\\claude_desktop_config.json';
    return {
      files: [
        {
          path: configPath,
          content: profileJson,
          hint: t('user_keys.desktop_config_hint'),
        },
      ],
    };
  }

  if (tab === 'codex') {
    // Codex CLI 配置 — 写入 config.toml + auth.json，与 CCS 导入格式一致
    const configDir = shell === 'unix' ? '~/.codex' : '%USERPROFILE%\\.codex';
    const configPath = shell === 'unix' ? `${configDir}/config.toml` : `${configDir}\\config.toml`;
    const authPath = shell === 'unix' ? `${configDir}/auth.json` : `${configDir}\\auth.json`;
    const providerName = siteName || 'AirGate';
    const configToml = `model_provider = "${providerName}"
model = "gpt-5.5"
review_model = "gpt-5.5"
model_reasoning_effort = "xhigh"
disable_response_storage = true
network_access = "enabled"

[model_providers.${providerName}]
name = "${providerName}"
base_url = "${baseUrl}"
wire_api = "responses"
requires_openai_auth = true

[features]
goals = true`;
    const authJson = JSON.stringify({ OPENAI_API_KEY: apiKey }, null, 2);
    return {
      files: [
        {
          path: configPath,
          content: configToml,
          hint: t('user_keys.codex_config_toml_hint'),
        },
        {
          path: authPath,
          content: authJson,
        },
      ],
    };
  }

  if (tab === 'grok') {
    // Grok Build 原生配置格式与 CC-Switch 的受管应用保持一致。
    const configDir = shell === 'unix' ? '~/.grok' : '%USERPROFILE%\\.grok';
    const configPath = shell === 'unix' ? `${configDir}/config.toml` : `${configDir}\\config.toml`;
    const model = 'grok-4.5';
    const configToml = `[models]
default = ${toTomlString(model)}
web_search = ${toTomlString(model)}

[endpoints]
xai_api_base_url = ${toTomlString(toResponsesBaseUrl(baseUrl))}

[auth]
preferred_method = "api_key"

[model.${toTomlString(model)}]
model = ${toTomlString(model)}
base_url = ${toTomlString(toResponsesBaseUrl(baseUrl))}
name = ${toTomlString(siteName || 'AirGate')}
api_key = ${toTomlString(apiKey)}
api_backend = "responses"
context_window = 500000`;
    return {
      files: [
        {
          path: configPath,
          content: configToml,
          hint: t('user_keys.grok_config_toml_hint'),
        },
      ],
    };
  }

  // Claude Code — Anthropic 环境变量配置
  if (shell === 'unix') {
    return {
      files: [
        {
          path: `~/.bashrc ${t('common.or')} ~/.zshrc`,
          content: `export ANTHROPIC_BASE_URL="${baseUrl}"\nexport ANTHROPIC_API_KEY="${apiKey}"`,
        },
      ],
    };
  } else if (shell === 'cmd') {
    return {
      files: [
        {
          path: 'CMD',
          content: `set ANTHROPIC_BASE_URL=${baseUrl}\nset ANTHROPIC_API_KEY=${apiKey}`,
        },
      ],
    };
  } else {
    return {
      files: [
        {
          path: 'PowerShell',
          content: `$env:ANTHROPIC_BASE_URL="${baseUrl}"\n$env:ANTHROPIC_API_KEY="${apiKey}"`,
        },
      ],
    };
  }
}

export function useUseKeyModal() {
  const { toast } = useToast();
  const { t } = useTranslation();

  const [useKeyTarget, setUseKeyTarget] = useState<APIKeyResp | null>(null);
  const [useKeyValue, setUseKeyValue] = useState<string | null>(null);
  const [useKeyTab, setUseKeyTab] = useState<UseKeyTab>('claude');
  const [useKeyShell, setUseKeyShell] = useState<UseKeyShell>('unix');

  const openUseKeyModal = useCallback(
    async (row: APIKeyResp) => {
      setUseKeyTarget(row);
      setUseKeyTab('claude');
      setUseKeyShell('unix');
      try {
        const resp = await apikeysApi.reveal(row.id);
        setUseKeyValue(resp.key || null);
      } catch {
        toast('error', t('user_keys.reveal_failed'));
        setUseKeyTarget(null);
      }
    },
    [toast, t],
  );

  const closeUseKeyModal = useCallback(() => {
    setUseKeyTarget(null);
    setUseKeyValue(null);
  }, []);

  return {
    useKeyTarget,
    useKeyValue,
    useKeyTab,
    setUseKeyTab,
    useKeyShell,
    setUseKeyShell,
    openUseKeyModal,
    closeUseKeyModal,
  };
}

export function UseKeyModal({
  useKeyTarget,
  useKeyValue,
  useKeyTab,
  setUseKeyTab,
  useKeyShell,
  setUseKeyShell,
  onClose,
}: {
  useKeyTarget: APIKeyResp | null;
  useKeyValue: string | null;
  useKeyTab: UseKeyTab;
  setUseKeyTab: (tab: UseKeyTab) => void;
  useKeyShell: UseKeyShell;
  setUseKeyShell: (shell: UseKeyShell) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const copy = useClipboard();
  const site = useSiteSettings();
  const baseUrl = site.api_base_url || window.location.origin;
  const modalState = useOverlayState({
    isOpen: !!useKeyTarget,
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
            style={{ maxWidth: '560px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{t('user_keys.use_key_title')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
      {useKeyValue ? (
        useKeyTarget?.group_id != null ? (
          <div className="space-y-4">
            <p className="text-sm text-text-secondary">
              {t('user_keys.use_key_desc')}
            </p>

            {/* 客户端选择 Tab */}
            <div className="grid grid-cols-2 gap-1 sm:grid-cols-4">
              <Button
                fullWidth
                size="sm"
                variant={useKeyTab === 'claude' ? 'primary' : 'secondary'}
                onPress={() => setUseKeyTab('claude')}
              >
                Claude Code
              </Button>
              <Button
                fullWidth
                size="sm"
                variant={useKeyTab === 'desktop' ? 'primary' : 'secondary'}
                onPress={() => setUseKeyTab('desktop')}
              >
                Claude Desktop
              </Button>
              <Button
                fullWidth
                size="sm"
                variant={useKeyTab === 'codex' ? 'primary' : 'secondary'}
                onPress={() => setUseKeyTab('codex')}
              >
                Codex CLI
              </Button>
              <Button
                fullWidth
                size="sm"
                variant={useKeyTab === 'grok' ? 'primary' : 'secondary'}
                onPress={() => setUseKeyTab('grok')}
              >
                Grok Build
              </Button>
            </div>

            {/* OS/Shell Tab（Claude Desktop 不需要） */}
            {useKeyTab !== 'desktop' && <div className="flex gap-1">
              <Button
                fullWidth
                size="sm"
                variant={useKeyShell === 'unix' ? 'primary' : 'secondary'}
                onPress={() => setUseKeyShell('unix')}
              >
                macOS / Linux
              </Button>
              {useKeyTab === 'codex' || useKeyTab === 'grok' ? (
                <Button
                  fullWidth
                  size="sm"
                  variant={useKeyShell !== 'unix' ? 'primary' : 'secondary'}
                  onPress={() => setUseKeyShell('cmd')}
                >
                  Windows
                </Button>
              ) : (
                <>
                  <Button
                    fullWidth
                    size="sm"
                    variant={useKeyShell === 'cmd' ? 'primary' : 'secondary'}
                    onPress={() => setUseKeyShell('cmd')}
                  >
                    Windows CMD
                  </Button>
                  <Button
                    fullWidth
                    size="sm"
                    variant={useKeyShell === 'powershell' ? 'primary' : 'secondary'}
                    onPress={() => setUseKeyShell('powershell')}
                  >
                    PowerShell
                  </Button>
                </>
              )}
            </div>}

            {/* 配置代码块 */}
            {getUseKeyConfig(baseUrl, useKeyTab, useKeyShell, useKeyValue, site.site_name || 'AirGate', t).files.map(
              (file, idx) => (
                <div key={idx}>
                  {file.hint && (
                    <p className="text-xs text-warning mb-1.5 flex items-center gap-1">
                      <AlertTriangle className="w-3 h-3 shrink-0" />
                      {file.hint}
                    </p>
                  )}
                  <div className="rounded-md overflow-hidden border border-glass-border">
                    <div className="flex items-center justify-between px-3 py-1.5 bg-bg-hover border-b border-glass-border">
                      <span className="text-xs text-text-tertiary font-mono">{file.path}</span>
                      <Button
                        size="sm"
                        variant="ghost"
                        onPress={() => copy(file.content, t('user_keys.copied'))}
                      >
                        <Copy className="w-3 h-3" />
                        {t('user_keys.copy')}
                      </Button>
                    </div>
                    <pre className="p-3 text-sm font-mono text-text bg-surface overflow-x-auto whitespace-pre-wrap">
                      {file.content}
                    </pre>
                  </div>
                </div>
              ),
            )}
          </div>
        ) : (
          <div className="rounded-md border border-glass-border bg-surface p-4 text-sm text-text-secondary">
            {t('user_keys.group_unbound_hint')}
          </div>
        )
      ) : (
        <div className="flex items-center justify-center py-8 text-text-tertiary text-sm">
          {t('common.loading')}
        </div>
      )}
            </Modal.Body>
            <Modal.Footer>
              <Button variant="primary" onPress={onClose}>
                {t('common.close')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
