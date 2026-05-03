import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Button, Checkbox, Input, Modal, Spinner, TextField as HeroTextField, useOverlayState } from '@heroui/react';
import { usersApi } from '../../../shared/api/users';
import { groupsApi } from '../../../shared/api/groups';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import { queryKeys } from '../../../shared/queryKeys';
import { FETCH_ALL_PARAMS } from '../../../shared/constants';
import type { UserResp, GroupResp, UpdateUserReq } from '../../../shared/types';

interface UserGroupsModalProps {
  open: boolean;
  user: UserResp;
  onClose: () => void;
  onSaved: () => void;
}

function initialRateState(groupRates?: Record<number, number>): Record<number, string> {
  const out: Record<number, string> = {};
  if (!groupRates) return out;
  for (const [key, value] of Object.entries(groupRates)) {
    if (typeof value === 'number' && value > 0) {
      out[Number(key)] = String(value);
    }
  }
  return out;
}

export function UserGroupsModal({ open, user, onClose, onSaved }: UserGroupsModalProps) {
  const { t } = useTranslation();
  const [selectedIds, setSelectedIds] = useState<number[]>(user.allowed_group_ids ?? []);
  const [customRates, setCustomRates] = useState<Record<number, string>>(() => initialRateState(user.group_rates));

  const { data: groupsData, isLoading: groupsLoading } = useQuery({
    queryKey: queryKeys.groupsAll(),
    queryFn: () => groupsApi.list(FETCH_ALL_PARAMS),
    enabled: open,
  });

  const allGroups: GroupResp[] = groupsData?.list ?? [];
  const exclusiveGroups = allGroups.filter((group) => group.is_exclusive);
  const normalGroups = allGroups.filter((group) => !group.is_exclusive);

  const buildPayload = (): UpdateUserReq => {
    const group_rates: Record<number, number> = {};
    for (const [key, raw] of Object.entries(customRates)) {
      if (raw === '' || raw == null) continue;
      const value = Number(raw);
      if (!Number.isFinite(value) || value <= 0) continue;
      group_rates[Number(key)] = value;
    }
    return {
      allowed_group_ids: selectedIds,
      group_rates,
    };
  };

  const updateMutation = useCrudMutation({
    mutationFn: (_?: void) => usersApi.update(user.id, buildPayload()),
    successMessage: t('users.update_success'),
    queryKey: queryKeys.users(),
    onSuccess: () => onSaved(),
  });

  const hasInvalidRate = useMemo(() => {
    for (const raw of Object.values(customRates)) {
      if (raw === '' || raw == null) continue;
      const value = Number(raw);
      if (!Number.isFinite(value) || value < 0) return true;
    }
    return false;
  }, [customRates]);

  const toggleExclusiveGroup = (groupId: number, isSelected: boolean) => {
    setSelectedIds((current) =>
      isSelected
        ? [...new Set([...current, groupId])]
        : current.filter((value) => value !== groupId),
    );
  };

  const renderRateField = (group: GroupResp, enabled: boolean) => (
    <div className="ml-auto w-24 shrink-0">
      <HeroTextField fullWidth isDisabled={!enabled}>
        <div className="relative">
          <Input
            aria-label={`${group.name} ${t('groups.rate_multiplier')}`}
            className="pr-6"
            type="number"
            min="0"
            step="0.01"
            disabled={!enabled}
            value={customRates[group.id] ?? ''}
            placeholder={String(group.rate_multiplier ?? 1)}
            onChange={(e) => setCustomRates((prev) => ({ ...prev, [group.id]: e.target.value }))}
          />
          <span className="pointer-events-none absolute right-3 top-1/2 z-10 -translate-y-1/2 text-[10px] text-text-tertiary">×</span>
        </div>
      </HeroTextField>
    </div>
  );

  const renderGroupRow = (group: GroupResp, selected: boolean, locked: boolean) => (
    <div
      key={group.id}
      className="flex items-center gap-2.5 rounded-lg px-3 py-2 text-sm text-text-secondary"
    >
      <Checkbox
        isDisabled={locked}
        isSelected={selected}
        onChange={(nextSelected) => toggleExclusiveGroup(group.id, nextSelected)}
      >
        <span className="text-text">{group.name}</span>
      </Checkbox>
      <span className="text-[10px] text-text-tertiary">{group.platform}</span>
      {renderRateField(group, !group.is_exclusive || selected)}
    </div>
  );
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
            style={{ maxWidth: '540px', width: 'min(100%, calc(100vw - 2rem))' }}
          >
            <Modal.Header>
              <Modal.Heading>{`${t('users.groups')} - ${user.email}`}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              {groupsLoading ? (
                <p className="py-8 text-center text-sm text-text-tertiary">{t('common.loading')}</p>
              ) : allGroups.length === 0 ? (
                <p className="py-8 text-center text-sm text-text-tertiary">{t('common.no_data')}</p>
              ) : (
                <div className="max-h-[26rem] space-y-4 overflow-y-auto">
                  <p className="text-[11px] text-text-tertiary">{t('users.group_rate_hint')}</p>

                  {normalGroups.length > 0 ? (
                    <div>
                      <p className="mb-2 text-xs font-medium uppercaser text-text-tertiary">
                        {t('users.normal_groups')}
                      </p>
                      <div className="space-y-0.5">
                        {normalGroups.map((group) => renderGroupRow(group, true, true))}
                      </div>
                    </div>
                  ) : null}

                  {exclusiveGroups.length > 0 ? (
                    <div>
                      <p className="mb-2 text-xs font-medium uppercaser text-text-tertiary">
                        {t('users.exclusive_groups')}
                      </p>
                      <div className="space-y-0.5">
                        {exclusiveGroups.map((group) =>
                          renderGroupRow(group, selectedIds.includes(group.id), false),
                        )}
                      </div>
                    </div>
                  ) : null}
                </div>
              )}
            </Modal.Body>
            <Modal.Footer>
              <Button variant="secondary" onPress={onClose}>
                {t('common.cancel')}
              </Button>
              <Button
                variant="primary"
                isDisabled={hasInvalidRate || updateMutation.isPending}
                onPress={() => updateMutation.mutate()}
              >
                {updateMutation.isPending ? <Spinner size="sm" /> : null}
                {t('common.save')}
              </Button>
            </Modal.Footer>
          </Modal.Dialog>
        </Modal.Container>
      </Modal.Backdrop>
    </Modal>
  );
}
