import { createElement, useEffect, useState } from 'react';
import type { CSSProperties } from 'react';
import { Server } from 'lucide-react';
import { getPluginPlatformIcon, onPlatformIconChange } from '../../../app/plugin-loader';

interface PlatformIconProps {
  platform: string;
  className?: string;
  style?: CSSProperties;
}

export function PlatformIcon({ platform, className = 'w-3.5 h-3.5', style }: PlatformIconProps) {
  const [, setVersion] = useState(0);

  useEffect(() => onPlatformIconChange(() => setVersion((value) => value + 1)), []);

  const PluginIcon = getPluginPlatformIcon(platform);
  if (PluginIcon) return createElement(PluginIcon, { className, style });
  return <Server className={className} style={style} />;
}
