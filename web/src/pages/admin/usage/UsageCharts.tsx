import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import {
  ResponsiveContainer,
  Tooltip as RechartsTooltip,
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Legend,
} from 'recharts';
import { CostValue } from '../../../shared/components/CostValue';
import { TOKEN_TREND_LINE_ORDER, TOKEN_TREND_RATIO_KEYS, USAGE_TOKEN_COLORS } from '../../../shared/constants';
import { fmtNum, fmtTrendTime } from '../../../shared/utils/format';
import type { UsageTrendBucket } from '../../../shared/types';

export function UsageTokenTrendChart({
  data,
  lineLabels,
}: {
  data: UsageTrendBucket[];
  lineLabels: Record<string, string>;
}) {
  const { t } = useTranslation();
  const chartData = useMemo(() => {
    let cumulativeCache = 0;
    let cumulativeTotal = 0;

    return data.map((d) => {
      const cacheTokens = d.cache_creation + d.cache_read;
      const totalTokens = d.input_tokens + d.output_tokens + cacheTokens;
      cumulativeCache += cacheTokens;
      cumulativeTotal += totalTokens;
      const cacheRatio = totalTokens > 0
        ? Math.min(100, Math.max(0, (cacheTokens / totalTokens) * 100))
        : 0;
      const cacheCumulativeRatio = cumulativeTotal > 0
        ? Math.min(100, Math.max(0, (cumulativeCache / cumulativeTotal) * 100))
        : 0;

      return {
        time: fmtTrendTime(d.time),
        rawTime: d.time,
        input: d.input_tokens,
        output: d.output_tokens,
        cacheCreation: d.cache_creation,
        cacheRead: d.cache_read,
        cacheRatio,
        cacheCumulativeRatio,
        actualCost: d.actual_cost,
        standardCost: d.standard_cost,
      };
    });
  }, [data]);

  return (
    <ResponsiveContainer width="100%" height="100%" debounce={80} initialDimension={{ width: 800, height: 300 }}>
      <LineChart data={chartData} margin={{ top: 4, right: 4, left: -18, bottom: 0 }}>
        <CartesianGrid stroke="var(--ag-border-subtle)" vertical={false} />
        <XAxis
          dataKey="time"
          tick={{ fontSize: 11, fill: 'var(--ag-text-tertiary)' }}
          axisLine={false}
          tickLine={false}
        />
        <YAxis
          yAxisId="tokens"
          tick={{ fontSize: 11, fill: 'var(--ag-text-tertiary)' }}
          axisLine={false}
          tickLine={false}
          tickFormatter={(v: number) => fmtNum(v)}
        />
        <YAxis
          yAxisId="ratio"
          orientation="right"
          domain={[0, 100]}
          tick={{ fontSize: 11, fill: 'var(--ag-text-tertiary)' }}
          axisLine={false}
          tickLine={false}
          tickFormatter={(v: number) => `${Math.round(v)}%`}
          width={32}
        />
        <RechartsTooltip
          content={({ active, payload, label }) => {
            if (!active || !payload?.length) return null;
            const d = payload[0]?.payload;
            const orderedPayload = [...payload].sort((a, b) => {
              const aIndex = TOKEN_TREND_LINE_ORDER.indexOf(String(a.dataKey) as keyof typeof USAGE_TOKEN_COLORS);
              const bIndex = TOKEN_TREND_LINE_ORDER.indexOf(String(b.dataKey) as keyof typeof USAGE_TOKEN_COLORS);
              return (aIndex < 0 ? TOKEN_TREND_LINE_ORDER.length : aIndex) - (bIndex < 0 ? TOKEN_TREND_LINE_ORDER.length : bIndex);
            });
            return (
              <div className="rounded-lg border border-border bg-bg-elevated p-3 text-xs shadow-lg">
                <div className="font-semibold text-text mb-2">{d?.rawTime ?? label}</div>
                {orderedPayload.map((entry, i) => (
                  <div key={i} className="flex items-center gap-2 py-0.5">
                    <div className="w-2.5 h-2.5 rounded-sm" style={{ background: entry.color }} />
                    <span className="text-text-secondary">{lineLabels[String(entry.dataKey)] || String(entry.dataKey)}:</span>
                    <span className="font-mono text-text ml-auto">
                      {TOKEN_TREND_RATIO_KEYS.has(String(entry.dataKey) as keyof typeof USAGE_TOKEN_COLORS) ? `${Number(entry.value).toFixed(1)}%` : fmtNum(Number(entry.value))}
                    </span>
                  </div>
                ))}
                <div className="border-t border-border-subtle mt-2 pt-2 text-text-secondary">
                  {t('usage.cost_actual')}: <CostValue className="font-mono" value={d?.actualCost ?? 0} tone="actual" />
                  {' | '}
                  {t('usage.cost_standard')}: <CostValue className="font-mono" value={d?.standardCost ?? 0} tone="standard" />
                </div>
              </div>
            );
          }}
        />
        <Legend
          height={24}
          content={() => (
            <div className="flex flex-wrap items-center justify-center gap-x-4 gap-y-1 pt-1 text-[11px] text-text-tertiary">
              {TOKEN_TREND_LINE_ORDER.map((key) => (
                <span key={key} className="inline-flex items-center gap-1.5">
                  {TOKEN_TREND_RATIO_KEYS.has(key) ? (
                    <span className="h-0 w-4 border-t-2 border-dashed" style={{ borderColor: USAGE_TOKEN_COLORS[key] }} />
                  ) : (
                    <span className="h-2 w-2 rounded-full" style={{ background: USAGE_TOKEN_COLORS[key] }} />
                  )}
                  <span>{lineLabels[key]}</span>
                </span>
              ))}
            </div>
          )}
        />
        <Line yAxisId="tokens" type="monotone" dataKey="input" stroke={USAGE_TOKEN_COLORS.input} strokeWidth={2} dot={false} isAnimationActive={false} />
        <Line yAxisId="tokens" type="monotone" dataKey="output" stroke={USAGE_TOKEN_COLORS.output} strokeWidth={2} dot={false} isAnimationActive={false} />
        <Line yAxisId="tokens" type="monotone" dataKey="cacheCreation" stroke={USAGE_TOKEN_COLORS.cacheCreation} strokeWidth={2} dot={false} isAnimationActive={false} />
        <Line yAxisId="tokens" type="monotone" dataKey="cacheRead" stroke={USAGE_TOKEN_COLORS.cacheRead} strokeWidth={2} dot={false} isAnimationActive={false} />
        <Line yAxisId="ratio" type="monotone" dataKey="cacheRatio" stroke={USAGE_TOKEN_COLORS.cacheRatio} strokeWidth={2} strokeDasharray="5 5" dot={false} isAnimationActive={false} />
        <Line yAxisId="ratio" type="monotone" dataKey="cacheCumulativeRatio" stroke={USAGE_TOKEN_COLORS.cacheCumulativeRatio} strokeWidth={2} strokeDasharray="5 5" dot={false} isAnimationActive={false} />
      </LineChart>
    </ResponsiveContainer>
  );
}
