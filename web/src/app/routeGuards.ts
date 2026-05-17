import { redirect } from '@tanstack/react-router';
import { getToken, getTokenRole } from '../shared/api/client';
import { setupApi } from '../shared/api/setup';
import { usersApi } from '../shared/api/users';

// 缓存安装状态，避免每次路由跳转都请求 /setup/status。
const SETUP_COMPLETE_STORAGE_KEY = 'airgate:setup:complete';

function readSetupCompleteCache(): boolean {
  if (typeof window === 'undefined') return false;
  try {
    return window.localStorage.getItem(SETUP_COMPLETE_STORAGE_KEY) === 'true';
  } catch {
    return false;
  }
}

function writeSetupCompleteCache(value: boolean) {
  if (typeof window === 'undefined') return;
  try {
    if (value) window.localStorage.setItem(SETUP_COMPLETE_STORAGE_KEY, 'true');
    else window.localStorage.removeItem(SETUP_COMPLETE_STORAGE_KEY);
  } catch {
    // Storage can be unavailable; the in-memory cache still covers this session.
  }
}

let setupChecked = readSetupCompleteCache();
let needsSetup = false;
let setupCheckPromise: Promise<boolean> | null = null;

function checkSetup(): boolean | Promise<boolean> {
  if (setupChecked) return needsSetup;
  if (setupCheckPromise) return setupCheckPromise;

  setupCheckPromise = (async () => {
    try {
      const resp = await setupApi.status();
      needsSetup = resp.needs_setup;
    } catch {
      needsSetup = true;
    }
    setupChecked = true;
    writeSetupCompleteCache(!needsSetup);
    return needsSetup;
  })();

  const p = setupCheckPromise;
  return p.finally(() => {
    if (setupCheckPromise === p) setupCheckPromise = null;
  });
}

export function withSetupCheck(handler: (needs: boolean) => void): void | Promise<void> {
  const result = checkSetup();
  if (result instanceof Promise) return result.then(handler);
  return handler(result);
}

// 需要强制重新检查安装状态时调用。
export function resetSetupCache() {
  setupChecked = false;
  needsSetup = false;
  setupCheckPromise = null;
  writeSetupCompleteCache(false);
}

// 安装完成后调用，直接标记 setup 已完成，避免跳转登录页前再次阻塞 /setup/status。
export function markSetupComplete() {
  setupChecked = true;
  needsSetup = false;
  setupCheckPromise = null;
  writeSetupCompleteCache(true);
}

// 缓存管理员身份校验结果，避免每次 admin 路由切换都请求 /users/me。
let adminVerified = false;
let adminVerifiedToken: string | null = null;
let adminCheckPromise: Promise<void> | null = null;
let adminCheckToken: string | null = null;

export function checkAdmin(): void | Promise<void> {
  const token = getToken();
  if (getTokenRole(token) === 'admin') {
    adminVerified = true;
    adminVerifiedToken = token;
    return;
  }

  if (adminVerified && adminVerifiedToken === token) return;

  if (adminCheckPromise && adminCheckToken === token) return adminCheckPromise;

  adminCheckToken = token;
  adminCheckPromise = (async () => {
    const user = await usersApi.me();
    if (user.role !== 'admin') {
      throw redirect({ to: '/' });
    }
    adminVerified = true;
    adminVerifiedToken = token;
  })();

  const p = adminCheckPromise;
  return p.finally(() => {
    if (adminCheckPromise === p) {
      adminCheckPromise = null;
      adminCheckToken = null;
    }
  });
}

export function resetAdminCache() {
  adminVerified = false;
  adminVerifiedToken = null;
  adminCheckPromise = null;
  adminCheckToken = null;
}
