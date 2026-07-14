import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Chip, EmptyState } from '@heroui/react';
import { BarChart3, Boxes, Pencil, Trash2 } from 'lucide-react';
import { CommonTable } from '../../../shared/components/CommonTable';
import { TableLoadingRow } from '../../../shared/components/TableLoadingRow';
import { NativeSwitch } from '../../../shared/components/NativeSwitch';
import { SortableHeader } from '../../../shared/components/SortableHeader';
import { formatDate, formatDateTime } from '../../../shared/utils/format';
import type { ChannelKeyResp, ChannelKeySortBy, SortOrder } from '../../../shared/types';
import {
  KeyMetricsRow, KeyStatusChip, TYPE_CHIP_COLORS, typeLabel,
} from './keyShared';

const COLUMN_COUNT = 7;

// 密钥视图：跨渠道平铺展示全部 key（每行一把），priority/weight/created_at 可点击表头排序。
// 与渠道视图（ChannelsPage 内的 KeyRow）共用类型徽章/状态徽章/指标行渲染逻辑（见 keyShared.tsx）。
export function ChannelKeysTable({
  rows,
  isLoading,
  footer,
  sortBy,
  sortOrder,
  onSortChange,
  onOpenModels,
  onEdit,
  onDelete,
  onStats,
  onRefreshBalance,
  refreshingBalanceId,
  onToggleEnabled,
  togglingId,
}: {
  rows: ChannelKeyResp[];
  isLoading: boolean;
  footer: ReactNode;
  sortBy?: ChannelKeySortBy;
  sortOrder: SortOrder;
  onSortChange: (field: ChannelKeySortBy) => void;
  onOpenModels: (key: ChannelKeyResp) => void;
  onEdit: (key: ChannelKeyResp) => void;
  onDelete: (key: ChannelKeyResp) => void;
  onStats: (key: ChannelKeyResp) => void;
  onRefreshBalance: (key: ChannelKeyResp) => void;
  refreshingBalanceId: number | null;
  onToggleEnabled: (key: ChannelKeyResp, enabled: boolean) => void;
  togglingId: number | null;
}) {
  const { t } = useTranslation();

  function sortState(field: ChannelKeySortBy): SortOrder | null {
    return sortBy === field ? sortOrder : null;
  }

  return (
    <CommonTable
      ariaLabel={t('channels.view_keys')}
      className="ag-channel-keys-table"
      footer={footer}
      minWidth={1200}
    >
      <CommonTable.Header>
        <CommonTable.Column id="channel">{t('channels.channel_name')}</CommonTable.Column>
        <CommonTable.Column id="key">{t('channels.keys_label')}</CommonTable.Column>
        <CommonTable.Column id="priority" style={{ width: 100 }}>
          <SortableHeader
            active={sortState('priority')}
            label={t('channels.priority')}
            onClick={() => onSortChange('priority')}
          />
        </CommonTable.Column>
        <CommonTable.Column id="weight" style={{ width: 100 }}>
          <SortableHeader
            active={sortState('weight')}
            label={t('channels.weight')}
            onClick={() => onSortChange('weight')}
          />
        </CommonTable.Column>
        <CommonTable.Column id="metrics">{t('channels.stats_header')}</CommonTable.Column>
        <CommonTable.Column id="created" style={{ width: 130 }}>
          <SortableHeader
            active={sortState('created_at')}
            label={t('channels.created_at')}
            onClick={() => onSortChange('created_at')}
          />
        </CommonTable.Column>
        <CommonTable.Column id="actions">{t('common.actions')}</CommonTable.Column>
      </CommonTable.Header>
      <CommonTable.Body>
        {isLoading ? (
          <TableLoadingRow colSpan={COLUMN_COUNT} />
        ) : rows.length === 0 ? (
          <CommonTable.Row id="empty">
            <CommonTable.Cell colSpan={COLUMN_COUNT}>
              <EmptyState>
                <div className="text-sm text-default-500">{t('common.no_data')}</div>
              </EmptyState>
            </CommonTable.Cell>
          </CommonTable.Row>
        ) : (
          rows.map((key) => (
            <CommonTable.Row id={String(key.id)} key={key.id}>
              <CommonTable.Cell>
                <div className="flex min-w-0 flex-col">
                  <span className="truncate font-medium text-text" title={key.channel_name}>{key.channel_name}</span>
                  <a
                    className="truncate font-mono text-[11px] text-text-tertiary hover:text-accent hover:underline"
                    href={key.base_url}
                    rel="noopener noreferrer"
                    target="_blank"
                    title={key.base_url}
                  >
                    {key.base_url}
                  </a>
                </div>
              </CommonTable.Cell>
              <CommonTable.Cell>
                <div className="flex flex-wrap items-center gap-2">
                  <span className="max-w-[160px] truncate font-medium text-text" title={key.name}>
                    {key.name || t('channels.key_unnamed')}
                  </span>
                  <Chip color={TYPE_CHIP_COLORS[key.type] ?? 'default'} size="sm" variant="soft">
                    {typeLabel(key.type)}
                  </Chip>
                  <NativeSwitch
                    ariaLabel={t('channels.status_enabled')}
                    isDisabled={togglingId === key.id}
                    isSelected={key.status === 'enabled'}
                    onChange={(enabled) => onToggleEnabled(key, enabled)}
                  />
                  {key.status === 'disabled_auto' ? (
                    <KeyStatusChip errorMsg={key.error_msg} status={key.status} />
                  ) : null}
                  <span className="font-mono text-[11px] text-text-tertiary" title={t('channels.api_key')}>
                    {key.api_key_hint || '-'}
                  </span>
                </div>
              </CommonTable.Cell>
              <CommonTable.Cell>
                <span className="font-mono text-text">{key.priority}</span>
              </CommonTable.Cell>
              <CommonTable.Cell>
                <span className="font-mono text-text">{key.weight}</span>
              </CommonTable.Cell>
              <CommonTable.Cell>
                <KeyMetricsRow
                  channelKey={key}
                  refreshingBalance={refreshingBalanceId === key.id}
                  showPriorityWeight={false}
                  onRefreshBalance={() => onRefreshBalance(key)}
                />
              </CommonTable.Cell>
              <CommonTable.Cell>
                <span className="text-xs text-text-secondary" title={formatDateTime(key.created_at)}>
                  {formatDate(key.created_at)}
                </span>
              </CommonTable.Cell>
              <CommonTable.Cell>
                <div className="ag-table-row-actions flex justify-center gap-1">
                  <Button size="sm" variant="secondary" onPress={() => onStats(key)}>
                    <BarChart3 className="h-3.5 w-3.5" />
                    {t('channels.stats_action')}
                  </Button>
                  <Button size="sm" variant="secondary" onPress={() => onOpenModels(key)}>
                    <Boxes className="h-3.5 w-3.5" />
                    {t('channels.models')}
                  </Button>
                  <Button size="sm" variant="secondary" onPress={() => onEdit(key)}>
                    <Pencil className="h-3.5 w-3.5" />
                    {t('common.edit')}
                  </Button>
                  <Button className="text-danger" size="sm" variant="danger-soft" onPress={() => onDelete(key)}>
                    <Trash2 className="h-3.5 w-3.5" />
                    {t('common.delete')}
                  </Button>
                </div>
              </CommonTable.Cell>
            </CommonTable.Row>
          ))
        )}
      </CommonTable.Body>
    </CommonTable>
  );
}
