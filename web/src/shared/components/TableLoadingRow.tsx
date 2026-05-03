import { ProgressCircle, Table as HeroTable } from '@heroui/react';

export function TableLoadingRow({
  colSpan,
  minHeight = 220,
}: {
  colSpan: number;
  minHeight?: number;
}) {
  return (
    <HeroTable.Row id="loading">
      <HeroTable.Cell colSpan={colSpan}>
        <div className="flex w-full items-center justify-center" style={{ minHeight }}>
          <div className="flex flex-col items-center gap-3 rounded-[var(--radius)] px-6 py-5">
            <div className="relative flex h-12 w-12 items-center justify-center">
              <span className="absolute inset-0 rounded-full bg-primary/10 blur-md" />
              <div className="relative">
                <ProgressCircle isIndeterminate aria-label="Loading">
                  <ProgressCircle.Track>
                    <ProgressCircle.TrackCircle />
                    <ProgressCircle.FillCircle />
                  </ProgressCircle.Track>
                </ProgressCircle>
              </div>
            </div>
            <span className="text-xs text-text-tertiary">Loading</span>
          </div>
        </div>
      </HeroTable.Cell>
    </HeroTable.Row>
  );
}
