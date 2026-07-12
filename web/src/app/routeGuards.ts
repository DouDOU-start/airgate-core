import { redirect } from '@tanstack/react-router';
import { getToken, getTokenRole } from '../shared/api/client';
import { usersApi } from '../shared/api/users';

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
