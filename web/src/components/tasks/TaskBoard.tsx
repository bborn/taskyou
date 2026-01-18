import { useMemo } from 'react';
import { Plus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { TaskCard } from './TaskCard';
import type { Task } from '@/api/types';

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
  statuses: string[];
}

const columns: Column[] = [
  { id: 'backlog', title: 'Backlog', statuses: ['backlog'] },
  { id: 'in_progress', title: 'In Progress', statuses: ['queued', 'processing'] },
  { id: 'blocked', title: 'Blocked', statuses: ['blocked'] },
  { id: 'done', title: 'Done', statuses: ['done'] },
];

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
      grouped[column.id] = tasks.filter(task =>
        column.statuses.includes(task.status)
      );
    }

    return grouped;
  }, [tasks]);

  return (
    <div className="h-full">
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">Tasks</h1>
        <Button onClick={onNewTask}>
          <Plus className="h-4 w-4 mr-2" />
          New Task
        </Button>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4 h-[calc(100vh-180px)]">
        {columns.map(column => (
          <div
            key={column.id}
            className="flex flex-col rounded-lg border bg-muted/30 p-3"
          >
            <div className="flex items-center justify-between mb-3">
              <h2 className="font-semibold text-sm">{column.title}</h2>
              <span className="text-xs text-muted-foreground bg-muted px-2 py-0.5 rounded-full">
                {tasksByColumn[column.id]?.length || 0}
              </span>
            </div>

            <div className="flex-1 overflow-y-auto space-y-2">
              {tasksByColumn[column.id]?.map(task => (
                <TaskCard
                  key={task.id}
                  task={task}
                  onQueue={onQueue}
                  onRetry={onRetry}
                  onClose={onClose}
                  onClick={onTaskClick}
                />
              ))}

              {tasksByColumn[column.id]?.length === 0 && (
                <p className="text-sm text-muted-foreground text-center py-8">
                  No tasks
                </p>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
