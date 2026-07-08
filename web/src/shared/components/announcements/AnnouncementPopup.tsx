import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Megaphone } from 'lucide-react';
import { Button, Modal, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../DialogTriggerShim';
import { announcementsApi } from '../../api/announcements';
import { queryKeys } from '../../queryKeys';
import { formatDateTime } from '../../utils/format';
import { AnnouncementMarkdown } from './AnnouncementMarkdown';
import { useMyAnnouncements } from './AnnouncementBell';

// AnnouncementPopup 弹窗强提醒：notify_mode=popup 的未读公告逐条弹出，
// 关闭即标记已读。会话内已弹过的 ID 记入 ref 去重，防止 invalidate 后重复弹。
export function AnnouncementPopup() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { data } = useMyAnnouncements();
  const shownIdsRef = useRef<Set<number>>(new Set());
  // dismissing 记录正在关闭的公告 ID：markRead 尚未落库前先本地隐藏，保证弹窗立即消失
  const [dismissingId, setDismissingId] = useState<number | null>(null);

  const markReadMutation = useMutation({
    mutationFn: (id: number) => announcementsApi.markRead(id),
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: queryKeys.myAnnouncements() }),
  });

  const queue = (data ?? []).filter(
    (item) =>
      item.notify_mode === 'popup'
      && !item.read_at
      && !shownIdsRef.current.has(item.id)
      && item.id !== dismissingId,
  );
  const current = queue[0] ?? null;

  const dismiss = () => {
    if (!current) return;
    shownIdsRef.current.add(current.id);
    setDismissingId(current.id);
    markReadMutation.mutate(current.id);
  };

  const modalState = useOverlayState({
    isOpen: !!current,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) dismiss();
    },
  });

  if (!current) return null;

  return (
    <Modal state={modalState}>
      <DialogTriggerShim />
      <Modal.Backdrop>
        <Modal.Container placement="center" scroll="inside" size="md">
          <Modal.Dialog
            className="ag-elevation-modal"
            style={{ maxWidth: '520px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Icon className="bg-accent-soft text-accent-soft-foreground">
                <Megaphone className="h-5 w-5" />
              </Modal.Icon>
              <Modal.Heading>{current.title}</Modal.Heading>
              <p className="mt-1 text-xs text-text-tertiary">
                {formatDateTime(current.created_at)}
              </p>
            </Modal.Header>
            <Modal.Body>
              <AnnouncementMarkdown content={current.content} />
            </Modal.Body>
            <Modal.Footer>
              {queue.length > 1 && (
                <span className="mr-auto self-center text-xs text-text-tertiary">
                  {t('announcements.popup_remaining', { count: queue.length - 1 })}
                </span>
              )}
              <Button variant="primary" onPress={dismiss}>
                {t('announcements.popup_ack')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
