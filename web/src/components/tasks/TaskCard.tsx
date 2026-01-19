import { motion } from 'framer-motion';
import {
  Play,
  RotateCcw,
  CheckCircle,
  Clock,
  Zap,
  AlertCircle,
  GitPullRequest,
  ShieldAlert,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import type { Task, TaskStatus } from '@/api/types';
import { cn } from '@/lib/utils';

interface TaskCardProps {
  task: Task;
  onQueue: (id: number) => void;
  onRetry: (id: number) => void;
  onClose: (id: number) => void;
  onClick: (task: Task) => void;
}

const statusConfig: Record<TaskStatus, {
  icon: React.ElementType;
  label: string;
  className: string;
  borderClass: string;
  bgClass: string;
  animate?: boolean;
}> = {
  backlog: {
    icon: Clock,
    label: 'Backlog',
    className: 'text-[hsl(var(--status-backlog))]',
    borderClass: 'border-status-backlog',
    bgClass: 'bg-[hsl(var(--status-backlog-bg))]',
  },
  queued: {
    icon: Clock,
    label: 'Queued',
    className: 'text-[hsl(var(--status-queued))]',
    borderClass: 'border-status-queued',
    bgClass: 'bg-[hsl(var(--status-queued-bg))]',
    animate: true,
  },
  processing: {
    icon: Zap,
    label: 'Running',
    className: 'text-[hsl(var(--status-processing))]',
    borderClass: 'border-status-processing',
    bgClass: 'bg-[hsl(var(--status-processing-bg))]',
    animate: true,
  },
  blocked: {
    icon: AlertCircle,
    label: 'Blocked',
    className: 'text-[hsl(var(--status-blocked))]',
    borderClass: 'border-status-blocked',
    bgClass: 'bg-[hsl(var(--status-blocked-bg))]',
  },
  done: {
    icon: CheckCircle,
    label: 'Done',
    className: 'text-[hsl(var(--status-done))]',
    borderClass: 'border-status-done',
    bgClass: 'bg-[hsl(var(--status-done-bg))]',
  },
};

export function TaskCard({ task, onQueue, onRetry, onClose, onClick }: TaskCardProps) {
  const config = statusConfig[task.status];
  const StatusIcon = config.icon;

  const handleAction = (e: React.MouseEvent, action: () => void) => {
    e.stopPropagation();
    action();
  };

  const isActive = task.status === 'processing' || task.status === 'queued';

  return (
    <motion.div
      layout
      initial={{ opacity: 0, y: 10 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, y: -10, transition: { duration: 0.15 } }}
      whileHover={{ scale: 1.01 }}
      whileTap={{ scale: 0.99 }}
      transition={{ duration: 0.2 }}
      onClick={() => onClick(task)}
      className={cn(
        'group cursor-pointer rounded-lg border bg-card p-4 transition-all duration-200',
        'hover:bg-accent/50 hover:shadow-md',
        config.borderClass,
        isActive && 'glow-primary'
      )}
    >
      {/* Header with status and project */}
      <div className="flex items-start justify-between gap-2 mb-2">
        <div className="flex items-center gap-2 min-w-0">
          <motion.div
            animate={config.animate ? { scale: [1, 1.1, 1] } : {}}
            transition={{ repeat: Infinity, duration: 2 }}
            className={cn('shrink-0', config.className)}
          >
            <StatusIcon className="h-4 w-4" />
          </motion.div>
          <span className={cn('text-xs font-medium', config.className)}>
            {config.label}
          </span>
        </div>
        <div className="flex items-center gap-1.5 shrink-0">
          {task.dangerous_mode && (
            <span title="Dangerous mode">
              <ShieldAlert className="h-3.5 w-3.5 text-orange-500" />
            </span>
          )}
          {task.pr_url && (
            <a
              href={task.pr_url}
              target="_blank"
              rel="noopener noreferrer"
              onClick={(e) => e.stopPropagation()}
              className="flex items-center gap-1 text-xs text-blue-500 hover:text-blue-600 hover:underline"
            >
              <GitPullRequest className="h-3 w-3" />
              <span>#{task.pr_number}</span>
            </a>
          )}
        </div>
      </div>

      {/* Title */}
      <h3 className="font-medium text-sm leading-snug line-clamp-2 mb-2 group-hover:text-primary transition-colors">
        {task.title}
      </h3>

      {/* Description preview */}
      {task.body && (
        <p className="text-xs text-muted-foreground line-clamp-2 mb-3">
          {task.body}
        </p>
      )}

      {/* Tags row */}
      <div className="flex flex-wrap items-center gap-1.5 mb-3">
        {task.project && task.project !== 'personal' && (
          <Badge variant="outline" className="text-[10px] px-1.5 py-0 h-5">
            {task.project}
          </Badge>
        )}
        {task.type && (
          <Badge variant="secondary" className="text-[10px] px-1.5 py-0 h-5">
            {task.type}
          </Badge>
        )}
      </div>

      {/* Action buttons */}
      <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
        {task.status === 'backlog' && (
          <Button
            variant="ghost"
            size="sm"
            className="h-7 px-2 text-xs gap-1 hover:bg-primary hover:text-primary-foreground"
            onClick={(e) => handleAction(e, () => onQueue(task.id))}
          >
            <Play className="h-3 w-3" />
            Run
          </Button>
        )}
        {task.status === 'blocked' && (
          <Button
            variant="ghost"
            size="sm"
            className="h-7 px-2 text-xs gap-1 hover:bg-orange-500 hover:text-white"
            onClick={(e) => handleAction(e, () => onRetry(task.id))}
          >
            <RotateCcw className="h-3 w-3" />
            Retry
          </Button>
        )}
        {(task.status === 'processing' || task.status === 'blocked') && (
          <Button
            variant="ghost"
            size="sm"
            className="h-7 px-2 text-xs gap-1 hover:bg-green-500 hover:text-white"
            onClick={(e) => handleAction(e, () => onClose(task.id))}
          >
            <CheckCircle className="h-3 w-3" />
            Done
          </Button>
        )}
      </div>

      {/* Processing indicator */}
      {task.status === 'processing' && (
        <motion.div
          className="absolute bottom-0 left-0 right-0 h-0.5 bg-gradient-to-r from-primary via-primary/50 to-primary rounded-b-lg"
          animate={{ backgroundPosition: ['0% 50%', '100% 50%', '0% 50%'] }}
          transition={{ duration: 2, repeat: Infinity, ease: 'linear' }}
          style={{ backgroundSize: '200% 200%' }}
        />
      )}
    </motion.div>
  );
}
