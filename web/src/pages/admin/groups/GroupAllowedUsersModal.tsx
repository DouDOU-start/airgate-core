import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Button, ComboBox, Input, ListBox, Modal, Spinner, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { Plus, Search, Trash2 } from 'lucide-react';
import { groupsApi } from '../../../shared/api/groups';
import { usersApi } from '../../../shared/api/users';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import { useDebouncedValue } from '../../../shared/hooks/useDebouncedValue';
import { queryKeys } from '../../../shared/queryKeys';
import type { GroupResp, GroupAllowedUserResp, UserResp } from '../../../shared/types';

interface GroupAllowedUsersModalProps {
  open: boolean;
  group: GroupResp;
  onClose: () => void;
}

// 专属分组用户管理：查看/新增/移除获准访问该专属分组的用户，交互结构与分组专属倍率弹窗一致。
export function GroupAllowedUsersModal({ open, group, onClose }: GroupAllowedUsersModalProps) {
  const { t } = useTranslation();
  const [emailQuery, setEmailQuery] = useState('');
  const [pickedUser, setPickedUser] = useState<UserResp | null>(null);
  const debouncedEmailQuery = useDebouncedValue(emailQuery.trim(), 250);

  const allowedKey = ['group-allowed-users', group.id] as const;
  const { data: allowedUsers = [], isLoading } = useQuery({
    queryKey: allowedKey,
    queryFn: () => groupsApi.listAllowedUsers(group.id),
    enabled: open,
  });

  const { data: searchData } = useQuery({
    queryKey: queryKeys.users('group-allowed-users-search', debouncedEmailQuery),
    queryFn: () => usersApi.list({
      page: 1,
      page_size: 20,
      keyword: debouncedEmailQuery || undefined,
    }),
    enabled: open && !pickedUser,
  });

  const grantMutation = useCrudMutation({
    mutationFn: (userId: number) => groupsApi.grantAllowedUser(group.id, userId),
    successMessage: t('groups.allowed_users_grant_success'),
    queryKey: allowedKey,
    onSuccess: () => {
      setEmailQuery('');
      setPickedUser(null);
    },
  });

  const revokeMutation = useCrudMutation({
    mutationFn: (userId: number) => groupsApi.revokeAllowedUser(group.id, userId),
    successMessage: t('groups.allowed_users_revoke_success'),
    queryKey: allowedKey,
  });

  const existingUserIds = useMemo(
    () => new Set((allowedUsers as GroupAllowedUserResp[]).map((row) => row.user_id)),
    [allowedUsers],
  );
  const searchResults = useMemo(
    () => (searchData?.list ?? []).filter((user) => !existingUserIds.has(user.id)),
    [existingUserIds, searchData?.list],
  );
  const searchOptions = useMemo(
    () => searchResults.map((user) => ({
      id: String(user.id),
      label: user.email,
      description: user.username,
      textValue: `${user.email} ${user.username ?? ''}`,
    })),
    [searchResults],
  );
  const visibleSearchOptions = useMemo(() => {
    if (!pickedUser || searchOptions.some((option) => option.id === String(pickedUser.id))) {
      return searchOptions;
    }
    return [
      {
        id: String(pickedUser.id),
        label: pickedUser.email,
        description: pickedUser.username,
        textValue: `${pickedUser.email} ${pickedUser.username ?? ''}`,
      },
      ...searchOptions,
    ];
  }, [pickedUser, searchOptions]);

  const handleAdd = () => {
    if (!pickedUser) return;
    grantMutation.mutate(pickedUser.id);
  };

  const modalState = useOverlayState({
    isOpen: open,
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
            style={{ maxWidth: '640px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{t('groups.allowed_users_title')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="mb-4 flex items-center gap-3 rounded-lg border border-glass-border px-3 py-2.5 text-sm">
                <span className="font-medium text-text">{group.name}</span>
                <span className="text-text-tertiary">|</span>
                <span className="text-text-tertiary">{t('groups.allowed_users_hint')}</span>
              </div>

              <div className="mb-4">
                <p className="mb-2 text-xs font-medium uppercase text-text-secondary">
                  {t('groups.allowed_users_add')}
                </p>
                <div className="flex items-start gap-2">
                  <div className="flex-1">
                    <ComboBox
                      aria-label={t('groups.rate_override_search_placeholder')}
                      allowsEmptyCollection
                      fullWidth
                      inputValue={emailQuery}
                      items={visibleSearchOptions}
                      menuTrigger="focus"
                      selectedKey={pickedUser ? String(pickedUser.id) : null}
                      onInputChange={(value) => {
                        setEmailQuery(value);
                        if (pickedUser && value !== pickedUser.email) {
                          setPickedUser(null);
                        }
                      }}
                      onSelectionChange={(key) => {
                        const value = key == null ? '' : String(key);
                        if (!value) {
                          setPickedUser(null);
                          setEmailQuery('');
                          return;
                        }
                        const user = searchResults.find((item) => String(item.id) === value)
                          ?? (pickedUser && String(pickedUser.id) === value ? pickedUser : null);
                        setPickedUser(user ?? null);
                        setEmailQuery(user?.email ?? '');
                      }}
                    >
                      <ComboBox.InputGroup className="relative">
                        <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                        <Input className="pl-9 pr-10" placeholder={t('groups.rate_override_search_placeholder') ?? ''} />
                        <ComboBox.Trigger
                          className="ag-combobox-preview-trigger absolute right-1 top-1/2 z-10 h-7 w-7 min-w-0 -translate-y-1/2 p-0 text-text-tertiary hover:text-text"
                        />
                      </ComboBox.InputGroup>
                      <ComboBox.Popover>
                        <ListBox
                          items={visibleSearchOptions}
                          renderEmptyState={() => (
                            <div className="px-3 py-6 text-center text-xs text-text-tertiary">
                              {debouncedEmailQuery ? t('common.no_data') : t('users.search_placeholder')}
                            </div>
                          )}
                        >
                          {(item) => (
                            <ListBox.Item id={item.id} textValue={item.textValue}>
                              <div className="min-w-0">
                                <div className="truncate text-sm text-text">{item.label}</div>
                                {item.description ? (
                                  <div className="truncate text-xs text-text-tertiary">{item.description}</div>
                                ) : null}
                              </div>
                            </ListBox.Item>
                          )}
                        </ListBox>
                      </ComboBox.Popover>
                    </ComboBox>
                  </div>
                  <Button
                    variant="primary"
                    isDisabled={!pickedUser || grantMutation.isPending}
                    onPress={handleAdd}
                  >
                    {grantMutation.isPending ? <Spinner size="sm" /> : <Plus className="h-3.5 w-3.5" />}
                    {t('common.add')}
                  </Button>
                </div>
              </div>

              <div>
                <p className="mb-2 text-xs font-medium uppercase text-text-secondary">
                  {t('groups.allowed_users_list', { count: allowedUsers.length })}
                </p>
                {isLoading ? (
                  <p className="py-8 text-center text-sm text-text-tertiary">{t('common.loading')}</p>
                ) : allowedUsers.length === 0 ? (
                  <p className="py-8 text-center text-sm text-text-tertiary">{t('groups.allowed_users_empty')}</p>
                ) : (
                  <div className="overflow-hidden rounded-lg border border-glass-border">
                    {(allowedUsers as GroupAllowedUserResp[]).map((row, index) => (
                      <div
                        key={row.user_id}
                        className={`flex items-center gap-3 px-3 py-2.5 text-sm ${index === 0 ? '' : 'border-t border-glass-border'}`}
                      >
                        <div className="min-w-0 flex-1">
                          <div className="truncate text-text">{row.email}</div>
                          {row.username ? (
                            <div className="truncate text-[11px] text-text-tertiary">{row.username}</div>
                          ) : null}
                        </div>
                        <Button
                          isIconOnly
                          size="sm"
                          variant="ghost"
                          className="text-danger"
                          isDisabled={revokeMutation.isPending}
                          onPress={() => revokeMutation.mutate(row.user_id)}
                        >
                          {revokeMutation.isPending && revokeMutation.variables === row.user_id
                            ? <Spinner size="sm" />
                            : <Trash2 className="h-3.5 w-3.5" />}
                        </Button>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>
                {t('common.close')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
