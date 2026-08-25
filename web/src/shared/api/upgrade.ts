import { get, post } from './client';

export type UpgradeMode = 'systemd' | 'docker' | 'noop';

export type UpgradeState =
  | 'idle'
  | 'checking'
  | 'downloading'
  | 'verifying'
  | 'swapping'
  | 'restarting'
  | 'failed'
  | 'success';

export interface UpgradeInfo {
  mode: UpgradeMode;
  current: string;
  latest?: string;
  has_update: boolean;
  release_url?: string;
  release_notes?: string;
  instructions?: string;
  can_upgrade: boolean;
  binary_path?: string;
  checked_at?: string;
}

export interface UpgradeStatus {
  state: UpgradeState;
  progress: number;
  message?: string;
  error?: string;
  target?: string;
}

export interface RunUpgradeReq {
  confirm_db_backup: boolean;
}

export const upgradeApi = {
  info: () => get<UpgradeInfo>('/api/v1/admin/upgrade/info'),
  status: () => get<UpgradeStatus>('/api/v1/admin/upgrade/status'),
  run: (data: RunUpgradeReq) => post<{ started: boolean }>('/api/v1/admin/upgrade/run', data),
};
