import { useState, useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { AlertTriangle, Copy } from 'lucide-react';
import { Modal } from '../../../shared/components/Modal';
import { Button } from '../../../shared/components/Button';
import { useToast } from '../../../shared/components/Toast';
import { apikeysApi } from '../../../shared/api/apikeys';
import type { APIKeyResp, GroupResp } from '../../../shared/types';

function getUseKeyConfig(
  baseUrl: string,
  platform: string,
  tab: 'claude' | 'codex',
  shell: 'unix' | 'cmd' | 'powershell',
  apiKey: string,
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
      // Codex CLI 配置
      if (shell === 'unix') {
        return {
          files: [
            {
              path: '~/.codex/config.toml',
              content: `model = "gpt-5.4"\n\n[api]\napi_key_env = "OPENAI_API_KEY"\nbase_url = "${baseUrl}"`,
            },
            {
              path: '~/.bashrc 或 ~/.zshrc',
              content: `export OPENAI_API_KEY="${apiKey}"`,
            },
          ],
        };
      } else if (shell === 'cmd') {
        return {
          files: [
            {
              path: '%USERPROFILE%\\.codex\\config.toml',
              content: `model = "gpt-5.4"\n\n[api]\napi_key_env = "OPENAI_API_KEY"\nbase_url = "${baseUrl}"`,
            },
            {
              path: 'CMD',
              content: `set OPENAI_API_KEY=${apiKey}`,
            },
          ],
        };
      } else {
        return {
          files: [
            {
              path: '$HOME\\.codex\\config.toml',
              content: `model = "gpt-5.4"\n\n[api]\napi_key_env = "OPENAI_API_KEY"\nbase_url = "${baseUrl}"`,
            },
            {
              path: 'PowerShell',
              content: `$env:OPENAI_API_KEY="${apiKey}"`,
            },
          ],
        };
      }
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
  const { toast } = useToast();
  const baseUrl = window.location.origin;

  return (
    <Modal
      open={!!useKeyTarget}
      onClose={onClose}
      title={t('user_keys.use_key_title')}
      width="560px"
      footer={
        <Button onClick={onClose}>
          {t('common.close')}
        </Button>
      }
    >
      {useKeyValue ? (
        useKeyPlatform ? (
          <div className="space-y-4">
            <p className="text-sm text-text-secondary">
              {t('user_keys.use_key_desc')}
            </p>

            {/* 客户端选择 Tab（OpenAI 平台时显示） */}
            {showClientTabs && (
              <div className="flex gap-1 p-0.5 rounded-md bg-bg-hover">
                <button
                  onClick={() => setUseKeyTab('claude')}
                  className={`flex-1 px-3 py-1.5 text-xs font-medium rounded transition-colors ${
                    useKeyTab === 'claude'
                      ? 'bg-bg-elevated text-text shadow-sm'
                      : 'text-text-tertiary hover:text-text-secondary'
                  }`}
                >
                  Claude Code
                </button>
                <button
                  onClick={() => setUseKeyTab('codex')}
                  className={`flex-1 px-3 py-1.5 text-xs font-medium rounded transition-colors ${
                    useKeyTab === 'codex'
                      ? 'bg-bg-elevated text-text shadow-sm'
                      : 'text-text-tertiary hover:text-text-secondary'
                  }`}
                >
                  Codex CLI
                </button>
              </div>
            )}

            {/* OS/Shell Tab */}
            <div className="flex gap-1 p-0.5 rounded-md bg-bg-hover">
              <button
                onClick={() => setUseKeyShell('unix')}
                className={`flex-1 px-3 py-1.5 text-xs font-medium rounded transition-colors ${
                  useKeyShell === 'unix'
                    ? 'bg-bg-elevated text-text shadow-sm'
                    : 'text-text-tertiary hover:text-text-secondary'
                }`}
              >
                macOS / Linux
              </button>
              <button
                onClick={() => setUseKeyShell('cmd')}
                className={`flex-1 px-3 py-1.5 text-xs font-medium rounded transition-colors ${
                  useKeyShell === 'cmd'
                    ? 'bg-bg-elevated text-text shadow-sm'
                    : 'text-text-tertiary hover:text-text-secondary'
                }`}
              >
                Windows CMD
              </button>
              <button
                onClick={() => setUseKeyShell('powershell')}
                className={`flex-1 px-3 py-1.5 text-xs font-medium rounded transition-colors ${
                  useKeyShell === 'powershell'
                    ? 'bg-bg-elevated text-text shadow-sm'
                    : 'text-text-tertiary hover:text-text-secondary'
                }`}
              >
                PowerShell
              </button>
            </div>

            {/* 配置代码块 */}
            {getUseKeyConfig(baseUrl, useKeyPlatform, useKeyTab, useKeyShell, useKeyValue).files.map(
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
                      <button
                        onClick={() => {
                          navigator.clipboard.writeText(file.content);
                          toast('success', t('user_keys.copied'));
                        }}
                        className="flex items-center gap-1 px-2 py-0.5 text-xs rounded hover:bg-bg-elevated text-text-secondary transition-colors"
                      >
                        <Copy className="w-3 h-3" />
                        {t('user_keys.copy')}
                      </button>
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
    </Modal>
  );
}
