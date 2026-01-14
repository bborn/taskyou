import { useState, useEffect, useRef } from 'react';
import { tasks } from '@/api/client';
import type { Task, TaskLog } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { cn } from '@/lib/utils';

interface TaskDetailProps {
  task: Task;
  onClose: () => void;
  onUpdate: (task: Task) => void;
}

const statusColors: Record<string, string> = {
  backlog: 'bg-gray-500',
  queued: 'bg-yellow-500',
  processing: 'bg-blue-500',
  blocked: 'bg-red-500',
  done: 'bg-green-500',
};

export function TaskDetail({ task, onClose, onUpdate }: TaskDetailProps) {
  const [logs, setLogs] = useState<TaskLog[]>([]);
  const [loading, setLoading] = useState(false);
  const [actionLoading, setActionLoading] = useState<string | null>(null);
  const logsEndRef = useRef<HTMLDivElement>(null);
  const pollIntervalRef = useRef<number>();

  // Fetch logs
  const fetchLogs = async () => {
    try {
      const taskLogs = await tasks.getLogs(task.id, 200);
      // Logs come in DESC order, reverse for display
      setLogs(taskLogs.reverse());
    } catch (err) {
      console.error('Failed to fetch logs:', err);
    }
  };

  useEffect(() => {
    setLoading(true);
    fetchLogs().finally(() => setLoading(false));

    // Poll for new logs while task is processing
    if (task.status === 'processing' || task.status === 'queued') {
      pollIntervalRef.current = window.setInterval(fetchLogs, 2000);
    }

    return () => {
      if (pollIntervalRef.current) {
        clearInterval(pollIntervalRef.current);
      }
    };
  }, [task.id, task.status]);

  // Scroll to bottom when logs update
  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [logs]);

  const handleQueue = async () => {
    setActionLoading('queue');
    try {
      const updated = await tasks.queue(task.id);
      onUpdate(updated);
    } catch (err) {
      console.error('Failed to queue task:', err);
    } finally {
      setActionLoading(null);
    }
  };

  const handleRetry = async () => {
    const feedback = window.prompt('Add feedback for retry (optional):');
    if (feedback === null) return; // Cancelled

    setActionLoading('retry');
    try {
      const updated = await tasks.retry(task.id, feedback || undefined);
      onUpdate(updated);
    } catch (err) {
      console.error('Failed to retry task:', err);
    } finally {
      setActionLoading(null);
    }
  };

  const handleClose = async () => {
    setActionLoading('close');
    try {
      const updated = await tasks.close(task.id);
      onUpdate(updated);
    } catch (err) {
      console.error('Failed to close task:', err);
    } finally {
      setActionLoading(null);
    }
  };

  const handleDelete = async () => {
    if (!window.confirm('Are you sure you want to delete this task?')) return;

    setActionLoading('delete');
    try {
      await tasks.delete(task.id);
      onClose();
    } catch (err) {
      console.error('Failed to delete task:', err);
    } finally {
      setActionLoading(null);
    }
  };

  const formatDate = (date: string) => {
    return new Date(date).toLocaleString();
  };

  const getLogColor = (lineType: string) => {
    switch (lineType) {
      case 'error':
        return 'text-red-400';
      case 'system':
        return 'text-yellow-400';
      case 'tool':
        return 'text-cyan-400';
      default:
        return 'text-gray-300';
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex">
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-black/50"
        onClick={onClose}
      />

      {/* Panel */}
      <div className="relative ml-auto w-full max-w-2xl bg-background border-l border-border flex flex-col h-full">
        {/* Header */}
        <div className="flex items-start justify-between p-4 border-b border-border">
          <div className="flex-1 min-w-0 pr-4">
            <div className="flex items-center gap-2 mb-2">
              <Badge className={cn(statusColors[task.status], 'text-white')}>
                {task.status}
              </Badge>
              <Badge variant="outline">{task.type}</Badge>
              <Badge variant="outline">{task.project}</Badge>
            </div>
            <h2 className="text-xl font-semibold truncate">{task.title}</h2>
            {task.pr_url && (
              <a
                href={task.pr_url}
                target="_blank"
                rel="noopener noreferrer"
                className="text-sm text-blue-400 hover:underline"
              >
                PR #{task.pr_number}
              </a>
            )}
          </div>
          <Button variant="ghost" size="sm" onClick={onClose}>
            ✕
          </Button>
        </div>

        {/* Body */}
        {task.body && (
          <div className="p-4 border-b border-border">
            <pre className="text-sm text-muted-foreground whitespace-pre-wrap font-mono">
              {task.body}
            </pre>
          </div>
        )}

        {/* Meta */}
        <div className="p-4 border-b border-border text-sm text-muted-foreground grid grid-cols-2 gap-2">
          <div>Created: {formatDate(task.created_at)}</div>
          {task.started_at && <div>Started: {formatDate(task.started_at)}</div>}
          {task.completed_at && <div>Completed: {formatDate(task.completed_at)}</div>}
          {task.branch_name && <div className="col-span-2">Branch: {task.branch_name}</div>}
          {task.dangerous_mode && (
            <div className="col-span-2 text-orange-400">⚠️ Dangerous Mode Enabled</div>
          )}
        </div>

        {/* Actions */}
        <div className="p-4 border-b border-border flex gap-2 flex-wrap">
          {task.status === 'backlog' && (
            <Button
              size="sm"
              onClick={handleQueue}
              disabled={actionLoading !== null}
            >
              {actionLoading === 'queue' ? 'Queuing...' : 'Queue'}
            </Button>
          )}
          {(task.status === 'blocked' || task.status === 'done') && (
            <Button
              size="sm"
              variant="secondary"
              onClick={handleRetry}
              disabled={actionLoading !== null}
            >
              {actionLoading === 'retry' ? 'Retrying...' : 'Retry'}
            </Button>
          )}
          {task.status !== 'done' && (
            <Button
              size="sm"
              variant="secondary"
              onClick={handleClose}
              disabled={actionLoading !== null}
            >
              {actionLoading === 'close' ? 'Closing...' : 'Close'}
            </Button>
          )}
          <Button
            size="sm"
            variant="destructive"
            onClick={handleDelete}
            disabled={actionLoading !== null}
          >
            {actionLoading === 'delete' ? 'Deleting...' : 'Delete'}
          </Button>
        </div>

        {/* Logs */}
        <div className="flex-1 overflow-hidden flex flex-col">
          <div className="px-4 py-2 text-sm font-medium border-b border-border">
            Execution Logs
          </div>
          <div className="flex-1 overflow-y-auto p-4 bg-black/20 font-mono text-xs">
            {loading ? (
              <div className="text-muted-foreground">Loading logs...</div>
            ) : logs.length === 0 ? (
              <div className="text-muted-foreground">No logs yet</div>
            ) : (
              logs.map((log) => (
                <div key={log.id} className={cn('py-0.5', getLogColor(log.line_type))}>
                  {log.content}
                </div>
              ))
            )}
            <div ref={logsEndRef} />
          </div>
        </div>
      </div>
    </div>
  );
}
