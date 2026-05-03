import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Checkbox, Label, ListBox, Modal, Select, Spinner, useOverlayState } from '@heroui/react';
import { User } from 'lucide-react';
import { AirGateDatePicker } from '../../../shared/components/AirGateDatePicker';
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

  const handleClose = () => {
    setSelectedUserIds([]);
    setGroupId(0);
    setExpiresAt('');
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
  const groupOptions = [
    { id: '0', label: t('subscriptions.select_group') },
    ...groups.map((group) => ({
      id: String(group.id),
      label: `${group.name} (${group.platform})`,
    })),
  ];
  const selectedGroupLabel = groupOptions.find((item) => item.id === String(groupId))?.label ?? t('subscriptions.select_group');
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) handleClose();
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
              <Modal.Heading>{t('subscriptions.bulk_assign')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <div className="space-y-1.5">
                  <p className="text-xs font-medium uppercaser text-text-secondary">
                    {t('subscriptions.select_users')} <span className="text-danger">*</span>
                  </p>
                  <div className="max-h-48 space-y-0.5 overflow-y-auto rounded-md border border-glass-border bg-bg-surface p-2">
                    {users.map((user) => (
                      <Checkbox
                        key={user.id}
                        isSelected={selectedUserIds.includes(user.id)}
                        onChange={(selected) => toggleUser(user.id, selected)}
                      >
                        <span className="inline-flex items-center gap-2">
                          <User className="h-3.5 w-3.5 text-text-tertiary" />
                          <span className="text-sm">
                            {user.email} ({user.username || '-'})
                          </span>
                        </span>
                      </Checkbox>
                    ))}
                  </div>
                  <p className="font-mono text-xs text-text-tertiary">
                    {t('subscriptions.selected_count', { count: selectedUserIds.length })}
                  </p>
                </div>

                <Select
                  fullWidth
                  isRequired
                  selectedKey={String(groupId)}
                  onSelectionChange={(key) => setGroupId(key == null ? 0 : Number(key))}
                >
                  <Label>{t('subscriptions.group')}</Label>
                  <Select.Trigger>
                    <Select.Value>{selectedGroupLabel}</Select.Value>
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

                <AirGateDatePicker
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
