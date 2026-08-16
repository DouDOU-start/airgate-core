import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Chip, EmptyState, Tooltip } from '@heroui/react';
import { BarChart3, Boxes, Pencil, Trash2 } from 'lucide-react';
import { CommonTable } from '../../../shared/components/CommonTable';
import { TableLoadingRow } from '../../../shared/components/TableLoadingRow';
import { NativeSwitch } from '../../../shared/components/NativeSwitch';
import { SortableHeader } from '../../../shared/components/SortableHeader';
import { formatDate, formatDateTime } from '../../../shared/utils/format';
import type { ChannelKeyResp, ChannelKeySortBy, SortOrder } from '../../../shared/types';
import type { HealthmonEntity } from '../../../shared/api/healthMonitor';
import {
  CredentialProtocolChips, effectiveKeyStatus, HealthStatusChip, KeyConfigurationSummary,
  KeyFinancialSummary, KeyRuntimeSummary, KeyStatusChip, TrafficHealthBadge,
} from './keyShared';

const COLUMN_COUNT = 9;

// 密钥视图：跨渠道平铺展示全部 key（每行一把），priority/weight/created_at 可点击表头排序。
// 与渠道视图共用类型、状态展示，并使用密钥视图专用的配置、运行、财务摘要（见 keyShared.tsx）。
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
  onRefreshUpstreamRate,
  refreshingUpstreamRateId,
  onToggleEnabled,
  togglingId,
  trafficHealthByKey,
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
  onRefreshUpstreamRate: (key: ChannelKeyResp) => void;
  refreshingUpstreamRateId: number | null;
  onToggleEnabled: (key: ChannelKeyResp, enabled: boolean) => void;
  togglingId: number | null;
  trafficHealthByKey?: Map<number, HealthmonEntity>;
}) {
  const { t } = useTranslation();

  function sortState(field: ChannelKeySortBy): SortOrder | null {
    return sortBy === field ? sortOrder : null;
  }

  return (
    <CommonTable
      ariaLabel={t('channels.view_keys')}
      className="ag-channel-keys-table"
      contentStyle={{ tableLayout: 'fixed' }}
      mobileLayout="cards"
      footer={footer}
      minWidth={1260}
    >
      <CommonTable.Header>
        <CommonTable.Column id="channel" style={{ width: 140 }}>{t('channels.channel_name')}</CommonTable.Column>
        <CommonTable.Column id="key" style={{ width: 200 }}>{t('channels.keys_label')}</CommonTable.Column>
        <CommonTable.Column id="priority" style={{ width: 65 }}>
          <SortableHeader
            active={sortState('priority')}
            label={t('channels.priority')}
            onClick={() => onSortChange('priority')}
          />
        </CommonTable.Column>
        <CommonTable.Column id="weight" style={{ width: 65 }}>
          <SortableHeader
            active={sortState('weight')}
            label={t('channels.weight')}
            onClick={() => onSortChange('weight')}
          />
        </CommonTable.Column>
        <CommonTable.Column id="configuration" style={{ width: 175 }}>
          <span className="ag-key-composite-heading">
            <span>{t('channels.models')}</span>
            <span aria-hidden="true">·</span>
            <span>{t('channels.cost_ratio')}</span>
          </span>
        </CommonTable.Column>
        <CommonTable.Column id="runtime" style={{ width: 170 }}>{t('channels.concurrency_rpm')}</CommonTable.Column>
        <CommonTable.Column id="metrics" style={{ width: 230 }}>
          <Tooltip>
            <Tooltip.Trigger className="inline-flex cursor-help">
              <span>{t('channels.stats_header')}</span>
            </Tooltip.Trigger>
            <Tooltip.Content className="max-w-sm">{t('channels.stats_hint')}</Tooltip.Content>
          </Tooltip>
        </CommonTable.Column>
        <CommonTable.Column id="created" style={{ width: 85 }}>
          <SortableHeader
            active={sortState('created_at')}
            label={t('channels.created_at')}
            onClick={() => onSortChange('created_at')}
          />
        </CommonTable.Column>
        <CommonTable.Column id="actions" style={{ width: 130 }}>{t('common.actions')}</CommonTable.Column>
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
          rows.map((key) => {
            const effectiveStatus = effectiveKeyStatus(key);
            return (
            <CommonTable.Row id={String(key.id)} key={key.id}>
              <CommonTable.Cell>
                <div className="ag-key-channel-cell">
                  <span className="truncate font-semibold text-text" title={key.channel_name}>{key.channel_name}</span>
                  <a
                    className="truncate font-mono text-[10px] text-text-tertiary hover:text-accent hover:underline"
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
                <div className="ag-key-identity-cell">
                  <div className="ag-key-identity-primary">
                    <span className="truncate font-semibold text-text" title={key.name}>
                      {key.name || t('channels.key_unnamed')}
                    </span>
                    <NativeSwitch
                      ariaLabel={t('channels.status_enabled')}
                      isDisabled={togglingId === key.id}
                      isSelected={key.status === 'enabled'}
                      onChange={(enabled) => onToggleEnabled(key, enabled)}
                    />
                  </div>
                  <div className="ag-key-identity-meta">
                    <CredentialProtocolChips channelKey={key} />
                    <span className="font-mono text-[10px] text-text-tertiary" title={t('channels.api_key')}>
                      {key.api_key_hint || '-'}
                    </span>
                    {effectiveStatus.status === 'disabled_auto' ? (
                      <KeyStatusChip errorMsg={effectiveStatus.errorMsg} status={effectiveStatus.status} />
                    ) : null}
                    {key.health_status && key.health_status !== 'healthy' ? (
                      <HealthStatusChip status={key.health_status} />
                    ) : null}
                    {(() => {
                      const th = trafficHealthByKey?.get(key.id);
                      if (!th) return null;
                      return (
                        <TrafficHealthBadge
                          idle={th.sample.idle}
                          lowSample={th.sample.low_sample}
                          successRate={th.success_rate}
                          errorRate={th.error_rate}
                        />
                      );
                    })()}
                  </div>
                  {key.tags.length > 0 ? (
                    <div className="ag-key-tags">
                      {key.tags.map((tag) => (
                        <Chip color="default" key={tag} size="sm" variant="soft">{tag}</Chip>
                      ))}
                    </div>
                  ) : null}
                </div>
              </CommonTable.Cell>
              <CommonTable.Cell>
                <span className="ag-key-scheduling-value">{key.priority}</span>
              </CommonTable.Cell>
              <CommonTable.Cell>
                <span className="ag-key-scheduling-value">{key.weight}</span>
              </CommonTable.Cell>
              <CommonTable.Cell>
                <KeyConfigurationSummary
                  channelKey={key}
                  refreshingUpstreamRate={refreshingUpstreamRateId === key.id}
                  onRefreshUpstreamRate={() => onRefreshUpstreamRate(key)}
                />
              </CommonTable.Cell>
              <CommonTable.Cell>
                <KeyRuntimeSummary channelKey={key} />
              </CommonTable.Cell>
              <CommonTable.Cell>
                <KeyFinancialSummary
                  channelKey={key}
                  refreshingBalance={refreshingBalanceId === key.id}
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
                  <Tooltip>
                    <Tooltip.Trigger>
                      <Button isIconOnly aria-label={t('channels.stats_action')} size="sm" variant="secondary" onPress={() => onStats(key)}>
                        <BarChart3 className="h-3.5 w-3.5" />
                      </Button>
                    </Tooltip.Trigger>
                    <Tooltip.Content>{t('channels.stats_action')}</Tooltip.Content>
                  </Tooltip>
                  <Tooltip>
                    <Tooltip.Trigger>
                      <Button isIconOnly aria-label={t('channels.models')} size="sm" variant="secondary" onPress={() => onOpenModels(key)}>
                        <Boxes className="h-3.5 w-3.5" />
                      </Button>
                    </Tooltip.Trigger>
                    <Tooltip.Content>{t('channels.models')}</Tooltip.Content>
                  </Tooltip>
                  <Tooltip>
                    <Tooltip.Trigger>
                      <Button isIconOnly aria-label={t('common.edit')} size="sm" variant="secondary" onPress={() => onEdit(key)}>
                        <Pencil className="h-3.5 w-3.5" />
                      </Button>
                    </Tooltip.Trigger>
                    <Tooltip.Content>{t('common.edit')}</Tooltip.Content>
                  </Tooltip>
                  <Tooltip>
                    <Tooltip.Trigger>
                      <Button isIconOnly aria-label={t('common.delete')} className="text-danger" size="sm" variant="danger-soft" onPress={() => onDelete(key)}>
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </Tooltip.Trigger>
                    <Tooltip.Content>{t('common.delete')}</Tooltip.Content>
                  </Tooltip>
                </div>
              </CommonTable.Cell>
            </CommonTable.Row>
            );
          })
        )}
      </CommonTable.Body>
    </CommonTable>
  );
}
