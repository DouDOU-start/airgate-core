import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Checkbox, Input, Label, ListBox, Modal, Select, Spinner, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { Search, User } from 'lucide-react';
import { CommonDatePicker } from '../../../shared/components/CommonDatePicker';
import type {
  BulkAssignReq,
  GroupResp,
  UserResp,
} from '../../../shared/types';

export function BulkAssignModal({
  open,
  groups,
  users,
  onClose,
  onSubmit,
  loading,
}: {
  open: boolean;
  groups: GroupResp[];
  users: UserResp[];
  onClose: () => void;
  onSubmit: (data: BulkAssignReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const [selectedUserIds, setSelectedUserIds] = useState<number[]>([]);
  const [groupId, setGroupId] = useState(0);
  const [expiresAt, setExpiresAt] = useState('');
  const [userKeyword, setUserKeyword] = useState('');

  const handleClose = () => {
    setSelectedUserIds([]);
    setGroupId(0);
    setExpiresAt('');
    setUserKeyword('');
    onClose();
  };

  const toggleUser = (userId: number, selected: boolean) => {
    setSelectedUserIds((current) =>
      selected
        ? [...new Set([...current, userId])]
        : current.filter((id) => id !== userId),
    );
  };

  const handleSubmit = () => {
    if (selectedUserIds.length === 0 || !groupId || !expiresAt) return;
    onSubmit({
      expires_at: expiresAt,
      group_id: groupId,
      user_ids: selectedUserIds,
    });
  };
  const groupOptions = groups.map((group) => ({
    id: String(group.id),
    label: `${group.name} (${group.platform})`,
  }));
  const selectedGroupLabel = groupOptions.find((item) => item.id === String(groupId))?.label;
  const selectedUserIdSet = useMemo(() => new Set(selectedUserIds), [selectedUserIds]);
  const filteredUsers = useMemo(() => {
    const keyword = userKeyword.trim().toLowerCase();
    if (!keyword) return users;
    return users.filter((user) =>
      user.email.toLowerCase().includes(keyword) ||
      (user.username ?? '').toLowerCase().includes(keyword) ||
      String(user.id).includes(keyword),
    );
  }, [userKeyword, users]);
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) handleClose();
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
              <Modal.Heading>{t('subscriptions.bulk_assign')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <div className="space-y-2">
                  <div className="flex items-center justify-between gap-3">
                    <Label>
                      {t('subscriptions.select_users')} <span className="text-danger">*</span>
                    </Label>
                    <span className="font-mono text-xs text-text-tertiary">
                      {t('subscriptions.selected_count', { count: selectedUserIds.length })}
                    </span>
                  </div>
                  <div className="relative">
                    <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                    <Input
                      className="pl-9"
                      value={userKeyword}
                      onChange={(event) => setUserKeyword(event.target.value)}
                      placeholder={t('users.search_placeholder')}
                    />
                  </div>
                  <div className="grid max-h-56 gap-2 overflow-y-auto rounded-md border border-glass-border bg-surface p-2">
                    {filteredUsers.length === 0 ? (
                      <div className="flex min-h-20 items-center justify-center text-sm text-text-tertiary">
                        {t('common.no_data')}
                      </div>
                    ) : filteredUsers.map((user) => {
                      const isSelected = selectedUserIdSet.has(user.id);
                      return (
                        <Checkbox
                          key={user.id}
                          className={`w-full rounded-md border p-2.5 transition-colors ${
                            isSelected
                              ? 'border-primary bg-primary-subtle'
                              : 'border-border-subtle bg-bg-surface hover:bg-bg-hover'
                          }`}
                          isSelected={isSelected}
                          onChange={(selected) => toggleUser(user.id, selected)}
                        >
                          <Checkbox.Control className={isSelected ? 'border-primary bg-primary text-primary-foreground' : undefined}>
                            <Checkbox.Indicator />
                          </Checkbox.Control>
                          <Checkbox.Content>
                            <span className="flex min-w-0 items-center gap-2">
                              <User className={isSelected ? 'h-3.5 w-3.5 shrink-0 text-primary' : 'h-3.5 w-3.5 shrink-0 text-text-tertiary'} />
                              <span className="min-w-0 text-left">
                                <span className="block truncate text-sm font-medium text-text">{user.email}</span>
                                <span className="block truncate text-xs text-text-tertiary">{user.username || '-'}</span>
                              </span>
                            </span>
                          </Checkbox.Content>
                        </Checkbox>
                      );
                    })}
                  </div>
                </div>

                <Select
                  fullWidth
                  isRequired
                  selectedKey={groupId ? String(groupId) : null}
                  onSelectionChange={(key) => setGroupId(key == null ? 0 : Number(key))}
                >
                  <Label>{t('subscriptions.group')}</Label>
                  <Select.Trigger>
                    <Select.Value>
                      {selectedGroupLabel ?? <span className="text-text-tertiary">{t('subscriptions.select_group')}</span>}
                    </Select.Value>
                    <Select.Indicator />
                  </Select.Trigger>
                  <Select.Popover>
                    <ListBox items={groupOptions}>
                      {(item) => (
                        <ListBox.Item id={item.id} textValue={item.label}>
                          {item.label}
                        </ListBox.Item>
                      )}
                    </ListBox>
                  </Select.Popover>
                </Select>

                <CommonDatePicker
                  isRequired
                  label={t('subscriptions.expire_time')}
                  value={expiresAt ? expiresAt.split('T')[0] : ''}
                  onChange={(value) => setExpiresAt(value ? `${value}T23:59:59Z` : '')}
                />
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={handleClose}>
                {t('common.cancel')}
              </Button>
              <Button variant="primary" isDisabled={loading} onPress={handleSubmit}>
                {loading ? <Spinner size="sm" /> : null}
                {t('subscriptions.bulk_assign_count', { count: selectedUserIds.length })}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
