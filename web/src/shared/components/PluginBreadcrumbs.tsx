import { Link } from '@tanstack/react-router';
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

export interface PluginBreadcrumbItem {
  to?: string;
  href?: string;
  labelKey?: string;
  labelFallback: string;
}

export interface PluginBreadcrumbsProps {
  items: PluginBreadcrumbItem[];
  pluginName?: string;
  ariaLabel?: string;
  className?: string;
}

function Separator() {
  return (
    <span aria-hidden="true" className="shrink-0 px-0.5 text-text-tertiary opacity-45">
      /
    </span>
  );
}

function pluginShortName(pluginName: string) {
  return pluginName
    .replace(/^airgate-/, '')
    .replace(/插件工作台$/u, '')
    .replace(/\s+(plugin\s+)?workbench$/iu, '');
}

const WORKBENCH_COMPACT_WIDTH = 384;

export function PluginBreadcrumbs({
  items,
  pluginName,
  ariaLabel,
  className,
}: PluginBreadcrumbsProps) {
  const { t } = useTranslation();
  const navRef = useRef<HTMLElement | null>(null);
  const [isCompact, setIsCompact] = useState(false);
  const rootClassName = `ag-plugin-breadcrumbs flex min-w-0 flex-1 items-center gap-1.5 overflow-hidden whitespace-nowrap text-sm ${className ?? ''}`;
  const shortPluginName = pluginName ? pluginShortName(pluginName) : undefined;
  const displayPluginName = shortPluginName
    ? t('plugin_shell.plugin_workbench', {
        name: shortPluginName,
        defaultValue: `${shortPluginName} plugin workbench`,
      })
    : undefined;
  const showWorkbench = Boolean(displayPluginName && (!isCompact || items.length === 0));

  useEffect(() => {
    const element = navRef.current;
    if (!element || typeof ResizeObserver === 'undefined') return;

    const updateCompact = (width: number) => {
      setIsCompact(width < WORKBENCH_COMPACT_WIDTH);
    };

    updateCompact(element.getBoundingClientRect().width);

    const observer = new ResizeObserver((entries) => {
      const entry = entries[0];
      if (!entry) return;
      updateCompact(entry.contentRect.width);
    });

    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  return (
    <nav
      ref={navRef}
      aria-label={ariaLabel ?? t('plugin_shell.breadcrumbs', 'Breadcrumbs')}
      className={rootClassName}
    >
      {items.length === 0 && showWorkbench && (
        <span className="ag-plugin-breadcrumb-workbench max-w-48 shrink-0 truncate px-1 py-1 font-mono text-[11px] leading-5 text-text-tertiary">
          {displayPluginName}
        </span>
      )}

      {items.map((item, index) => {
        const isLast = index === items.length - 1;
        const label = item.labelKey
          ? t(item.labelKey, { defaultValue: item.labelFallback })
          : item.labelFallback;

        return (
          <span
            key={`${index}-${label}`}
            className={`flex items-center gap-1.5 ${isLast ? 'min-w-0 flex-1' : 'shrink-0'}`}
          >
            {item.to && !isLast ? (
              <Link
                to={item.to}
                preload={false}
                className="shrink-0 rounded-[var(--radius)] px-1.5 py-1 text-text-tertiary transition-colors hover:bg-bg-hover hover:text-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
              >
                {label}
              </Link>
            ) : item.href && !isLast ? (
              <a
                href={item.href}
                className="shrink-0 rounded-[var(--radius)] px-1.5 py-1 text-text-tertiary no-underline transition-colors hover:bg-bg-hover hover:text-text focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
              >
                {label}
              </a>
            ) : (
              <span aria-current="page" className="min-w-0 truncate px-1 py-1 font-medium text-text">
                {label}
              </span>
            )}
            {isLast && showWorkbench && (
              <span className="ag-plugin-breadcrumb-workbench max-w-48 shrink-0 truncate px-0.5 py-1 font-mono text-[11px] leading-5 text-text-tertiary">
                {displayPluginName}
              </span>
            )}
            {!isLast && <Separator />}
          </span>
        );
      })}
    </nav>
  );
}
