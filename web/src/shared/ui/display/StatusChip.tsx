import { Chip } from '@heroui/react';
import { useTranslation } from 'react-i18next';
import type { ComponentProps } from 'react';

type ChipColor = ComponentProps<typeof Chip>['color'];

const statusMap: Record<string, { color: ChipColor; label: string }> = {
  active: { color: 'success', label: 'status.active' },
  disabled: { color: 'default', label: 'status.disabled' },
  enabled: { color: 'success', label: 'status.enabled' },
  error: { color: 'danger', label: 'status.error' },
  expired: { color: 'warning', label: 'status.expired' },
  failed: { color: 'danger', label: 'status.failed' },
  installed: { color: 'accent', label: 'status.installed' },
  paid: { color: 'success', label: 'status.paid' },
  pending: { color: 'accent', label: 'status.pending' },
  suspended: { color: 'warning', label: 'status.suspended' },
};

export interface StatusChipProps {
  status: string;
}

export function StatusChip({ status }: StatusChipProps) {
  const { t } = useTranslation();
  const config = statusMap[status] ?? { color: 'default' as const, label: status };

  return (
    <Chip color={config.color} size="sm" variant="soft">
      {t(config.label)}
    </Chip>
  );
}
