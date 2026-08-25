import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown, ChevronRight } from 'lucide-react';
import {
  renderAccountTypeFilterOption,
  type AccountTypeFilterOption,
} from './AccountPageSupport';

type AccountTypeFilterSelectProps = {
  oauthPlanOptions: AccountTypeFilterOption[];
  onSelect: (value: string) => void;
  platformsLoading: boolean;
  selectedOption: AccountTypeFilterOption | undefined;
  typeOptions: AccountTypeFilterOption[];
};

export function AccountTypeFilterSelect({
  oauthPlanOptions,
  onSelect,
  platformsLoading,
  selectedOption,
  typeOptions,
}: AccountTypeFilterSelectProps) {
  const { t } = useTranslation();
  const [isTypeMenuOpen, setIsTypeMenuOpen] = useState(false);
  const [isOAuthPlanMenuOpen, setIsOAuthPlanMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const selectedNode: ReactNode = selectedOption
    ? renderAccountTypeFilterOption(selectedOption)
    : t('accounts.all_types', '全部类型');

  const closeMenu = useCallback(() => {
    setIsTypeMenuOpen(false);
    setIsOAuthPlanMenuOpen(false);
  }, []);

  const selectTypeFilter = useCallback((nextValue: string) => {
    onSelect(nextValue);
    closeMenu();
  }, [closeMenu, onSelect]);

  useEffect(() => {
    if (!isTypeMenuOpen) return;

    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && menuRef.current?.contains(target)) return;
      closeMenu();
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeMenu();
    };

    document.addEventListener('pointerdown', handlePointerDown, true);
    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('pointerdown', handlePointerDown, true);
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [closeMenu, isTypeMenuOpen]);

  return (
    <div ref={menuRef} className="select select--full-width ag-account-type-select">
      <button
        type="button"
        aria-label={t('common.type')}
        aria-haspopup="menu"
        aria-expanded={isTypeMenuOpen}
        className="select__trigger select__trigger--full-width ag-account-type-trigger"
        onClick={() => {
          setIsTypeMenuOpen((open) => {
            if (open) setIsOAuthPlanMenuOpen(false);
            return !open;
          });
        }}
      >
        <span className="select__value ag-account-type-trigger-value">{selectedNode}</span>
        <ChevronDown
          className="select__indicator ag-account-type-trigger-indicator"
          data-open={isTypeMenuOpen ? 'true' : undefined}
        />
      </button>
      {isTypeMenuOpen ? (
        <div className="select__popover ag-account-type-menu" role="menu">
          <button
            type="button"
            role="menuitem"
            className="ag-account-type-menu-item"
            onPointerEnter={() => setIsOAuthPlanMenuOpen(false)}
            onFocus={() => setIsOAuthPlanMenuOpen(false)}
            onClick={() => selectTypeFilter('')}
          >
            {typeOptions[0]?.label ?? t('accounts.all_types', '全部类型')}
          </button>
          <div
            className="ag-account-type-cascade-row"
            onPointerEnter={() => setIsOAuthPlanMenuOpen(true)}
            onPointerLeave={() => setIsOAuthPlanMenuOpen(false)}
          >
            <button
              type="button"
              role="menuitem"
              className="ag-account-type-menu-item"
              onFocus={() => setIsOAuthPlanMenuOpen(true)}
              onClick={() => selectTypeFilter('oauth')}
            >
              <span className="truncate">OAuth</span>
              <ChevronRight className="h-3.5 w-3.5 shrink-0 text-text-tertiary" />
            </button>
            {isOAuthPlanMenuOpen ? (
              <>
                <span aria-hidden="true" className="ag-account-type-submenu-bridge" />
                <div className="ag-account-type-submenu" role="menu">
                  {oauthPlanOptions.length > 0 ? (
                    oauthPlanOptions.map((plan) => (
                      <button
                        key={plan.id}
                        type="button"
                        role="menuitem"
                        className="ag-account-type-submenu-item"
                        onClick={() => selectTypeFilter(plan.id)}
                      >
                        {renderAccountTypeFilterOption(plan, false)}
                      </button>
                    ))
                  ) : platformsLoading ? (
                    <span className="ag-account-type-submenu-loading">{t('common.loading')}</span>
                  ) : (
                    <span className="ag-account-type-submenu-loading">{t('accounts.no_oauth_plans', '暂无套餐')}</span>
                  )}
                </div>
              </>
            ) : null}
          </div>
          <button
            type="button"
            role="menuitem"
            className="ag-account-type-menu-item"
            onPointerEnter={() => setIsOAuthPlanMenuOpen(false)}
            onFocus={() => setIsOAuthPlanMenuOpen(false)}
            onClick={() => selectTypeFilter('apikey')}
          >
            API Key
          </button>
        </div>
      ) : null}
    </div>
  );
}
