import { Button } from '@heroui/react';
import { RefreshCw } from 'lucide-react';

interface RefreshButtonProps {
  ariaLabel: string;
  isRefreshing: boolean;
  onRefresh: () => unknown;
  size?: 'sm' | 'md' | 'lg';
}

// 列表页统一刷新按钮：请求期间保持尺寸不变，并用旋转反馈提示刷新进度。
export function RefreshButton({
  ariaLabel,
  isRefreshing,
  onRefresh,
  size = 'sm',
}: RefreshButtonProps) {
  return (
    <Button
      isIconOnly
      aria-busy={isRefreshing}
      aria-label={ariaLabel}
      isDisabled={isRefreshing}
      size={size}
      variant="ghost"
      onPress={() => onRefresh()}
    >
      <RefreshCw className={`h-4 w-4 ${isRefreshing ? 'animate-spin motion-reduce:animate-none' : ''}`} />
    </Button>
  );
}
