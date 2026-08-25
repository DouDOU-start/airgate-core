export function TableLoadingRow({
  colSpan,
  minHeight = 220,
}: {
  colSpan: number;
  minHeight?: number;
}) {
  return (
    <tr data-key="loading" data-slot="tr">
      <td colSpan={colSpan} data-slot="td">
        <div aria-busy="true" aria-live="polite" className="w-full" style={{ minHeight }}>
          <span className="sr-only">Loading</span>
        </div>
      </td>
    </tr>
  );
}
