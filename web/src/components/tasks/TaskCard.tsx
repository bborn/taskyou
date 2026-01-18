import { Play, RotateCcw, CheckCircle, MoreHorizontal } from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import type { Task, TaskStatus } from '@/api/types';

interface TaskCardProps {
  task: Task;
  onQueue: (id: number) => void;
  onRetry: (id: number) => void;
  onClose: (id: number) => void;
  onClick: (task: Task) => void;
}

function getStatusVariant(status: TaskStatus) {
  switch (status) {
    case 'backlog':
      return 'backlog';
    case 'queued':
      return 'queued';
    case 'processing':
      return 'processing';
    case 'blocked':
      return 'blocked';
    case 'done':
      return 'done';
    default:
      return 'secondary';
  }
}

function getStatusLabel(status: TaskStatus) {
  switch (status) {
    case 'backlog':
      return 'Backlog';
    case 'queued':
      return 'Queued';
    case 'processing':
      return 'Processing';
    case 'blocked':
      return 'Blocked';
    case 'done':
      return 'Done';
    default:
      return status;
  }
}

export function TaskCard({ task, onQueue, onRetry, onClose, onClick }: TaskCardProps) {
  const handleAction = (e: React.MouseEvent, action: () => void) => {
    e.stopPropagation();
    action();
  };

  return (
    <Card
      className="cursor-pointer hover:bg-accent/50 transition-colors"
      onClick={() => onClick(task)}
    >
      <CardHeader className="p-4 pb-2">
        <div className="flex items-start justify-between gap-2">
          <CardTitle className="text-sm font-medium line-clamp-2">
            {task.title}
          </CardTitle>
          <Button
            variant="ghost"
            size="icon"
            className="h-6 w-6 shrink-0"
            onClick={(e) => e.stopPropagation()}
          >
            <MoreHorizontal className="h-4 w-4" />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="p-4 pt-0">
        <div className="flex flex-wrap items-center gap-2 mb-3">
          <Badge variant={getStatusVariant(task.status)}>
            {getStatusLabel(task.status)}
          </Badge>
          {task.project && task.project !== 'personal' && (
            <Badge variant="outline" className="text-xs">
              {task.project}
            </Badge>
          )}
          {task.type && (
            <Badge variant="secondary" className="text-xs">
              {task.type}
            </Badge>
          )}
        </div>

        {task.body && (
          <p className="text-xs text-muted-foreground line-clamp-2 mb-3">
            {task.body}
          </p>
        )}

        <div className="flex items-center gap-1">
          {task.status === 'backlog' && (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 text-xs"
              onClick={(e) => handleAction(e, () => onQueue(task.id))}
            >
              <Play className="h-3 w-3 mr-1" />
              Execute
            </Button>
          )}
          {task.status === 'blocked' && (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 text-xs"
              onClick={(e) => handleAction(e, () => onRetry(task.id))}
            >
              <RotateCcw className="h-3 w-3 mr-1" />
              Retry
            </Button>
          )}
          {(task.status === 'processing' || task.status === 'blocked') && (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 text-xs"
              onClick={(e) => handleAction(e, () => onClose(task.id))}
            >
              <CheckCircle className="h-3 w-3 mr-1" />
              Close
            </Button>
          )}
        </div>

        <div className="mt-2 flex items-center gap-2">
          {task.pr_url && (
            <a
              href={task.pr_url}
              target="_blank"
              rel="noopener noreferrer"
              className="text-xs text-blue-500 hover:underline"
              onClick={(e) => e.stopPropagation()}
            >
              PR #{task.pr_number}
            </a>
          )}
          {task.dangerous_mode && (
            <span className="text-xs text-orange-500" title="Dangerous mode enabled">
              ⚠️
            </span>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
