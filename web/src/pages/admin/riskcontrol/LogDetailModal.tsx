import { useTranslation } from 'react-i18next';
import { Button, Chip, Modal, useOverlayState } from '@heroui/react';
import { Trash2 } from 'lucide-react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { riskControlApi, type ModerationLog } from '../../../shared/api/riskControl';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../../shared/queryKeys';

interface LogDetailModalProps {
  log: ModerationLog | null;
  open: boolean;
  onClose: () => void;
}

// 命中详情：输入摘录 + 各类别分值与阈值快照对照 + 命中哈希管理。
export function LogDetailModal({ log, open, onClose }: LogDetailModalProps) {
  const { t } = useTranslation();

  const deleteHashMutation = useCrudMutation({
    mutationFn: (hash: string) => riskControlApi.deleteHash(hash),
    successMessage: t('risk_control.hash_deleted'),
    queryKey: queryKeys.riskControlStatus(),
  });

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
    },
  });

  if (!log) return null;

  const categories = Object.keys(log.category_scores ?? {}).sort(
    (a, b) => (log.category_scores[b] ?? 0) - (log.category_scores[a] ?? 0),
  );

  const row = (label: string, value: React.ReactNode) => (
    <div className="flex gap-3 text-sm py-1.5 border-b border-default-100 last:border-0">
      <div className="w-32 shrink-0 text-default-500">{label}</div>
      <div className="min-w-0 break-all">{value}</div>
    </div>
  );

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" size="lg">
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{t('risk_control.log_detail')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="max-h-[60vh] overflow-y-auto space-y-4">
                <div>
                  {row(t('risk_control.col_request_id'), <code className="font-mono text-xs">{log.request_id}</code>)}
                  {row(t('risk_control.col_user'), `${log.user_email || '-'} (#${log.user_id || '-'})`)}
                  {row(t('risk_control.col_endpoint'), `${log.endpoint}（${log.protocol}）`)}
                  {row(t('risk_control.col_model'), log.model || '-')}
                  {row(t('risk_control.col_action'), (
                    <Chip color={log.flagged ? 'danger' : 'default'} size="sm">
                      {t(`risk_control.action_${log.action}`, log.action)}
                    </Chip>
                  ))}
                  {log.matched_keyword ? row(t('risk_control.col_keyword'), log.matched_keyword) : null}
                  {row(t('risk_control.col_violation'), String(log.violation_count))}
                  {log.error ? row(t('risk_control.col_error'), <span className="text-danger">{log.error}</span>) : null}
                  {row(t('risk_control.col_time'), new Date(log.created_at).toLocaleString())}
                </div>

                <div>
                  <div className="text-sm font-medium mb-1">{t('risk_control.input_excerpt')}</div>
                  <div className="text-sm bg-default-50 rounded-lg p-3 whitespace-pre-wrap break-all">
                    {log.input_excerpt || '-'}
                  </div>
                </div>

                {categories.length > 0 && (
                  <div>
                    <div className="text-sm font-medium mb-1">{t('risk_control.scores_vs_thresholds')}</div>
                    <div className="space-y-1">
                      {categories.map((category) => {
                        const score = log.category_scores[category] ?? 0;
                        const threshold = log.threshold_snapshot?.[category];
                        const hit = threshold != null && score >= threshold;
                        return (
                          <div key={category} className="flex items-center gap-2 text-sm">
                            <span className="font-mono text-xs w-52 truncate">{category}</span>
                            <span className={hit ? 'text-danger font-semibold' : ''}>{score.toFixed(3)}</span>
                            <span className="text-default-400 text-xs">
                              / {threshold != null ? threshold.toFixed(2) : '-'}
                            </span>
                          </div>
                        );
                      })}
                    </div>
                  </div>
                )}

                {log.input_hash ? (
                  <div className="flex items-center gap-2 text-sm">
                    <span className="text-default-500">{t('risk_control.input_hash')}</span>
                    <code className="font-mono text-xs truncate">{log.input_hash}</code>
                    <Button
                      isDisabled={deleteHashMutation.isPending}
                      size="sm"
                      variant="ghost"
                      onPress={() => deleteHashMutation.mutate(log.input_hash)}
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                      {t('risk_control.delete_hash')}
                    </Button>
                  </div>
                ) : null}
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="ghost" onPress={onClose}>{t('common.close')}</Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
