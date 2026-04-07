import { createContext, useContext, useState, useEffect, type ReactNode } from 'react';
import type { UserResp } from '../../shared/types';
import { setToken, getToken } from '../../shared/api/client';
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
  };

  const logout = () => {
    setToken(null);
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
