import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Bell, ChevronDown, ChevronUp } from 'lucide-react';
import { Button, EmptyState, Modal, Spinner, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../DialogTriggerShim';
import { announcementsApi } from '../../api/announcements';
import { queryKeys } from '../../queryKeys';
import { formatDateTime } from '../../utils/format';
import { AnnouncementMarkdown } from './AnnouncementMarkdown';

// 公告拉取节奏：与 sub2api 同款 20 分钟兜底轮询；后台刷新不触发全局加载条。
export const ANNOUNCEMENTS_REFETCH_INTERVAL = 20 * 60 * 1000;

export function useMyAnnouncements() {
  return useQuery({
    queryKey: queryKeys.myAnnouncements(),
    queryFn: () => announcementsApi.listMine(),
    staleTime: 60_000,
    refetchInterval: ANNOUNCEMENTS_REFETCH_INTERVAL,
    meta: { globalLoading: false },
  });
}

export function AnnouncementBell() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [expandedId, setExpandedId] = useState<number | null>(null);

  const { data, isLoading } = useMyAnnouncements();
  const announcements = data ?? [];
  const unreadCount = announcements.filter((item) => !item.read_at).length;

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: queryKeys.myAnnouncements() });

  const markReadMutation = useMutation({
    mutationFn: (id: number) => announcementsApi.markRead(id),
    onSuccess: invalidate,
  });
  const markAllReadMutation = useMutation({
    mutationFn: () => announcementsApi.markAllRead(),
    onSuccess: invalidate,
  });

  const toggleExpand = (id: number, unread: boolean) => {
    const next = expandedId === id ? null : id;
    setExpandedId(next);
    // 展开未读公告即视为已读
    if (next !== null && unread) {
      markReadMutation.mutate(id);
    }
  };

  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      setOpen(nextOpen);
      if (!nextOpen) setExpandedId(null);
    },
  });

  return (
    <>
      <Button
        aria-label={t('announcements.bell_title')}
        className="relative h-10 w-10"
        isIconOnly
        size="sm"
        variant="ghost"
        onPress={() => setOpen(true)}
      >
        <Bell className="h-5 w-5" />
        {unreadCount > 0 && (
          <span
            aria-hidden
            className="absolute right-1.5 top-1.5 flex h-2 w-2"
          >
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-danger opacity-75" />
            <span className="relative inline-flex h-2 w-2 rounded-full bg-danger" />
          </span>
        )}
      </Button>

      <Modal state={modalState}>
        <DialogTriggerShim />
        <Modal.Backdrop>
          <Modal.Container placement="center" scroll="inside" size="md">
            <Modal.Dialog
              className="ag-elevation-modal"
              style={{ maxWidth: '560px', width: 'min(100%, calc(100vw - 2rem))' }}
            >
              <Modal.Header>
                <Modal.Heading>
                  <span className="inline-flex items-center gap-2">
                    {t('announcements.bell_title')}
                    {unreadCount > 0 && (
                      <span className="rounded-full bg-danger px-1.5 py-0.5 text-[10px] font-semibold leading-none text-white">
                        {unreadCount}
                      </span>
                    )}
                  </span>
                </Modal.Heading>
                <Modal.CloseTrigger />
              </Modal.Header>
              <Modal.Body>
                {isLoading ? (
                  <div className="flex justify-center py-8">
                    <Spinner size="sm" />
                  </div>
                ) : announcements.length === 0 ? (
                  <EmptyState>
                    <div className="text-sm text-default-500">{t('announcements.empty')}</div>
                  </EmptyState>
                ) : (
                  <div className="space-y-2">
                    {announcements.map((item) => {
                      const unread = !item.read_at;
                      const expanded = expandedId === item.id;
                      return (
                        <div
                          key={item.id}
                          className="rounded-[var(--radius)] border border-border"
                        >
                          <button
                            type="button"
                            className="flex w-full items-center gap-2 px-3 py-2.5 text-left"
                            onClick={() => toggleExpand(item.id, unread)}
                          >
                            <span
                              className={`h-1.5 w-1.5 shrink-0 rounded-full ${unread ? 'bg-danger' : 'bg-transparent'}`}
                            />
                            <span className="min-w-0 flex-1">
                              <span className={`block truncate text-sm ${unread ? 'font-semibold text-text' : 'text-text-secondary'}`}>
                                {item.title}
                              </span>
                              <span className="block text-xs text-text-tertiary">
                                {formatDateTime(item.created_at)}
                              </span>
                            </span>
                            {expanded
                              ? <ChevronUp className="h-4 w-4 shrink-0 text-text-tertiary" />
                              : <ChevronDown className="h-4 w-4 shrink-0 text-text-tertiary" />}
                          </button>
                          {expanded && (
                            <div className="border-t border-border px-3 py-2.5">
                              <AnnouncementMarkdown content={item.content} />
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
              </Modal.Body>
              {unreadCount > 0 && (
                <Modal.Footer>
                  <Button
                    isDisabled={markAllReadMutation.isPending}
                    size="sm"
                    variant="secondary"
                    onPress={() => markAllReadMutation.mutate()}
                  >
                    {markAllReadMutation.isPending ? <Spinner size="sm" /> : null}
                    {t('announcements.mark_all_read')}
                  </Button>
                </Modal.Footer>
              )}
            </Modal.Dialog>
          </Modal.Container>
        </Modal.Backdrop>
      </Modal>
    </>
  );
}
