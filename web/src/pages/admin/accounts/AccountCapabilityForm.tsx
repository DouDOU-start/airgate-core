import { Checkbox, Label } from '@heroui/react';
import { useTranslation } from 'react-i18next';

const WORKLOADS = ['chat', 'image'] as const;
const IMAGE_PROTOCOLS = ['images_api', 'responses_tool'] as const;

type Workload = typeof WORKLOADS[number];
type ImageProtocol = typeof IMAGE_PROTOCOLS[number];

type AccountExtra = Record<string, unknown>;

function readStringArray(extra: AccountExtra | undefined, key: string): string[] {
  const raw = extra?.[key];
  if (Array.isArray(raw)) {
    return raw.filter((item): item is string => typeof item === 'string');
  }
  if (typeof raw === 'string') {
    return raw
      .split(',')
      .map((item) => item.trim())
      .filter(Boolean);
  }
  return [];
}

function writeCapabilityArray(extra: AccountExtra | undefined, key: string, values: string[]): AccountExtra {
  const next = { ...(extra ?? {}) };
  if (values.length === 0) {
    delete next[key];
  } else {
    next[key] = values;
  }
  return next;
}

function toggleValue<T extends string>(values: T[], value: T, checked: boolean): T[] {
  if (checked) {
    return values.includes(value) ? values : [...values, value];
  }
  return values.filter((item) => item !== value);
}

export function AccountCapabilityForm({
  platform,
  extra,
  onChange,
}: {
  platform: string;
  extra: AccountExtra | undefined;
  onChange: (extra: AccountExtra) => void;
}) {
  const { t } = useTranslation();

  if (platform !== 'openai') return null;

  const workloads = readStringArray(extra, 'allowed_workloads') as Workload[];
  const selectedWorkloads = workloads.length > 0 ? workloads : [...WORKLOADS];
  const imageProtocols = readStringArray(extra, 'image_protocols') as ImageProtocol[];
  const selectedImageProtocols = imageProtocols.length > 0 ? imageProtocols : [...IMAGE_PROTOCOLS];
  const imageEnabled = selectedWorkloads.includes('image');

  const updateWorkloads = (nextValues: Workload[]) => {
    if (nextValues.length === 0) return;
    let nextExtra = writeCapabilityArray(extra, 'allowed_workloads', nextValues.length === WORKLOADS.length ? [] : nextValues);
    if (!nextValues.includes('image')) {
      nextExtra = writeCapabilityArray(nextExtra, 'image_protocols', []);
    }
    onChange(nextExtra);
  };

  const updateImageProtocols = (nextValues: ImageProtocol[]) => {
    if (nextValues.length === 0) return;
    onChange(writeCapabilityArray(extra, 'image_protocols', nextValues.length === IMAGE_PROTOCOLS.length ? [] : nextValues));
  };

  return (
    <div className="space-y-3 rounded-xl border border-border bg-surface-secondary/40 p-3">
      <div>
        <Label>{t('accounts.capabilities')}</Label>
      </div>

      <div className="grid gap-3 md:grid-cols-2">
        <div className="space-y-2">
          <p className="text-xs font-medium text-text-secondary">{t('accounts.allowed_workloads')}</p>
          {WORKLOADS.map((workload) => (
            <Checkbox
              key={workload}
              className="flex"
              isSelected={selectedWorkloads.includes(workload)}
              onChange={(checked) => updateWorkloads(toggleValue(selectedWorkloads, workload, checked))}
            >
              <Checkbox.Control>
                <Checkbox.Indicator />
              </Checkbox.Control>
              <Checkbox.Content>
                <span className="text-sm text-text">{t(`accounts.workload_${workload}`)}</span>
              </Checkbox.Content>
            </Checkbox>
          ))}
        </div>

        <div className={imageEnabled ? 'space-y-2' : 'space-y-2 opacity-50'}>
          <p className="text-xs font-medium text-text-secondary">{t('accounts.image_protocols')}</p>
          {IMAGE_PROTOCOLS.map((protocol) => (
            <Checkbox
              key={protocol}
              className="flex"
              isDisabled={!imageEnabled}
              isSelected={imageEnabled && selectedImageProtocols.includes(protocol)}
              onChange={(checked) => updateImageProtocols(toggleValue(selectedImageProtocols, protocol, checked))}
            >
              <Checkbox.Control>
                <Checkbox.Indicator />
              </Checkbox.Control>
              <Checkbox.Content>
                <span className="text-sm text-text">{t(`accounts.image_protocol_${protocol}`)}</span>
              </Checkbox.Content>
            </Checkbox>
          ))}
        </div>
      </div>
    </div>
  );
}
