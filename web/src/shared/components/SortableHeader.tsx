import { ChevronDown, ChevronsUpDown, ChevronUp } from 'lucide-react';
import type { SortOrder } from '../types';

/** 可排序表头：当前排序字段高亮箭头方向，非当前字段显示中性上下箭头；点击切换/翻转排序。 */
export function SortableHeader({
  active, label, onClick,
}: {
  active: SortOrder | null;
  label: string;
  onClick: () => void;
}) {
  const Icon = active === 'asc' ? ChevronUp : active === 'desc' ? ChevronDown : ChevronsUpDown;
  return (
    <button
      className="inline-flex items-center gap-1 text-inherit"
      type="button"
      onClick={onClick}
    >
      {label}
      <Icon className={`h-3.5 w-3.5 ${active ? 'text-text' : 'text-text-tertiary'}`} />
    </button>
  );
}
