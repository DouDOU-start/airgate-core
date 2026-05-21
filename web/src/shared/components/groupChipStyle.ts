import type { CSSProperties } from 'react';

const GROUP_CHIP_COLOR = 'oklch(62.04% 0.1950 253.83)';

export const GROUP_CHIP_STYLE: CSSProperties = {
  background: `color-mix(in srgb, ${GROUP_CHIP_COLOR} 18%, transparent)`,
  boxShadow: `inset 0 0 0 1px color-mix(in srgb, ${GROUP_CHIP_COLOR} 34%, transparent)`,
  color: GROUP_CHIP_COLOR,
};
