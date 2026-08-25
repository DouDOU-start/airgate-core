import { lazy, Suspense, type ComponentType } from 'react';
import { useDeferredActivation } from '../../shared/hooks/useDeferredActivation';
import { PageLoading } from '../../shared/components/PageLoading';

type UserUsageContentModule = {
  default: ComponentType;
};

let userUsageContentPromise: Promise<UserUsageContentModule> | undefined;

function loadUserUsageContent() {
  userUsageContentPromise ??= import('./UserUsageContent').then((module) => ({ default: module.default }));
  return userUsageContentPromise;
}

export function preloadUserUsageContent() {
  return loadUserUsageContent();
}

const UserUsageContent = lazy(loadUserUsageContent);
const USER_USAGE_PAGE_ACTIVATION_DELAY_MS = 180;

function UserUsagePagePlaceholder() {
  return (
    <>
      <PageLoading />
      <div className="min-h-[320px]" aria-hidden="true" />
    </>
  );
}

export default function UserUsagePage() {
  const active = useDeferredActivation(USER_USAGE_PAGE_ACTIVATION_DELAY_MS);

  if (!active) {
    return <UserUsagePagePlaceholder />;
  }

  return (
    <Suspense fallback={<UserUsagePagePlaceholder />}>
      <UserUsageContent />
    </Suspense>
  );
}
