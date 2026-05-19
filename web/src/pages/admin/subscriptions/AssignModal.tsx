import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, ComboBox, Input, Label, ListBox, Modal, Select, Spinner, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { Search } from 'lucide-react';
import { CommonDatePicker } from '../../../shared/components/CommonDatePicker';
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
  const [userKeyword, setUserKeyword] = useState('');
  const [selectedUserLabel, setSelectedUserLabel] = useState('');

  const handleClose = () => {
    setForm({ user_id: 0, group_id: 0, expires_at: '' });
    setUserKeyword('');
    setSelectedUserLabel('');
    onClose();
  };

  const handleUserInputChange = (value: string) => {
    if (form.user_id) {
      if (value === selectedUserLabel) return;
      setSelectedUserLabel('');
      setForm((current) => ({ ...current, user_id: 0 }));
      setUserKeyword('');
      return;
    }
    setUserKeyword(value);
  };

  const handleSubmit = () => {
    if (!form.user_id || !form.group_id || !form.expires_at) return;
    onSubmit(form);
  };
  const userOptions = useMemo(() => users.map((user) => ({
    id: String(user.id),
    label: user.email,
    description: user.username || '-',
    matchText: `${user.id} ${user.email} ${user.username ?? ''}`.toLowerCase(),
  })), [users]);
  const groupOptions = useMemo(() => groups.map((group) => ({
    id: String(group.id),
    label: `${group.name} (${group.platform})`,
  })), [groups]);
  const selectedGroupLabel = groupOptions.find((item) => item.id === String(form.group_id))?.label;
  const filteredUserOptions = useMemo(() => {
    const keyword = userKeyword.trim().toLowerCase();
    if (!keyword) return userOptions;
    return userOptions.filter((item) =>
      item.matchText.includes(keyword),
    );
  }, [userKeyword, userOptions]);
  const handleUserSelectionChange = (key: string | number | null) => {
    const value = key == null ? '' : String(key);
    if (!value) {
      setForm((current) => ({ ...current, user_id: 0 }));
      setSelectedUserLabel('');
      setUserKeyword('');
      return;
    }
    const option = userOptions.find((item) => item.id === value);
    setForm((current) => ({
      ...current,
      user_id: option ? Number(option.id) : 0,
    }));
    if (option) {
      setSelectedUserLabel(option.label);
      setUserKeyword(option.label);
    }
  };
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
          <Modal.Dialog className="ag-elevation-modal">
            <Modal.Header>
              <Modal.Heading>{t('subscriptions.assign')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="space-y-4">
                <ComboBox
                  allowsEmptyCollection
                  fullWidth
                  inputValue={userKeyword}
                  isRequired
                  items={filteredUserOptions}
                  menuTrigger="focus"
                  selectedKey={form.user_id ? String(form.user_id) : null}
                  onInputChange={handleUserInputChange}
                  onBlur={() => {
                    if (!form.user_id) setUserKeyword('');
                  }}
                  onSelectionChange={handleUserSelectionChange}
                >
                  <Label>{t('subscriptions.user')}</Label>
                  <ComboBox.InputGroup className="relative">
                    <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                    <Input className="pl-9 pr-10" placeholder={t('users.search_placeholder')} />
                    <ComboBox.Trigger
                      aria-label={t('subscriptions.select_user')}
                      className="ag-combobox-preview-trigger absolute right-1 top-1/2 z-10 h-7 w-7 min-w-0 -translate-y-1/2 p-0 text-text-tertiary hover:text-text"
                    />
                  </ComboBox.InputGroup>
                  <ComboBox.Popover>
                    <ListBox
                      items={filteredUserOptions}
                      renderEmptyState={() => (
                        <div className="px-3 py-6 text-center text-xs text-text-tertiary">
                          {userKeyword.trim() ? t('common.no_data') : t('users.search_placeholder')}
                        </div>
                      )}
                    >
                      {(item) => (
                        <ListBox.Item id={item.id} textValue={`${item.label} ${item.description}`}>
                          <div className="min-w-0">
                            <div className="truncate text-sm text-text">{item.label}</div>
                            <div className="truncate text-xs text-text-tertiary">{item.description}</div>
                          </div>
                        </ListBox.Item>
                      )}
                    </ListBox>
                  </ComboBox.Popover>
                </ComboBox>

                <Select
                  fullWidth
                  isRequired
                  selectedKey={form.group_id ? String(form.group_id) : null}
                  onSelectionChange={(key) => setForm({ ...form, group_id: key == null ? 0 : Number(key) })}
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
