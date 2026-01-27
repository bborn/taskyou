import { useState, useEffect } from 'react';
import {
  Search,
  Settings,
  LogOut,
  Moon,
  Sun,
  Monitor,
  Command,
  Zap,
  ChevronDown,
} from 'lucide-react';
import { Avatar, AvatarImage, AvatarFallback } from '@/components/ui/avatar';
import { Button } from '@/components/ui/button';
import type { User } from '@/api/types';

interface HeaderProps {
  user: User;
  onLogout: () => void;
  onSettings: () => void;
  onCommandPalette: () => void;
}

type Theme = 'light' | 'dark' | 'system';

export function Header({ user, onLogout, onSettings, onCommandPalette }: HeaderProps) {
  const [showUserMenu, setShowUserMenu] = useState(false);
  const [theme, setTheme] = useState<Theme>(() => {
    const stored = localStorage.getItem('theme') as Theme;
    return stored || 'system';
  });

  // Apply theme on mount and changes
  useEffect(() => {
    const root = document.documentElement;
    if (theme === 'system') {
      const systemDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
      root.classList.toggle('dark', systemDark);
    } else {
      root.classList.toggle('dark', theme === 'dark');
    }
  }, [theme]);

  const handleThemeChange = (newTheme: Theme) => {
    setTheme(newTheme);
    localStorage.setItem('theme', newTheme);
  };

  const getInitials = (name: string) => {
    return name
      .split(' ')
      .map((n) => n[0])
      .join('')
      .toUpperCase()
      .slice(0, 2);
  };

  const themeIcons = {
    light: Sun,
    dark: Moon,
    system: Monitor,
  };
  const ThemeIcon = themeIcons[theme];

  return (
    <header className="sticky top-0 z-40 border-b border-border bg-background/80 backdrop-blur-lg">
      <div className="container mx-auto px-4">
        <div className="flex h-16 items-center justify-between">
          {/* Logo */}
          <div className="flex items-center gap-3">
            <div className="flex items-center gap-2">
              <div className="h-8 w-8 rounded-lg bg-gradient-to-br from-primary to-purple-600 flex items-center justify-center">
                <Zap className="h-5 w-5 text-white" />
              </div>
              <span className="font-bold text-xl">
                task<span className="text-primary">you</span>
              </span>
            </div>
            {user.sprite && (
              <span className="hidden sm:flex items-center gap-1.5 text-xs text-muted-foreground px-2 py-1 rounded-full bg-muted">
                {user.sprite.status === 'running' ? (
                  <>
                    <span className="h-1.5 w-1.5 rounded-full bg-green-500 animate-pulse" />
                    Online
                  </>
                ) : (
                  <>
                    <span className="h-1.5 w-1.5 rounded-full bg-muted-foreground" />
                    {user.sprite.status}
                  </>
                )}
              </span>
            )}
          </div>

          {/* Center - Search Trigger */}
          <button
            onClick={onCommandPalette}
            className="hidden md:flex items-center gap-2 px-3 py-1.5 rounded-lg border border-border bg-muted/50 hover:bg-muted text-sm text-muted-foreground transition-colors min-w-[280px]"
          >
            <Search className="h-4 w-4" />
            <span className="flex-1 text-left">Search tasks...</span>
            <kbd className="flex items-center gap-0.5 px-1.5 py-0.5 text-xs bg-background rounded border border-border">
              <Command className="h-3 w-3" />K
            </kbd>
          </button>

          {/* Right side */}
          <div className="flex items-center gap-2">
            {/* Mobile search */}
            <Button
              variant="ghost"
              size="icon"
              className="md:hidden"
              onClick={onCommandPalette}
            >
              <Search className="h-5 w-5" />
            </Button>

            {/* Theme toggle */}
            <Button
              variant="ghost"
              size="icon"
              onClick={() => {
                const themes: Theme[] = ['light', 'dark', 'system'];
                const currentIndex = themes.indexOf(theme);
                const nextTheme = themes[(currentIndex + 1) % themes.length];
                handleThemeChange(nextTheme);
              }}
              title={`Theme: ${theme}`}
            >
              <ThemeIcon className="h-5 w-5" />
            </Button>

            {/* Settings */}
            <Button variant="ghost" size="icon" onClick={onSettings}>
              <Settings className="h-5 w-5" />
            </Button>

            {/* User menu */}
            <div className="relative">
              <button
                onClick={() => setShowUserMenu(!showUserMenu)}
                className="flex items-center gap-2 p-1.5 rounded-lg hover:bg-muted transition-colors"
              >
                <Avatar className="h-8 w-8">
                  <AvatarImage src={user.avatar_url} alt={user.name} />
                  <AvatarFallback className="bg-primary/10 text-primary text-xs font-medium">
                    {getInitials(user.name || user.email)}
                  </AvatarFallback>
                </Avatar>
                <ChevronDown className="h-4 w-4 text-muted-foreground hidden sm:block" />
              </button>

              {/* Dropdown */}
              {showUserMenu && (
                <>
                  <div
                    className="fixed inset-0 z-40"
                    onClick={() => setShowUserMenu(false)}
                  />
                  <div className="absolute right-0 top-full mt-2 w-56 rounded-lg border border-border bg-card shadow-lg z-50 py-1">
                    <div className="px-3 py-2 border-b border-border">
                      <p className="font-medium text-sm truncate">{user.name}</p>
                      <p className="text-xs text-muted-foreground truncate">{user.email}</p>
                    </div>
                    <button
                      onClick={() => {
                        setShowUserMenu(false);
                        onSettings();
                      }}
                      className="w-full flex items-center gap-2 px-3 py-2 text-sm hover:bg-muted transition-colors"
                    >
                      <Settings className="h-4 w-4" />
                      Settings
                    </button>
                    <button
                      onClick={() => {
                        setShowUserMenu(false);
                        onLogout();
                      }}
                      className="w-full flex items-center gap-2 px-3 py-2 text-sm text-red-500 hover:bg-red-50 dark:hover:bg-red-950/20 transition-colors"
                    >
                      <LogOut className="h-4 w-4" />
                      Sign out
                    </button>
                  </div>
                </>
              )}
            </div>
          </div>
        </div>
      </div>
    </header>
  );
}
