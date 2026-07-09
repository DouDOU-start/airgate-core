import type { CSSProperties, ReactNode } from 'react';
import { cx } from '../utils/cx';

interface NativeSwitchProps {
  ariaLabel?: string;
  children?: ReactNode;
  className?: string;
  contentClassName?: string;
  contentStyle?: CSSProperties;
  isDisabled?: boolean;
  isSelected: boolean;
  label?: ReactNode;
  name?: string;
  onChange: (selected: boolean) => void;
}

export function NativeSwitch({
  ariaLabel,
  children,
  className,
  contentClassName,
  contentStyle,
  isDisabled = false,
  isSelected,
  label,
  name,
  onChange,
}: NativeSwitchProps) {
  const content = label ?? children;
  const rootClassName = cx('ag-native-switch', className);
  const labelClassName = cx('ag-native-switch-content', contentClassName);

  return (
    <label className={rootClassName} data-disabled={isDisabled ? 'true' : 'false'}>
      <input
        aria-label={ariaLabel}
        checked={isSelected}
        className="ag-native-switch-input"
        disabled={isDisabled}
        name={name}
        role="switch"
        type="checkbox"
        onChange={(event) => onChange(event.currentTarget.checked)}
      />
      <span className="ag-native-switch-track" aria-hidden="true">
        <span className="ag-native-switch-thumb" />
      </span>
      {content ? (
        <span className={labelClassName} style={contentStyle}>
          {content}
        </span>
      ) : null}
    </label>
  );
}
