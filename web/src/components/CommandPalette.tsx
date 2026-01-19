import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import {
  Search,
  Clock,
  Zap,
  AlertCircle,
  CheckCircle,
  Plus,
  Settings,
  ArrowRight,
  Command,
} from 'lucide-react';
import type { Task, TaskStatus } from '@/api/types';
import { cn } from '@/lib/utils';

interface CommandPaletteProps {
  isOpen: boolean;
  onClose: () => void;
  tasks: Task[];
  onSelectTask: (task: Task) => void;
  onNewTask: () => void;
  onSettings: () => void;
}

interface CommandItem {
  id: string;
  type: 'task' | 'action';
  task?: Task;
  label: string;
  description?: string;
  icon: React.ElementType;
  iconColor?: string;
  action?: () => void;
}

const statusIcons: Record<TaskStatus, { icon: React.ElementType; color: string }> = {
  backlog: { icon: Clock, color: 'text-[hsl(var(--status-backlog))]' },
  queued: { icon: Clock, color: 'text-[hsl(var(--status-queued))]' },
  processing: { icon: Zap, color: 'text-[hsl(var(--status-processing))]' },
  blocked: { icon: AlertCircle, color: 'text-[hsl(var(--status-blocked))]' },
  done: { icon: CheckCircle, color: 'text-[hsl(var(--status-done))]' },
};

export function CommandPalette({
  isOpen,
  onClose,
  tasks,
  onSelectTask,
  onNewTask,
  onSettings,
}: CommandPaletteProps) {
  const [query, setQuery] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Reset state when opened
  useEffect(() => {
    if (isOpen) {
      setQuery('');
      setSelectedIndex(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [isOpen]);

  // Build filtered items
  const items = useMemo<CommandItem[]>(() => {
    const actions: CommandItem[] = [
      {
        id: 'new-task',
        type: 'action',
        label: 'New Task',
        description: 'Create a new task',
        icon: Plus,
        iconColor: 'text-primary',
        action: () => {
          onClose();
          onNewTask();
        },
      },
      {
        id: 'settings',
        type: 'action',
        label: 'Settings',
        description: 'Open settings',
        icon: Settings,
        iconColor: 'text-muted-foreground',
        action: () => {
          onClose();
          onSettings();
        },
      },
    ];

    const taskItems: CommandItem[] = tasks.map((task) => {
      const statusInfo = statusIcons[task.status];
      return {
        id: `task-${task.id}`,
        type: 'task',
        task,
        label: task.title,
        description: task.project !== 'personal' ? task.project : undefined,
        icon: statusInfo.icon,
        iconColor: statusInfo.color,
        action: () => {
          onClose();
          onSelectTask(task);
        },
      };
    });

    if (!query.trim()) {
      // Show recent/active tasks first, then actions
      const activeTasks = taskItems.filter(
        (t) => t.task && ['processing', 'queued', 'blocked'].includes(t.task.status)
      );
      const recentTasks = taskItems
        .filter((t) => t.task && !['processing', 'queued', 'blocked'].includes(t.task.status))
        .slice(0, 5);

      return [...actions, ...activeTasks, ...recentTasks];
    }

    const lowerQuery = query.toLowerCase();

    // Filter tasks by query
    const filteredTasks = taskItems.filter((item) => {
      if (!item.task) return false;
      const task = item.task;
      return (
        task.title.toLowerCase().includes(lowerQuery) ||
        task.project.toLowerCase().includes(lowerQuery) ||
        task.id.toString().includes(lowerQuery) ||
        (task.pr_number && task.pr_number.toString().includes(lowerQuery)) ||
        (task.pr_url && task.pr_url.toLowerCase().includes(lowerQuery))
      );
    });

    // Filter actions by query
    const filteredActions = actions.filter(
      (item) => item.label.toLowerCase().includes(lowerQuery)
    );

    return [...filteredActions, ...filteredTasks];
  }, [tasks, query, onClose, onSelectTask, onNewTask, onSettings]);

  // Clamp selected index
  useEffect(() => {
    if (selectedIndex >= items.length) {
      setSelectedIndex(Math.max(0, items.length - 1));
    }
  }, [items.length, selectedIndex]);

  // Scroll selected item into view
  useEffect(() => {
    const selectedEl = listRef.current?.children[selectedIndex] as HTMLElement;
    selectedEl?.scrollIntoView({ block: 'nearest' });
  }, [selectedIndex]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      switch (e.key) {
        case 'ArrowDown':
          e.preventDefault();
          setSelectedIndex((i) => Math.min(i + 1, items.length - 1));
          break;
        case 'ArrowUp':
          e.preventDefault();
          setSelectedIndex((i) => Math.max(i - 1, 0));
          break;
        case 'Enter':
          e.preventDefault();
          items[selectedIndex]?.action?.();
          break;
        case 'Escape':
          e.preventDefault();
          onClose();
          break;
      }
    },
    [items, selectedIndex, onClose]
  );

  // Global keyboard shortcut
  useEffect(() => {
    const handleGlobalKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        if (isOpen) {
          onClose();
        }
      }
    };

    window.addEventListener('keydown', handleGlobalKeyDown);
    return () => window.removeEventListener('keydown', handleGlobalKeyDown);
  }, [isOpen, onClose]);

  return (
    <AnimatePresence>
      {isOpen && (
        <motion.div
          className="fixed inset-0 z-[100] flex items-start justify-center pt-[15vh] px-4"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
        >
          {/* Backdrop */}
          <motion.div
            className="absolute inset-0 bg-black/50 backdrop-blur-sm"
            onClick={onClose}
          />

          {/* Dialog */}
          <motion.div
            className="relative w-full max-w-xl bg-card rounded-xl shadow-2xl border border-border overflow-hidden"
            initial={{ opacity: 0, scale: 0.95, y: -20 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.95, y: -20 }}
            transition={{ type: 'spring', damping: 25, stiffness: 300 }}
          >
            {/* Search input */}
            <div className="flex items-center gap-3 px-4 py-3 border-b border-border">
              <Search className="h-5 w-5 text-muted-foreground shrink-0" />
              <input
                ref={inputRef}
                type="text"
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                  setSelectedIndex(0);
                }}
                onKeyDown={handleKeyDown}
                placeholder="Search tasks, run actions..."
                className="flex-1 bg-transparent text-base outline-none placeholder:text-muted-foreground"
              />
              <kbd className="hidden sm:flex items-center gap-1 px-2 py-1 text-xs text-muted-foreground bg-muted rounded">
                <Command className="h-3 w-3" />K
              </kbd>
            </div>

            {/* Results */}
            <div
              ref={listRef}
              className="max-h-[400px] overflow-y-auto scrollbar-thin py-2"
            >
              {items.length === 0 ? (
                <div className="px-4 py-8 text-center text-muted-foreground">
                  No results found for "{query}"
                </div>
              ) : (
                items.map((item, index) => {
                  const Icon = item.icon;
                  const isSelected = index === selectedIndex;

                  return (
                    <button
                      key={item.id}
                      onClick={() => item.action?.()}
                      onMouseEnter={() => setSelectedIndex(index)}
                      className={cn(
                        'w-full flex items-center gap-3 px-4 py-2.5 text-left transition-colors',
                        isSelected
                          ? 'bg-accent text-accent-foreground'
                          : 'hover:bg-muted/50'
                      )}
                    >
                      <Icon className={cn('h-4 w-4 shrink-0', item.iconColor)} />
                      <div className="flex-1 min-w-0">
                        <div className="font-medium text-sm truncate">
                          {item.label}
                        </div>
                        {item.description && (
                          <div className="text-xs text-muted-foreground truncate">
                            {item.description}
                          </div>
                        )}
                      </div>
                      {isSelected && (
                        <ArrowRight className="h-4 w-4 text-muted-foreground shrink-0" />
                      )}
                    </button>
                  );
                })
              )}
            </div>

            {/* Footer */}
            <div className="flex items-center justify-between px-4 py-2 border-t border-border text-xs text-muted-foreground">
              <div className="flex items-center gap-4">
                <span className="flex items-center gap-1">
                  <kbd className="px-1.5 py-0.5 bg-muted rounded text-[10px]">↑↓</kbd>
                  navigate
                </span>
                <span className="flex items-center gap-1">
                  <kbd className="px-1.5 py-0.5 bg-muted rounded text-[10px]">↵</kbd>
                  select
                </span>
                <span className="flex items-center gap-1">
                  <kbd className="px-1.5 py-0.5 bg-muted rounded text-[10px]">esc</kbd>
                  close
                </span>
              </div>
              <span>{items.length} results</span>
            </div>
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
