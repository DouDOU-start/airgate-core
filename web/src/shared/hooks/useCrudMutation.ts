import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useToast } from '../ui';

interface CrudMutationOptions<TData, TVariables = void> {
  mutationFn: (variables: TVariables) => Promise<TData>;
  /** 成功提示消息，省略则不显示 toast */
  successMessage?: string;
  /** invalidateQueries 的 queryKey */
  queryKey: readonly unknown[];
  /** 同一数据的其他视图缓存（如按不同维度平铺查询的独立 queryKey），一并失效 */
  extraQueryKeys?: readonly (readonly unknown[])[];
  /** 成功后的额外回调（如关闭弹窗） */
  onSuccess?: (data: TData, variables: TVariables) => void;
}

export function useCrudMutation<TData, TVariables>(
  opts: CrudMutationOptions<TData, TVariables>,
) {
  const { toast } = useToast();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: opts.mutationFn,
    onSuccess: (data, variables) => {
      if (opts.successMessage) toast('success', opts.successMessage);
      queryClient.invalidateQueries({ queryKey: opts.queryKey });
      opts.extraQueryKeys?.forEach((key) => queryClient.invalidateQueries({ queryKey: key }));
      opts.onSuccess?.(data, variables);
    },
    onError: (err: Error) => toast('error', err.message),
  });
}
