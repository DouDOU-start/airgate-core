import { useCallback, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { accountsApi } from '../../../shared/api/accounts';
import { queryKeys } from '../../../shared/queryKeys';
import type { AccountResp, PagedData } from '../../../shared/types';
import { accountSupportsUsageRefresh } from './AccountUsageCell';

/** 后台用量刷新限制并发，避免页面加载时同时压向所有上游。 */
const BACKGROUND_USAGE_REFRESH_CONCURRENCY = 3;

/**
 * 静默刷新账号用量：限制并发、过滤重复请求，并就地更新列表缓存。
 * 页面主刷新状态不等待这些请求，账号单元格可通过 refreshingIds 展示局部进度。
 */
export function useBackgroundAccountUsageRefresh() {
  const queryClient = useQueryClient();
  const inFlightRef = useRef(new Set<number>());
  const [refreshingIds, setRefreshingIds] = useState<Set<number>>(() => new Set());

  const refresh = useCallback((accounts: AccountResp[]) => {
    const ids = [...new Set(
      accounts
        .filter((account) => accountSupportsUsageRefresh(account.platform, account.type))
        .map((account) => account.id)
        .filter((id) => !inFlightRef.current.has(id)),
    )];
    if (ids.length === 0) return;

    for (const id of ids) inFlightRef.current.add(id);
    setRefreshingIds((current) => new Set([...current, ...ids]));

    let cursor = 0;
    const worker = async () => {
      while (cursor < ids.length) {
        const id = ids[cursor];
        cursor += 1;
        if (id === undefined) break;
        try {
          const updated = await accountsApi.refreshUsage(id);
          // 只合并用量相关字段，保留列表接口提供的运行时指标、金额和分组信息。
          queryClient.setQueriesData<PagedData<AccountResp>>(
            { queryKey: queryKeys.accounts() },
            (current) => {
              if (!current?.list.some((account) => account.id === id)) return current;
              return {
                ...current,
                list: current.list.map((account) => (
                  account.id === id
                    ? {
                      ...account,
                      usage: updated.usage,
                      plan_type: updated.plan_type || account.plan_type,
                      subscription_active_until:
                        updated.subscription_active_until || account.subscription_active_until,
                    }
                    : account
                )),
              };
            },
          );
        } catch {
          // 单个账号刷新失败时保留旧快照；后台任务不弹提示、不阻塞其它账号。
        } finally {
          inFlightRef.current.delete(id);
          setRefreshingIds((current) => {
            if (!current.has(id)) return current;
            const next = new Set(current);
            next.delete(id);
            return next;
          });
        }
      }
    };

    const workerCount = Math.min(BACKGROUND_USAGE_REFRESH_CONCURRENCY, ids.length);
    void Promise.all(Array.from({ length: workerCount }, () => worker()));
  }, [queryClient]);

  return { refresh, refreshingIds };
}
