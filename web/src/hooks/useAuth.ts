import { useState, useEffect, useCallback } from 'react';
import { auth } from '@/api/client';
import type { User } from '@/api/types';

// Mock user for development mode
const DEV_USER: User = {
  id: 'dev-user',
  email: 'dev@localhost',
  name: 'Development User',
  avatar_url: '',
};

interface UseAuthReturn {
  user: User | null;
  loading: boolean;
  error: Error | null;
  logout: () => Promise<void>;
  refresh: () => Promise<void>;
}

export function useAuth(): UseAuthReturn {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  const fetchUser = useCallback(async () => {
    // In development mode, use a mock user to bypass OAuth
    if (import.meta.env.DEV) {
      setUser(DEV_USER);
      setLoading(false);
      return;
    }

    try {
      setLoading(true);
      setError(null);
      const userData = await auth.getMe();
      setUser(userData);
    } catch (err) {
      setUser(null);
      // Don't set error for unauthorized - that's expected for unauthenticated users
      if (err instanceof Error && !err.message.includes('Unauthorized')) {
        setError(err);
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchUser();
  }, [fetchUser]);

  const logout = useCallback(async () => {
    try {
      await auth.logout();
      setUser(null);
    } catch (err) {
      console.error('Logout failed:', err);
    }
  }, []);

  return {
    user,
    loading,
    error,
    logout,
    refresh: fetchUser,
  };
}
