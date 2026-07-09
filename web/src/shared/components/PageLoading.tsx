interface TopLoadingLineProps {
  active?: boolean;
}

export function TopLoadingLine({ active = true }: TopLoadingLineProps) {
  if (!active) return null;

  return (
    <div className="ag-global-loading-line" role="progressbar" aria-label="Loading">
      <span />
    </div>
  );
}

export function PageLoading() {
  return <TopLoadingLine />;
}

export function FullPageLoading() {
  return (
    <div className="min-h-screen bg-bg text-text">
      <TopLoadingLine />
    </div>
  );
}
