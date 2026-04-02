import { useState, useEffect, useRef, type ComponentType } from 'react';
import { pluginsApi } from '../../../shared/api/plugins';
import { FETCH_ALL_PARAMS } from '../../../shared/constants';
import {
  loadPluginFrontend,
  type AccountFormProps,
  type PluginOAuthBridge,
} from '../../../app/plugin-loader';
import type {
  CredentialField,
  AccountTypeResp,
  CredentialSchemaResp,
} from '../../../shared/types';

/** 平台 → 插件名称映射缓存 */
let platformPluginMap: Map<string, string> | null = null;

export async function getPlatformPluginMap(): Promise<Map<string, string>> {
  if (platformPluginMap) return platformPluginMap;
  const resp = await pluginsApi.list(FETCH_ALL_PARAMS);
  const map = new Map<string, string>();
  for (const p of resp.list) {
    if (p.platform) map.set(p.platform, p.name);
  }
  platformPluginMap = map;
  return map;
}

export function detectCredentialAccountType(credentials: Record<string, string>): string {
  if (credentials.provider === 'sub2api') return 'sub2api';
  if (credentials.api_key) return 'apikey';
  if (credentials.access_token) return 'oauth';
  return '';
}

export function getSchemaAccountTypes(schema?: CredentialSchemaResp): AccountTypeResp[] {
  return schema?.account_types ?? [];
}

export function getSchemaSelectedAccountType(
  schema: CredentialSchemaResp | undefined,
  accountType: string,
): AccountTypeResp | undefined {
  const accountTypes = getSchemaAccountTypes(schema);
  if (!accountTypes.length) return undefined;
  return accountTypes.find((item) => item.key === accountType) ?? accountTypes[0];
}

export function getSchemaVisibleFields(
  schema: CredentialSchemaResp | undefined,
  accountType: string,
): CredentialField[] {
  const selectedType = getSchemaSelectedAccountType(schema, accountType);
  if (selectedType) return selectedType.fields;
  return schema?.fields ?? [];
}

export function filterCredentialsForAccountType(
  credentials: Record<string, string>,
  accountType?: AccountTypeResp,
): Record<string, string> {
  if (!accountType) return credentials;

  const allowedKeys = new Set(accountType.fields.map((field) => field.key));
  const next: Record<string, string> = {};
  for (const [key, value] of Object.entries(credentials)) {
    if (allowedKeys.has(key)) {
      next[key] = value;
    }
  }
  return next;
}

const pluginFormCache = new Map<string, ComponentType<AccountFormProps> | null>();

export function usePluginAccountForm(platform: string) {
  const [Form, setForm] = useState<ComponentType<AccountFormProps> | null>(null);
  const [pluginId, setPluginId] = useState('');
  const loadedRef = useRef('');

  useEffect(() => {
    if (!platform) {
      setForm(null);
      setPluginId('');
      loadedRef.current = '';
      return;
    }
    if (loadedRef.current === platform) return;
    loadedRef.current = platform;
    let cancelled = false;

    getPlatformPluginMap().then((map) => {
      const resolvedPluginId = map.get(platform) ?? '';
      if (cancelled) return;

      setPluginId(resolvedPluginId);

      if (!resolvedPluginId) {
        setForm(null);
        return;
      }
      if (pluginFormCache.has(resolvedPluginId)) {
        const cachedForm = pluginFormCache.get(resolvedPluginId) ?? null;
        setForm(() => cachedForm);
        return;
      }
      loadPluginFrontend(resolvedPluginId).then((mod) => {
        if (cancelled) return;
        const form = mod?.accountForm ?? null;
        pluginFormCache.set(resolvedPluginId, form);
        setForm(() => form);
      });
    });

    return () => {
      cancelled = true;
    };
  }, [platform]);

  return { Form, pluginId };
}

export function createPluginOAuthBridge(pluginId: string): PluginOAuthBridge | undefined {
  if (!pluginId) return undefined;

  return {
    start: async () => {
      const result = await pluginsApi.rpc<{ authorize_url: string; state: string }>(
        pluginId, 'oauth/start',
      );
      return {
        authorizeURL: result.authorize_url,
        state: result.state,
      };
    },
    exchange: async (callbackURL: string) => {
      const result = await pluginsApi.rpc<{
        account_type: string; account_name: string; credentials: Record<string, string>;
      }>(pluginId, 'oauth/exchange', { callback_url: callbackURL });
      return {
        accountType: result.account_type,
        accountName: result.account_name,
        credentials: result.credentials,
      };
    },
  };
}
