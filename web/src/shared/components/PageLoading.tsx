import { ProgressCircle } from '@heroui/react';

function LoadingIndicator() {
  return (
    <ProgressCircle isIndeterminate aria-label="Loading">
      <ProgressCircle.Track>
        <ProgressCircle.TrackCircle />
        <ProgressCircle.FillCircle />
      </ProgressCircle.Track>
    </ProgressCircle>
  );
}

function LoadingBlock({ compact = false }: { compact?: boolean }) {
  return (
    <div
      className={
        compact
          ? 'flex h-full min-h-[240px] items-center justify-center'
          : 'flex min-h-[420px] items-center justify-center'
      }
    >
      <div className="flex flex-col items-center gap-3 rounded-[var(--radius)] px-6 py-5">
        <div className="relative flex h-12 w-12 items-center justify-center">
          <span className="absolute inset-0 rounded-full bg-primary/10 blur-md" />
          <div className="relative">
            <LoadingIndicator />
          </div>
        </div>
        <span className="text-xs text-text-tertiary">Loading</span>
      </div>
    </div>
  );
}

export function PageLoading() {
  return <LoadingBlock />;
}

export function FullPageLoading() {
  return (
    <div className="min-h-screen bg-bg text-text">
      <LoadingBlock />
    </div>
  );
}

export function ChatPageLoading() {
  return (
    <div className="h-full min-h-0 bg-bg text-text">
      <LoadingBlock compact />
    </div>
  );
}
