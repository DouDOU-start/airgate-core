import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useToast } from '../ui';

interface CrudMutationOptions<TData, TVariables = void> {
  mutationFn: (variables: TVariables) => Promise<TData>;
  /** 成功提示消息，省略则不显示 toast */
  successMessage?: string;
  /** invalidateQueries 的 queryKey */
  queryKey: readonly unknown[];
  /** 成功后的额外回调（如关闭弹窗） */
  onSuccess?: (data: TData) => void;
}

export function useCrudMutation<TData, TVariables>(
  opts: CrudMutationOptions<TData, TVariables>,
) {
  const { toast } = useToast();
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: opts.mutationFn,
    onSuccess: (data) => {
      if (opts.successMessage) toast('success', opts.successMessage);
      queryClient.invalidateQueries({ queryKey: opts.queryKey });
      opts.onSuccess?.(data);
    },
    onError: (err: Error) => toast('error', err.message),
  });
}
