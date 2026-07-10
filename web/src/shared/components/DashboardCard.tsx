import type { ReactNode } from 'react';
import { Card } from '@heroui/react';

/**
 * 仪表盘面板卡：标题 + 可选右侧操作区（extra）。
 * title 与 extra 均可省略；两者都缺省时不渲染头部。
 */
export function DashboardCard({
  children,
  extra,
  title,
}: {
  children: ReactNode;
  extra?: ReactNode;
  title?: string;
}) {
  const hasHeader = Boolean(title || extra);

  return (
    <Card className="ag-dashboard-panel">
      {hasHeader ? (
        <div
          className={`flex min-w-0 items-center gap-3 border-b border-separator p-3 pb-2.5 2xl:p-4 2xl:pb-3 ${title ? 'justify-between' : 'justify-end'}`}
        >
          {title ? <h3 className="min-w-0 truncate text-base leading-none text-text">{title}</h3> : null}
          {extra ? (
            <div className="min-w-0 shrink">{extra}</div>
          ) : null}
        </div>
      ) : null}
      <Card.Content className={hasHeader ? 'p-3 2xl:p-4' : 'p-3 2xl:p-4'}>{children}</Card.Content>
    </Card>
  );
}
