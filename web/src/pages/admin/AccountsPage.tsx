import { lazy, Suspense, type ComponentType } from 'react';
import { PageLoading } from '../../shared/components/PageLoading';
import { useDeferredActivation } from '../../shared/hooks/useDeferredActivation';

type AccountsPageContentModule = {
  default: ComponentType;
};

let accountsPageContentPromise: Promise<AccountsPageContentModule> | undefined;

function loadAccountsPageContent() {
  accountsPageContentPromise ??= import('./AccountsPageContent').then((module) => ({ default: module.default }));
  return accountsPageContentPromise;
}

const AccountsPageContent = lazy(loadAccountsPageContent);
const ACCOUNTS_PAGE_ACTIVATION_DELAY_MS = 180;

function AccountsPagePlaceholder() {
  return (
    <>
      <PageLoading />
      <div className="min-h-[320px]" aria-hidden="true" />
    </>
  );
}

export default function AccountsPage() {
  const active = useDeferredActivation(ACCOUNTS_PAGE_ACTIVATION_DELAY_MS);

  if (!active) {
    return <AccountsPagePlaceholder />;
  }

  return (
    <Suspense fallback={<AccountsPagePlaceholder />}>
      <AccountsPageContent />
    </Suspense>
  );
}
