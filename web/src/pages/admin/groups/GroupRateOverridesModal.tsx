import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Button, ComboBox, Input, ListBox, Modal, Spinner, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { Check, Plus, Trash2, X } from 'lucide-react';
import { PlatformIcon } from '../../../shared/ui';
import { groupsApi } from '../../../shared/api/groups';
import { usersApi } from '../../../shared/api/users';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import type { GroupResp, GroupRateOverrideResp, UserResp } from '../../../shared/types';

interface GroupRateOverridesModalProps {
  open: boolean;
  group: GroupResp;
  onClose: () => void;
}

export function GroupRateOverridesModal({ open, group, onClose }: GroupRateOverridesModalProps) {
  const { t } = useTranslation();
  const [emailQuery, setEmailQuery] = useState('');
  const [pickedUser, setPickedUser] = useState<UserResp | null>(null);
  const [newRate, setNewRate] = useState('1');
  const [editingUserId, setEditingUserId] = useState<number | null>(null);
  const [editingRate, setEditingRate] = useState('');

  const overridesKey = ['group-rate-overrides', group.id] as const;
  const { data: overrides = [], isLoading } = useQuery({
    queryKey: overridesKey,
    queryFn: () => groupsApi.listRateOverrides(group.id),
    enabled: open,
  });

  const { data: searchData } = useQuery({
    queryKey: ['users-search', emailQuery],
    queryFn: () => usersApi.list({ page: 1, page_size: 10, keyword: emailQuery }),
    enabled: open && emailQuery.trim().length > 0 && !pickedUser,
  });

  const setMutation = useCrudMutation({
    mutationFn: (payload: { userId: number; rate: number }) =>
      groupsApi.setRateOverride(group.id, payload.userId, payload.rate),
    successMessage: t('groups.rate_override_set_success'),
    queryKey: overridesKey,
    onSuccess: () => {
      setEmailQuery('');
      setPickedUser(null);
      setNewRate('1');
      setEditingUserId(null);
    },
  });

  const deleteMutation = useCrudMutation({
    mutationFn: (userId: number) => groupsApi.deleteRateOverride(group.id, userId),
    successMessage: t('groups.rate_override_delete_success'),
    queryKey: overridesKey,
  });

  const existingUserIds = useMemo(
    () => new Set((overrides as GroupRateOverrideResp[]).map((row) => row.user_id)),
    [overrides],
  );
  const searchResults = (searchData?.list ?? []).filter((user) => !existingUserIds.has(user.id));
  const searchOptions = searchResults.map((user) => ({
    id: String(user.id),
    label: user.email,
    description: user.username,
  }));
  const newRateNum = Number(newRate);
  const canAdd = !!pickedUser && Number.isFinite(newRateNum) && newRateNum > 0;

  const handleAdd = () => {
    if (!canAdd || !pickedUser) return;
    setMutation.mutate({ userId: pickedUser.id, rate: newRateNum });
  };

  const commitEdit = (userId: number) => {
    const value = Number(editingRate);
    if (!Number.isFinite(value) || value <= 0) return;
    setMutation.mutate({ userId, rate: value });
  };
  const modalState = useOverlayState({
    isOpen: open,
    onOpenChange: (nextOpen) => {
      if (!nextOpen) onClose();
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
              <Modal.Heading>{t('groups.rate_override_title')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
      <div className="mb-4 flex items-center gap-3 rounded-lg border border-glass-border px-3 py-2.5 text-sm">
        <PlatformIcon platform={group.platform} className="h-4 w-4" />
        <span className="font-medium text-text">{group.name}</span>
        <span className="text-text-tertiary">|</span>
        <span className="text-text-tertiary">{group.platform}</span>
        <span className="text-text-tertiary">|</span>
        <span className="text-text-tertiary">
          {t('groups.default_rate')}: <span className="font-mono text-primary">{group.rate_multiplier}x</span>
        </span>
      </div>

      <div className="mb-4">
        <p className="mb-2 text-xs font-medium uppercaser text-text-secondary">
          {t('groups.rate_override_add')}
        </p>
        <div className="flex items-start gap-2">
          <div className="flex-1">
            <ComboBox
              allowsCustomValue
              fullWidth
              inputValue={pickedUser ? pickedUser.email : emailQuery}
              items={searchOptions}
              menuTrigger="focus"
              selectedKey={pickedUser ? String(pickedUser.id) : null}
              onInputChange={(value) => {
                setPickedUser(null);
                setEmailQuery(value);
              }}
              onSelectionChange={(key) => {
                const user = searchResults.find((item) => String(item.id) === String(key));
                setPickedUser(user ?? null);
                setEmailQuery('');
              }}
            >
              <ComboBox.InputGroup>
                <Input placeholder={t('groups.rate_override_search_placeholder') ?? ''} />
                <ComboBox.Trigger />
              </ComboBox.InputGroup>
              <ComboBox.Popover>
                <ListBox items={searchOptions}>
                  {(item) => (
                    <ListBox.Item id={item.id} textValue={item.label}>
                      <div className="min-w-0">
                        <div className="truncate">{item.label}</div>
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
          <HeroTextField className="w-24" fullWidth>
            <Input
              type="number"
              min="0"
              step="0.01"
              value={newRate}
              onChange={(e) => setNewRate(e.target.value)}
            />
          </HeroTextField>
          <Button
            variant="primary"
            isDisabled={!canAdd || (setMutation.isPending && !editingUserId)}
            onPress={handleAdd}
          >
            {setMutation.isPending && !editingUserId ? <Spinner size="sm" /> : <Plus className="h-3.5 w-3.5" />}
            {t('common.add')}
          </Button>
        </div>
      </div>

      <div>
        <p className="mb-2 text-xs font-medium uppercaser text-text-secondary">
          {t('groups.rate_override_list', { count: overrides.length })}
        </p>
        {isLoading ? (
          <p className="py-8 text-center text-sm text-text-tertiary">{t('common.loading')}</p>
        ) : overrides.length === 0 ? (
          <p className="py-8 text-center text-sm text-text-tertiary">{t('groups.rate_override_empty')}</p>
        ) : (
          <div className="overflow-hidden rounded-lg border border-glass-border">
            {(overrides as GroupRateOverrideResp[]).map((row, index) => {
              const isEditing = editingUserId === row.user_id;
              return (
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
                  {isEditing ? (
                    <>
                      <HeroTextField className="w-24" fullWidth>
                        <Input
                          type="number"
                          min="0"
                          step="0.01"
                          value={editingRate}
                          onChange={(e) => setEditingRate(e.target.value)}
                        />
                      </HeroTextField>
                      <Button
                        size="sm"
                        variant="ghost"
                        isDisabled={setMutation.isPending}
                        onPress={() => commitEdit(row.user_id)}
                      >
                        {setMutation.isPending ? <Spinner size="sm" /> : <Check className="h-3.5 w-3.5" />}
                        {t('common.save')}
                      </Button>
                      <Button size="sm" variant="ghost" onPress={() => setEditingUserId(null)}>
                        <X className="h-3.5 w-3.5" />
                        {t('common.cancel')}
                      </Button>
                    </>
                  ) : (
                    <>
                      <Button
                        size="sm"
                        variant="ghost"
                        onPress={() => {
                          setEditingUserId(row.user_id);
                          setEditingRate(String(row.rate));
                        }}
                      >
                        <span className="font-mono text-primary">{row.rate}x</span>
                      </Button>
                      <Button
                        isIconOnly
                        size="sm"
                        variant="ghost"
                        className="text-danger"
                        isDisabled={deleteMutation.isPending}
                        onPress={() => deleteMutation.mutate(row.user_id)}
                      >
                        {deleteMutation.isPending ? <Spinner size="sm" /> : <Trash2 className="h-3.5 w-3.5" />}
                      </Button>
                    </>
                  )}
                </div>
              );
            })}
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
