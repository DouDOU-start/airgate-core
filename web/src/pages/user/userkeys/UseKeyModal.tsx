import { useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Modal, useOverlayState } from '@heroui/react';
import { AlertTriangle, Copy } from 'lucide-react';
import { useToast } from '../../../shared/ui';
import { useClipboard } from '../../../shared/hooks/useClipboard';
import { useSiteSettings } from '../../../app/providers/SiteSettingsProvider';
import { apikeysApi } from '../../../shared/api/apikeys';
import type { APIKeyResp, GroupResp } from '../../../shared/types';

function getUseKeyConfig(
  baseUrl: string,
  platform: string,
  tab: 'claude' | 'codex',
  shell: 'unix' | 'cmd' | 'powershell',
  apiKey: string,
  t: (key: string) => string,
): { files: Array<{ path: string; content: string; hint?: string }> } {
  // OpenAI 平台同时支持 Claude Code（通过 /v1/messages 适配）和 Codex CLI
  if (platform === 'openai') {
    if (tab === 'claude') {
      // Claude Code 配置 — 通过 OpenAI 插件的 Anthropic 协议适配
      if (shell === 'unix') {
        return {
          files: [
            {
              path: '~/.bashrc 或 ~/.zshrc',
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
    } else {
      // Codex CLI 配置 — 写入 ~/.codex/config.toml 与 ~/.codex/auth.json
      const configDir = shell === 'unix' ? '~/.codex' : '%userprofile%\\.codex';
      const configToml = `model_provider = "OpenAI"
model = "gpt-5.4"
review_model = "gpt-5.4"
model_reasoning_effort = "xhigh"
disable_response_storage = true
network_access = "enabled"
windows_wsl_setup_acknowledged = true
model_context_window = 1000000
model_auto_compact_token_limit = 900000

[model_providers.OpenAI]
name = "OpenAI"
base_url = "${baseUrl}"
wire_api = "responses"
requires_openai_auth = true`;
      const authJson = `{\n  "OPENAI_API_KEY": "${apiKey}"\n}`;
      return {
        files: [
          {
            path: `${configDir}/config.toml`,
            content: configToml,
            hint: t('user_keys.codex_config_toml_hint'),
          },
          {
            path: `${configDir}/auth.json`,
            content: authJson,
          },
        ],
      };
    }
  }

  // 默认/其他平台 — 使用 Claude 标准配置
  if (shell === 'unix') {
    return {
      files: [
        {
          path: '~/.bashrc 或 ~/.zshrc',
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

export function useUseKeyModal(groupMap: Map<number, GroupResp>) {
  const { toast } = useToast();
  const { t } = useTranslation();

  const [useKeyTarget, setUseKeyTarget] = useState<APIKeyResp | null>(null);
  const [useKeyValue, setUseKeyValue] = useState<string | null>(null);
  const [useKeyTab, setUseKeyTab] = useState<'claude' | 'codex'>('claude');
  const [useKeyShell, setUseKeyShell] = useState<'unix' | 'cmd' | 'powershell'>('unix');

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

  const getGroupPlatform = (groupId: number | null) =>
    groupId == null ? '' : groupMap.get(groupId)?.platform || '';

  const useKeyPlatform = useKeyTarget ? getGroupPlatform(useKeyTarget.group_id) : '';
  const showClientTabs = useKeyPlatform === 'openai';

  return {
    useKeyTarget,
    useKeyValue,
    useKeyTab,
    setUseKeyTab,
    useKeyShell,
    setUseKeyShell,
    useKeyPlatform,
    showClientTabs,
    openUseKeyModal,
    closeUseKeyModal,
  };
}

export function UseKeyModal({
  useKeyTarget,
  useKeyValue,
  useKeyPlatform,
  showClientTabs,
  useKeyTab,
  setUseKeyTab,
  useKeyShell,
  setUseKeyShell,
  onClose,
}: {
  useKeyTarget: APIKeyResp | null;
  useKeyValue: string | null;
  useKeyPlatform: string;
  showClientTabs: boolean;
  useKeyTab: 'claude' | 'codex';
  setUseKeyTab: (tab: 'claude' | 'codex') => void;
  useKeyShell: 'unix' | 'cmd' | 'powershell';
  setUseKeyShell: (shell: 'unix' | 'cmd' | 'powershell') => void;
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
        useKeyPlatform ? (
          <div className="space-y-4">
            <p className="text-sm text-text-secondary">
              {t('user_keys.use_key_desc')}
            </p>

            {/* 客户端选择 Tab（OpenAI 平台时显示） */}
            {showClientTabs && (
              <div className="flex gap-1">
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
                  variant={useKeyTab === 'codex' ? 'primary' : 'secondary'}
                  onPress={() => setUseKeyTab('codex')}
                >
                  Codex CLI
                </Button>
              </div>
            )}

            {/* OS/Shell Tab */}
            <div className="flex gap-1">
              <Button
                fullWidth
                size="sm"
                variant={useKeyShell === 'unix' ? 'primary' : 'secondary'}
                onPress={() => setUseKeyShell('unix')}
              >
                macOS / Linux
              </Button>
              {useKeyTab === 'codex' ? (
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
            </div>

            {/* 配置代码块 */}
            {getUseKeyConfig(baseUrl, useKeyPlatform, useKeyTab, useKeyShell, useKeyValue, t).files.map(
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
