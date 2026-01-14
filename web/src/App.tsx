import { useState } from 'react';
import { useAuth } from './hooks/useAuth';
import { LoginPage } from './pages/LoginPage';
import { DashboardPage } from './pages/DashboardPage';
import { SettingsPage } from './pages/SettingsPage';

type View = 'dashboard' | 'settings';

function App() {
  const { user, loading, logout } = useAuth();
  const [view, setView] = useState<View>('dashboard');

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-background">
        <div className="flex flex-col items-center gap-4">
          <div className="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent" />
          <p className="text-muted-foreground">Loading...</p>
        </div>
      </div>
    );
  }

  if (!user) {
    return <LoginPage />;
  }

  if (view === 'settings') {
    return <SettingsPage onBack={() => setView('dashboard')} />;
  }

  return (
    <DashboardPage
      user={user}
      onLogout={logout}
      onSettings={() => setView('settings')}
    />
  );
}

export default App;
