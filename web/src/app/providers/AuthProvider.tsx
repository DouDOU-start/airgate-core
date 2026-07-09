import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import type { UserResp } from '../../shared/types';
import {
  setToken,
  getToken,
  setSessionAPIKey,
  getTokenAPIKeyID,
  getTokenRole,
} from '../../shared/api/client';
import { usersApi } from '../../shared/api/users';
import { resetAdminCache } from '../routeGuards';

interface AuthContextType {
  user: UserResp | null;
  loading: boolean;
  /** 是否为 API Key 登录 */
  isAPIKeySession: boolean;
  login: (token: string, user: UserResp) => void;
  logout: () => void;
}

const AuthContext = createContext<AuthContextType>({
  user: null,
  loading: true,
  isAPIKeySession: false,
  login: () => {},
  logout: () => {},
});

function normalizeSessionUser(user: UserResp, token = getToken()): UserResp {
  const role = getTokenRole(token);
  const apiKeyID = getTokenAPIKeyID(token);
  const effectiveRole: UserResp['role'] = apiKeyID ? 'api_key' : (role ?? user.role);

  return {
    ...user,
    role: effectiveRole,
    ...(apiKeyID ? { api_key_id: apiKeyID } : {}),
  };
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<UserResp | null>(null);
  const [loading, setLoading] = useState(true);
  const authRevisionRef = useRef(0);

  useEffect(() => {
    let cancelled = false;
    const revision = authRevisionRef.current;
    const token = getToken();
    if (token) {
      usersApi.me()
        .then((userData) => {
          const currentToken = getToken();
          if (!cancelled && authRevisionRef.current === revision && currentToken) {
            setUser(normalizeSessionUser(userData, currentToken));
          }
        })
        .catch(() => {
          if (!cancelled && authRevisionRef.current === revision && getToken() === token) {
            resetAdminCache();
            setToken(null);
            setUser(null);
          }
        })
        .finally(() => {
          if (!cancelled && authRevisionRef.current === revision) setLoading(false);
        });
    } else {
      setLoading(false);
    }

    return () => {
      cancelled = true;
    };
  }, []);

  // 窗口回焦 / 标签页重新可见时刷新会话用户信息（余额、并发数等），
  // 与 react-query 的 refetchOnWindowFocus 行为对齐：管理员在别处调整了
  // 余额后，用户切回本标签页即可看到最新值，无需整页刷新。
  // 节流 5s，避免频繁切换窗口时重复请求。
  const lastFocusRefreshRef = useRef(0);
  useEffect(() => {
    const REFRESH_MIN_INTERVAL_MS = 5_000;
    const refresh = () => {
      if (!getToken() || document.visibilityState === 'hidden') return;
      const now = Date.now();
      if (now - lastFocusRefreshRef.current < REFRESH_MIN_INTERVAL_MS) return;
      lastFocusRefreshRef.current = now;
      const revision = authRevisionRef.current;
      usersApi.me()
        .then((freshUser) => {
          const currentToken = getToken();
          if (authRevisionRef.current === revision && currentToken) {
            setUser(normalizeSessionUser(freshUser, currentToken));
          }
        })
        // 静默失败：网络抖动不打断会话，登录态失效由请求层统一处理
        .catch(() => {});
    };
    window.addEventListener('focus', refresh);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      window.removeEventListener('focus', refresh);
      document.removeEventListener('visibilitychange', refresh);
    };
  }, []);

  const login = useCallback((token: string, userData: UserResp) => {
    authRevisionRef.current += 1;
    const revision = authRevisionRef.current;
    resetAdminCache();
    setToken(token);
    setUser(normalizeSessionUser(userData, token));
    // 登录响应可能不包含全部用户字段（例如 API Key 登录时缺少 quota / expires_at），
    // 异步用 /me 拉一次完整数据补齐，避免首屏额度等信息显示不准。
    usersApi.me()
      .then((freshUser) => {
        const currentToken = getToken();
        if (authRevisionRef.current === revision && currentToken) {
          setUser(normalizeSessionUser(freshUser, currentToken));
        }
      })
      .catch(() => {});
  }, []);

  const logout = useCallback(() => {
    authRevisionRef.current += 1;
    setToken(null);
    setSessionAPIKey(null);
    setUser(null);
    resetAdminCache();
    window.location.href = '/login';
  }, []);

  const isAPIKeySession = user?.role === 'api_key' || !!(user?.api_key_id && user.api_key_id > 0);
  const value = useMemo(
    () => ({ user, loading, isAPIKeySession, login, logout }),
    [isAPIKeySession, loading, login, logout, user],
  );

  return (
    <AuthContext.Provider value={value}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  return useContext(AuthContext);
}
