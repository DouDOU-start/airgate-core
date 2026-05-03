import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Card, Checkbox, Link as HeroLink, Modal, useOverlayState } from '@heroui/react';
import {
  Download, RefreshCw, ExternalLink, Copy, ShieldAlert, CheckCircle2,
  XCircle, Loader2, Info,
} from 'lucide-react';
import { useToast } from '../../shared/ui';
import { useClipboard } from '../../shared/hooks/useClipboard';
import { upgradeApi, type UpgradeInfo, type UpgradeStatus } from '../../shared/api/upgrade';

// 升级流程进行中（状态机非终态）的判断。处于这些状态时按钮置灰、轮询保持开启。
const RUNNING_STATES = new Set<UpgradeStatus['state']>([
  'checking', 'downloading', 'verifying', 'swapping', 'restarting',
]);

export function SystemUpdatePanel() {
  const { t } = useTranslation();
  const { toast } = useToast();
  const copy = useClipboard();
  const queryClient = useQueryClient();

  const [upgradeOpen, setUpgradeOpen] = useState(false);
  const [confirmBackup, setConfirmBackup] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  const { data: info, isFetching: infoLoading, refetch: refetchInfo } = useQuery({
    queryKey: ['upgrade-info'],
    queryFn: () => upgradeApi.info(),
    staleTime: 60_000,
  });

  // 初始拉一次状态；之后只在升级流程期间轮询
  const { data: status } = useQuery({
    queryKey: ['upgrade-status'],
    queryFn: () => upgradeApi.status(),
    refetchInterval: (q) => {
      const s = q.state.data?.state;
      return s && RUNNING_STATES.has(s) ? 1500 : false;
    },
  });

  const running = !!status && RUNNING_STATES.has(status.state);

  // 升级成功后（状态切到 restarting）后端会主动退出，这里 5 秒后强制刷新页面，
  // 等 systemd 把新版本拉起来。前端 5 秒内反复刷 /upgrade/info 拿不到响应是正常的。
  useEffect(() => {
    if (status?.state === 'restarting') {
      const timer = setTimeout(() => window.location.reload(), 5000);
      return () => clearTimeout(timer);
    }
  }, [status?.state]);

  const handleRun = async () => {
    if (!confirmBackup) return;
    setSubmitting(true);
    try {
      await upgradeApi.run({ confirm_db_backup: true });
      toast('success', t('settings.system_run_started'));
      // 立即拉一次 status，触发自动轮询
      queryClient.invalidateQueries({ queryKey: ['upgrade-status'] });
    } catch (err) {
      toast('error', err instanceof Error ? err.message : String(err));
    } finally {
      setSubmitting(false);
    }
  };

  const renderStateBadge = () => {
    if (!info) return null;
    if (info.has_update) {
      return (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-warning/15 text-warning text-[11px] font-medium">
          <Info className="w-3 h-3" />
          {t('settings.system_has_update')}
        </span>
      );
    }
    if (info.latest) {
      return (
        <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-success/15 text-success text-[11px] font-medium">
          <CheckCircle2 className="w-3 h-3" />
          {t('settings.system_up_to_date')}
        </span>
      );
    }
    return null;
  };

  const renderActionArea = () => {
    if (!info) return null;

    if (info.mode === 'systemd') {
      return (
        <Button
          isDisabled={!info.can_upgrade || running}
          onPress={() => {
            setConfirmBackup(false);
            setUpgradeOpen(true);
          }}
        >
          <Download className="w-4 h-4" />
          {info.has_update
            ? t('settings.system_upgrade_to', { version: info.latest })
            : t('settings.system_no_update')}
        </Button>
      );
    }

    if (info.mode === 'docker') {
      return (
        <div className="space-y-2">
          <div className="text-xs text-text-tertiary">
            {t('settings.system_docker_hint')}
          </div>
          <div className="flex items-center gap-2">
            <code className="flex-1 px-3 py-2 rounded-md bg-bg-subtle border border-border text-xs font-mono text-text overflow-x-auto whitespace-nowrap">
              {info.instructions}
            </code>
            <Button
              variant="secondary"
              size="sm"
              onPress={() => copy(info.instructions || '')}
            >
              <Copy className="w-3.5 h-3.5" />
              {t('common.copy')}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onPress={() => refetchInfo()}
              isDisabled={infoLoading}
            >
              <RefreshCw className={`w-3.5 h-3.5 ${infoLoading ? 'animate-spin' : ''}`} />
              {t('settings.system_check_update')}
            </Button>
          </div>
        </div>
      );
    }

    // noop
    return (
      <div className="text-xs text-text-tertiary">
        {t('settings.system_noop_hint')}
        {info.release_url && (
          <HeroLink
            href={info.release_url}
            target="_blank"
            rel="noopener noreferrer"
            className="ml-1 inline-flex items-center gap-1 text-primary hover:underline"
          >
            {t('settings.system_view_release')}
            <ExternalLink className="w-3 h-3" />
          </HeroLink>
        )}
      </div>
    );
  };

  return (
    <Card>
      <Card.Header>
        <Card.Title>{t('settings.system_update_title')}</Card.Title>
      </Card.Header>
      <Card.Content>
        <div className="space-y-5">
          {/* 版本对照 */}
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div className="rounded-xl border border-border bg-bg-subtle px-4 py-3">
              <div className="text-[10px] uppercaser text-text-tertiary mb-1">
                {t('settings.system_current_version')}
              </div>
              <div className="text-lg font-mono font-semibold text-text">
                {info?.current ?? '—'}
              </div>
              {info?.binary_path && (
                <div className="text-[11px] text-text-tertiary mt-1 truncate" title={info.binary_path}>
                  {info.binary_path}
                </div>
              )}
            </div>
            <div className="rounded-xl border border-border bg-bg-subtle px-4 py-3">
              <div className="flex items-center justify-between mb-1">
                <span className="text-[10px] uppercaser text-text-tertiary">
                  {t('settings.system_latest_version')}
                </span>
                {renderStateBadge()}
              </div>
              <div className="text-lg font-mono font-semibold text-text">
                {info?.latest ?? '—'}
              </div>
              <div className="text-[11px] text-text-tertiary mt-1">
                {t('settings.system_mode')}：<span className="font-mono">{info?.mode ?? '—'}</span>
              </div>
            </div>
          </div>

          {/* 操作区 */}
          <div className="flex items-center justify-between gap-3 flex-wrap">
            <div className="flex-1 min-w-[200px]">{renderActionArea()}</div>
            {info?.mode !== 'docker' && (
              <Button
                variant="secondary"
                size="sm"
                onPress={() => refetchInfo()}
                isDisabled={infoLoading}
              >
                <RefreshCw className={`w-3.5 h-3.5 ${infoLoading ? 'animate-spin' : ''}`} />
                {t('settings.system_check_update')}
              </Button>
            )}
          </div>

        {/* Release notes */}
        {info?.release_notes && (
          <details className="rounded-xl border border-border bg-bg-subtle">
            <summary className="px-4 py-3 cursor-pointer text-xs font-medium text-text-secondary select-none">
              {t('settings.system_release_notes')}
              {info.release_url && (
                <HeroLink
                  href={info.release_url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="ml-2 text-primary hover:underline inline-flex items-center gap-1"
                  onClick={(e) => e.stopPropagation()}
                >
                  GitHub
                  <ExternalLink className="w-3 h-3" />
                </HeroLink>
              )}
            </summary>
            <pre className="px-4 pb-4 text-[11px] text-text-tertiary whitespace-pre-wrap font-mono max-h-80 overflow-y-auto">
              {info.release_notes}
            </pre>
          </details>
        )}

        {/* 上次失败时显示错误 */}
          {status?.state === 'failed' && (
            <Alert status="danger">
              <Alert.Content>
                <Alert.Title>{t('settings.system_last_failed')}</Alert.Title>
                <Alert.Description>
                  <div className="text-xs">
                    {status.message && <div>{status.message}</div>}
                    {status.error && <div className="font-mono mt-1">{status.error}</div>}
                  </div>
                </Alert.Description>
              </Alert.Content>
            </Alert>
          )}
        </div>
      </Card.Content>

      <UpgradeRunModal
        open={upgradeOpen}
        onClose={() => !running && setUpgradeOpen(false)}
        info={info}
        status={status}
        running={running}
        submitting={submitting}
        confirmBackup={confirmBackup}
        setConfirmBackup={setConfirmBackup}
        onRun={handleRun}
      />
    </Card>
  );
}

interface UpgradeRunModalProps {
  open: boolean;
  onClose: () => void;
  info: UpgradeInfo | undefined;
  status: UpgradeStatus | undefined;
  running: boolean;
  submitting: boolean;
  confirmBackup: boolean;
  setConfirmBackup: (v: boolean) => void;
  onRun: () => void;
}

function UpgradeRunModal({
  open, onClose, info, status, running, submitting, confirmBackup, setConfirmBackup, onRun,
}: UpgradeRunModalProps) {
  const { t } = useTranslation();
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  if (!info) return null;

  const showProgress = running || status?.state === 'failed' || status?.state === 'success';
  const progressPct = Math.round((status?.progress ?? 0) * 100);

  return (
    <Modal state={modalState}>
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '560px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{t('settings.system_upgrade_to', { version: info.latest })}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
        {!showProgress && (
          <>
            <Alert status="warning">
              <Alert.Content>
                <Alert.Title>{t('settings.system_warning_title')}</Alert.Title>
                <Alert.Description>
                  <ul className="text-xs list-disc pl-4 space-y-1">
                    <li>{t('settings.system_warning_backup')}</li>
                    <li>{t('settings.system_warning_restart')}</li>
                    <li>{t('settings.system_warning_rollback')}</li>
                  </ul>
                </Alert.Description>
              </Alert.Content>
            </Alert>

            <Checkbox
              className="items-start"
              isSelected={confirmBackup}
              onChange={setConfirmBackup}
            >
              <span className="text-xs text-text-secondary">
                {t('settings.system_confirm_backup')}
              </span>
            </Checkbox>
          </>
        )}

        {showProgress && status && (
          <div className="space-y-3">
            <div className="flex items-center gap-2">
              {status.state === 'failed' ? (
                <XCircle className="w-5 h-5 text-danger" />
              ) : status.state === 'restarting' || status.state === 'success' ? (
                <CheckCircle2 className="w-5 h-5 text-success" />
              ) : (
                <Loader2 className="w-5 h-5 animate-spin text-primary" />
              )}
              <div className="text-sm font-medium text-text">
                {t(`settings.system_state_${status.state}`)}
              </div>
            </div>

            {status.state === 'downloading' && (
              <div>
                <div className="h-2 rounded-full bg-bg-subtle overflow-hidden">
                  <div
                    className="h-full bg-primary transition-all duration-300"
                    style={{ width: `${progressPct}%` }}
                  />
                </div>
                <div className="text-[11px] text-text-tertiary mt-1 text-right font-mono">
                  {progressPct}%
                </div>
              </div>
            )}

            {status.message && (
              <div className="text-xs text-text-tertiary font-mono break-all">
                {status.message}
              </div>
            )}

            {status.state === 'failed' && status.error && (
              <Alert status="danger">
                <Alert.Content>
                  <Alert.Title>{t('settings.system_failed_title')}</Alert.Title>
                  <Alert.Description>
                    <div className="text-xs font-mono break-all">{status.error}</div>
                  </Alert.Description>
                </Alert.Content>
              </Alert>
            )}

            {status.state === 'restarting' && (
              <Alert status="accent">
                <Alert.Content>
                  <Alert.Description>
                    <div className="text-xs flex items-center gap-2">
                      <ShieldAlert className="w-4 h-4 flex-shrink-0" />
                      {t('settings.system_restarting_hint')}
                    </div>
                  </Alert.Description>
                </Alert.Content>
              </Alert>
            )}
          </div>
        )}
              </div>
            </Modal.Body>
            {!running ? (
              <Modal.Footer>
                <div className="flex items-center justify-end gap-2">
                  <Button variant="secondary" onPress={onClose} isDisabled={submitting}>
                    {t('common.cancel')}
                  </Button>
                  <Button
                    onPress={onRun}
                    isDisabled={!confirmBackup || submitting}
                    aria-busy={submitting}
                  >
                    <Download className="w-4 h-4" />
                    {t('settings.system_start_upgrade')}
                  </Button>
                </div>
              </Modal.Footer>
            ) : null}
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
