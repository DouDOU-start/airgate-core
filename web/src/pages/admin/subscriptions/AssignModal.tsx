import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Label, ListBox, Modal, Select, Spinner, useOverlayState } from '@heroui/react';
import { AirGateDatePicker } from '../../../shared/components/AirGateDatePicker';
import type {
  AssignSubscriptionReq,
  GroupResp,
  UserResp,
} from '../../../shared/types';

export function AssignModal({
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
  onSubmit: (data: AssignSubscriptionReq) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const [form, setForm] = useState<AssignSubscriptionReq>({
    expires_at: '',
    group_id: 0,
    user_id: 0,
  });

  const handleClose = () => {
    setForm({ user_id: 0, group_id: 0, expires_at: '' });
    onClose();
  };

  const handleSubmit = () => {
    if (!form.user_id || !form.group_id || !form.expires_at) return;
    onSubmit(form);
  };
  const userOptions = [
    { id: '0', label: t('subscriptions.select_user') },
    ...users.map((user) => ({
      id: String(user.id),
      label: `${user.email} (${user.username || '-'})`,
    })),
  ];
  const groupOptions = [
    { id: '0', label: t('subscriptions.select_group') },
    ...groups.map((group) => ({
      id: String(group.id),
      label: `${group.name} (${group.platform})`,
    })),
  ];
  const selectedUserLabel = userOptions.find((item) => item.id === String(form.user_id))?.label ?? t('subscriptions.select_user');
  const selectedGroupLabel = groupOptions.find((item) => item.id === String(form.group_id))?.label ?? t('subscriptions.select_group');
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
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{t('subscriptions.assign')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <Select
                  fullWidth
                  isRequired
                  selectedKey={String(form.user_id)}
                  onSelectionChange={(key) => setForm({ ...form, user_id: key == null ? 0 : Number(key) })}
                >
                  <Label>{t('subscriptions.user')}</Label>
                  <Select.Trigger>
                    <Select.Value>{selectedUserLabel}</Select.Value>
                    <Select.Indicator />
                  </Select.Trigger>
                  <Select.Popover>
                    <ListBox items={userOptions}>
                      {(item) => (
                        <ListBox.Item id={item.id} textValue={item.label}>
                          {item.label}
                        </ListBox.Item>
                      )}
                    </ListBox>
                  </Select.Popover>
                </Select>

                <Select
                  fullWidth
                  isRequired
                  selectedKey={String(form.group_id)}
                  onSelectionChange={(key) => setForm({ ...form, group_id: key == null ? 0 : Number(key) })}
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
                  value={form.expires_at ? form.expires_at.split('T')[0] : ''}
                  onChange={(value) => setForm({ ...form, expires_at: value ? `${value}T23:59:59Z` : '' })}
                />
              </div>
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={handleClose}>
                {t('common.cancel')}
              </Button>
              <Button variant="primary" isDisabled={loading} onPress={handleSubmit}>
                {loading ? <Spinner size="sm" /> : null}
                {t('subscriptions.assign')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
