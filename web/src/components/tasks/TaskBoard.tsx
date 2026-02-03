import { useMemo } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import {
  Plus,
  Zap,
  AlertCircle,
  CheckCircle,
  Inbox,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { TaskCard } from './TaskCard';
import type { Task, TaskStatus } from '@/api/types';
import { cn } from '@/lib/utils';

interface TaskBoardProps {
  tasks: Task[];
  onQueue: (id: number) => void;
  onRetry: (id: number) => void;
  onClose: (id: number) => void;
  onTaskClick: (task: Task) => void;
  onNewTask: () => void;
}

interface Column {
  id: string;
  title: string;
  statuses: TaskStatus[];
  icon: React.ElementType;
  emptyMessage: string;
  accentColor: string;
}

const columns: Column[] = [
  {
    id: 'backlog',
    title: 'Backlog',
    statuses: ['backlog'],
    icon: Inbox,
    emptyMessage: 'No tasks waiting',
    accentColor: 'hsl(var(--status-backlog))',
  },
  {
    id: 'in_progress',
    title: 'In Progress',
    statuses: ['queued', 'processing'],
    icon: Zap,
    emptyMessage: 'Nothing running',
    accentColor: 'hsl(var(--status-processing))',
  },
  {
    id: 'blocked',
    title: 'Needs Attention',
    statuses: ['blocked'],
    icon: AlertCircle,
    emptyMessage: 'All clear!',
    accentColor: 'hsl(var(--status-blocked))',
  },
  {
    id: 'done',
    title: 'Completed',
    statuses: ['done'],
    icon: CheckCircle,
    emptyMessage: 'Nothing completed yet',
    accentColor: 'hsl(var(--status-done))',
  },
];

const containerVariants = {
  hidden: { opacity: 0 },
  visible: {
    opacity: 1,
    transition: {
      staggerChildren: 0.05,
    },
  },
};

const columnVariants = {
  hidden: { opacity: 0, y: 20 },
  visible: { opacity: 1, y: 0 },
};

export function TaskBoard({
  tasks,
  onQueue,
  onRetry,
  onClose,
  onTaskClick,
  onNewTask,
}: TaskBoardProps) {
  const tasksByColumn = useMemo(() => {
    const grouped: Record<string, Task[]> = {};

    for (const column of columns) {
      grouped[column.id] = tasks
        .filter(task => column.statuses.includes(task.status))
        .sort((a, b) => {
          // Processing tasks first, then by most recent
          if (a.status === 'processing' && b.status !== 'processing') return -1;
          if (b.status === 'processing' && a.status !== 'processing') return 1;
          return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime();
        });
    }

    return grouped;
  }, [tasks]);

  const totalActive = tasksByColumn['in_progress']?.length || 0;
  const totalBlocked = tasksByColumn['blocked']?.length || 0;

  return (
    <div className="h-full">
      {/* Header */}
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-4">
          <h1 className="text-2xl font-bold text-gradient">Tasks</h1>
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            {totalActive > 0 && (
              <span className="flex items-center gap-1 px-2 py-1 rounded-full bg-[hsl(var(--status-processing-bg))] text-[hsl(var(--status-processing))]">
                <Zap className="h-3.5 w-3.5" />
                {totalActive} running
              </span>
            )}
            {totalBlocked > 0 && (
              <span className="flex items-center gap-1 px-2 py-1 rounded-full bg-[hsl(var(--status-blocked-bg))] text-[hsl(var(--status-blocked))]">
                <AlertCircle className="h-3.5 w-3.5" />
                {totalBlocked} blocked
              </span>
            )}
          </div>
        </div>
        <Button onClick={onNewTask} className="gap-2 shadow-lg hover:shadow-xl transition-shadow">
          <Plus className="h-4 w-4" />
          New Task
        </Button>
      </div>

      {/* Kanban Board */}
      <motion.div
        className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4 h-[calc(100vh-180px)]"
        variants={containerVariants}
        initial="hidden"
        animate="visible"
      >
        {columns.map((column) => {
          const columnTasks = tasksByColumn[column.id] || [];
          const Icon = column.icon;
          const hasActive = column.id === 'in_progress' && columnTasks.some(t => t.status === 'processing');

          return (
            <motion.div
              key={column.id}
              variants={columnVariants}
              className={cn(
                'flex flex-col rounded-xl border bg-card/50 overflow-hidden',
                hasActive && 'ring-2 ring-primary/20'
              )}
            >
              {/* Column Header */}
              <div
                className="flex items-center justify-between px-4 py-3 border-b"
                style={{
                  borderBottomColor: columnTasks.length > 0 ? column.accentColor : undefined,
                  borderBottomWidth: columnTasks.length > 0 ? '2px' : '1px',
                }}
              >
                <div className="flex items-center gap-2">
                  <Icon
                    className="h-4 w-4"
                    style={{ color: column.accentColor }}
                  />
                  <h2 className="font-semibold text-sm">{column.title}</h2>
                </div>
                <motion.span
                  key={columnTasks.length}
                  initial={{ scale: 1.2 }}
                  animate={{ scale: 1 }}
                  className={cn(
                    'text-xs font-medium px-2 py-0.5 rounded-full',
                    columnTasks.length > 0
                      ? 'bg-primary/10 text-primary'
                      : 'bg-muted text-muted-foreground'
                  )}
                >
                  {columnTasks.length}
                </motion.span>
              </div>

              {/* Column Content */}
              <div className="flex-1 overflow-y-auto p-3 space-y-2 scrollbar-thin">
                <AnimatePresence mode="popLayout">
                  {columnTasks.map((task) => (
                    <TaskCard
                      key={task.id}
                      task={task}
                      onQueue={onQueue}
                      onRetry={onRetry}
                      onClose={onClose}
                      onClick={onTaskClick}
                    />
                  ))}
                </AnimatePresence>

                {columnTasks.length === 0 && (
                  <motion.div
                    initial={{ opacity: 0 }}
                    animate={{ opacity: 1 }}
                    className="flex flex-col items-center justify-center py-12 text-muted-foreground"
                  >
                    <Icon className="h-8 w-8 mb-2 opacity-30" />
                    <p className="text-sm">{column.emptyMessage}</p>
                  </motion.div>
                )}
              </div>

              {/* Quick add button for backlog */}
              {column.id === 'backlog' && (
                <div className="p-3 border-t">
                  <Button
                    variant="ghost"
                    className="w-full justify-start text-muted-foreground hover:text-foreground gap-2"
                    onClick={onNewTask}
                  >
                    <Plus className="h-4 w-4" />
                    Add task
                  </Button>
                </div>
              )}
            </motion.div>
          );
        })}
      </motion.div>
    </div>
  );
}
