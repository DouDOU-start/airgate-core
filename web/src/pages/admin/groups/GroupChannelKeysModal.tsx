import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { Button, ComboBox, Input, ListBox, Modal, Spinner, useOverlayState } from '@heroui/react';
import { DialogTriggerShim } from '../../../shared/components/DialogTriggerShim';
import { Plus, Search, Trash2 } from 'lucide-react';
import { groupsApi } from '../../../shared/api/groups';
import { channelsApi } from '../../../shared/api/channels';
import { useCrudMutation } from '../../../shared/hooks/useCrudMutation';
import { useDebouncedValue } from '../../../shared/hooks/useDebouncedValue';
import { queryKeys } from '../../../shared/queryKeys';
import { KeyStatusChip } from '../channels/keyShared';
import type { GroupResp, ChannelKeyResp } from '../../../shared/types';

interface GroupChannelKeysModalProps {
  open: boolean;
  group: GroupResp;
  onClose: () => void;
}

// 分组渠道 key 绑定管理：查看/新增/移除绑定到该分组的渠道 key，交互结构与专属分组用户管理弹窗一致。
export function GroupChannelKeysModal({ open, group, onClose }: GroupChannelKeysModalProps) {
  const { t } = useTranslation();
  const [searchQuery, setSearchQuery] = useState('');
  const [pickedKey, setPickedKey] = useState<ChannelKeyResp | null>(null);
  const debouncedSearchQuery = useDebouncedValue(searchQuery.trim(), 250);

  const boundKey = ['group-channel-keys', group.id] as const;
  const { data: boundData, isLoading } = useQuery({
    queryKey: boundKey,
    queryFn: () => channelsApi.listKeys({ group_id: group.id, page: 1, page_size: 200 }),
    enabled: open,
  });
  const boundKeys = boundData?.list ?? [];

  const { data: searchData } = useQuery({
    queryKey: queryKeys.channelKeys('group-channel-keys-search', debouncedSearchQuery),
    queryFn: () => channelsApi.listKeys({
      page: 1,
      page_size: 20,
      keyword: debouncedSearchQuery || undefined,
    }),
    enabled: open && !pickedKey,
  });

  const bindMutation = useCrudMutation({
    mutationFn: (keyId: number) => groupsApi.bindChannelKey(group.id, keyId),
    successMessage: t('groups.channel_keys_bind_success'),
    queryKey: boundKey,
    extraQueryKeys: [queryKeys.channelKeys()],
    onSuccess: () => {
      setSearchQuery('');
      setPickedKey(null);
    },
  });

  const unbindMutation = useCrudMutation({
    mutationFn: (keyId: number) => groupsApi.unbindChannelKey(group.id, keyId),
    successMessage: t('groups.channel_keys_unbind_success'),
    queryKey: boundKey,
    extraQueryKeys: [queryKeys.channelKeys()],
  });

  const existingKeyIds = useMemo(
    () => new Set(boundKeys.map((row) => row.id)),
    [boundKeys],
  );
  const searchResults = useMemo(
    () => (searchData?.list ?? []).filter((key) => !existingKeyIds.has(key.id)),
    [existingKeyIds, searchData?.list],
  );
  const searchOptions = useMemo(
    () => searchResults.map((key) => ({
      id: String(key.id),
      label: key.name || key.channel_name,
      description: key.name ? key.channel_name : undefined,
      textValue: `${key.channel_name} ${key.name}`,
    })),
    [searchResults],
  );
  const visibleSearchOptions = useMemo(() => {
    if (!pickedKey || searchOptions.some((option) => option.id === String(pickedKey.id))) {
      return searchOptions;
    }
    return [
      {
        id: String(pickedKey.id),
        label: pickedKey.name || pickedKey.channel_name,
        description: pickedKey.name ? pickedKey.channel_name : undefined,
        textValue: `${pickedKey.channel_name} ${pickedKey.name}`,
      },
      ...searchOptions,
    ];
  }, [pickedKey, searchOptions]);

  const handleAdd = () => {
    if (!pickedKey) return;
    bindMutation.mutate(pickedKey.id);
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
              <Modal.Heading>{t('groups.channel_keys_title')}</Modal.Heading>
              <Modal.CloseTrigger />
            </Modal.Header>
            <Modal.Body>
              <div className="mb-4 flex items-center gap-3 rounded-lg border border-glass-border px-3 py-2.5 text-sm">
                <span className="font-medium text-text">{group.name}</span>
                <span className="text-text-tertiary">|</span>
                <span className="text-text-tertiary">{t('groups.channel_keys_hint')}</span>
              </div>

              <div className="mb-4">
                <p className="mb-2 text-xs font-medium uppercase text-text-secondary">
                  {t('groups.channel_keys_add')}
                </p>
                <div className="flex items-start gap-2">
                  <div className="flex-1">
                    <ComboBox
                      aria-label={t('groups.channel_keys_search_placeholder')}
                      allowsEmptyCollection
                      fullWidth
                      inputValue={searchQuery}
                      items={visibleSearchOptions}
                      menuTrigger="focus"
                      selectedKey={pickedKey ? String(pickedKey.id) : null}
                      onInputChange={(value) => {
                        setSearchQuery(value);
                        if (pickedKey && value !== (pickedKey.name || pickedKey.channel_name)) {
                          setPickedKey(null);
                        }
                      }}
                      onSelectionChange={(key) => {
                        const value = key == null ? '' : String(key);
                        if (!value) {
                          setPickedKey(null);
                          setSearchQuery('');
                          return;
                        }
                        const found = searchResults.find((item) => String(item.id) === value)
                          ?? (pickedKey && String(pickedKey.id) === value ? pickedKey : null);
                        setPickedKey(found ?? null);
                        setSearchQuery(found ? (found.name || found.channel_name) : '');
                      }}
                    >
                      <ComboBox.InputGroup className="relative">
                        <Search className="pointer-events-none absolute left-3 top-1/2 z-10 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
                        <Input className="pl-9 pr-10" placeholder={t('groups.channel_keys_search_placeholder') ?? ''} />
                        <ComboBox.Trigger
                          className="ag-combobox-preview-trigger absolute right-1 top-1/2 z-10 h-7 w-7 min-w-0 -translate-y-1/2 p-0 text-text-tertiary hover:text-text"
                        />
                      </ComboBox.InputGroup>
                      <ComboBox.Popover>
                        <ListBox
                          items={visibleSearchOptions}
                          renderEmptyState={() => (
                            <div className="px-3 py-6 text-center text-xs text-text-tertiary">
                              {debouncedSearchQuery ? t('common.no_data') : t('groups.channel_keys_search_placeholder')}
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
                    isDisabled={!pickedKey || bindMutation.isPending}
                    onPress={handleAdd}
                  >
                    {bindMutation.isPending ? <Spinner size="sm" /> : <Plus className="h-3.5 w-3.5" />}
                    {t('common.add')}
                  </Button>
                </div>
              </div>

              <div>
                <p className="mb-2 text-xs font-medium uppercase text-text-secondary">
                  {t('groups.channel_keys_list', { count: boundKeys.length })}
                </p>
                {isLoading ? (
                  <p className="py-8 text-center text-sm text-text-tertiary">{t('common.loading')}</p>
                ) : boundKeys.length === 0 ? (
                  <p className="py-8 text-center text-sm text-text-tertiary">{t('groups.channel_keys_empty')}</p>
                ) : (
                  <div className="overflow-hidden rounded-lg border border-glass-border">
                    {boundKeys.map((row, index) => (
                      <div
                        key={row.id}
                        className={`flex items-center gap-3 px-3 py-2.5 text-sm ${index === 0 ? '' : 'border-t border-glass-border'}`}
                      >
                        <div className="min-w-0 flex-1">
                          <div className="truncate text-text">{row.name || row.channel_name}</div>
                          {row.name ? (
                            <div className="truncate text-[11px] text-text-tertiary">{row.channel_name}</div>
                          ) : null}
                        </div>
                        <KeyStatusChip status={row.status} errorMsg={row.error_msg} />
                        <Button
                          isIconOnly
                          size="sm"
                          variant="ghost"
                          className="text-danger"
                          isDisabled={unbindMutation.isPending}
                          onPress={() => unbindMutation.mutate(row.id)}
                        >
                          {unbindMutation.isPending && unbindMutation.variables === row.id
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
