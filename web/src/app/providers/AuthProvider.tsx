import { createContext, useContext, useState, useEffect, type ReactNode } from 'react';
import type { UserResp } from '../../shared/types';
import { setToken, getToken, setSessionAPIKey } from '../../shared/api/client';
import { usersApi } from '../../shared/api/users';

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

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<UserResp | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const token = getToken();
    if (token) {
      usersApi.me()
        .then(setUser)
        .catch(() => setToken(null))
        .finally(() => setLoading(false));
    } else {
      setLoading(false);
    }
  }, []);

  const login = (token: string, userData: UserResp) => {
    setToken(token);
    setUser(userData);
    // 登录响应可能不包含全部用户字段（例如 API Key 登录时缺少 quota / expires_at），
    // 异步用 /me 拉一次完整数据补齐，避免首屏额度等信息显示不准。
    usersApi.me().then(setUser).catch(() => {});
  };

  const logout = () => {
    setToken(null);
    setSessionAPIKey(null);
    setUser(null);
    window.location.href = '/login';
  };

  const isAPIKeySession = !!(user?.api_key_id && user.api_key_id > 0);

  return (
    <AuthContext.Provider value={{ user, loading, isAPIKeySession, login, logout }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  return useContext(AuthContext);
}
