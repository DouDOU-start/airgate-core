export interface KeyForm {
  name: string;
  group_id: string;
  quota_usd: string;
  expires_at: string;
}

export const emptyForm: KeyForm = {
  name: '',
  group_id: '',
  quota_usd: '',
  expires_at: '',
};
