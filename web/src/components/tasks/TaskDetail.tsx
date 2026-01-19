import { useState, useEffect, useRef, useCallback } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import {
  X,
  Play,
  RotateCcw,
  CheckCircle,
  Trash2,
  Edit3,
  Save,
  Shield,
  ShieldAlert,
  GitPullRequest,
  GitBranch,
  Clock,
  Calendar,
  Zap,
  AlertCircle,
  Terminal,
  ChevronDown,
  ChevronUp,
  MessageSquare,
  ExternalLink,
} from 'lucide-react';
import { tasks } from '@/api/client';
import type { Task, TaskLog, TaskStatus } from '@/api/types';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { cn } from '@/lib/utils';

interface TaskDetailProps {
  task: Task;
  onClose: () => void;
  onUpdate: (task: Task) => void;
  onDelete?: () => void;
}

const statusConfig: Record<TaskStatus, {
  icon: React.ElementType;
  label: string;
  className: string;
  bgClass: string;
}> = {
  backlog: {
    icon: Clock,
    label: 'Backlog',
    className: 'text-[hsl(var(--status-backlog))]',
    bgClass: 'bg-[hsl(var(--status-backlog))]',
  },
  queued: {
    icon: Clock,
    label: 'Queued',
    className: 'text-[hsl(var(--status-queued))]',
    bgClass: 'bg-[hsl(var(--status-queued))]',
  },
  processing: {
    icon: Zap,
    label: 'Running',
    className: 'text-[hsl(var(--status-processing))]',
    bgClass: 'bg-[hsl(var(--status-processing))]',
  },
  blocked: {
    icon: AlertCircle,
    label: 'Blocked',
    className: 'text-[hsl(var(--status-blocked))]',
    bgClass: 'bg-[hsl(var(--status-blocked))]',
  },
  done: {
    icon: CheckCircle,
    label: 'Done',
    className: 'text-[hsl(var(--status-done))]',
    bgClass: 'bg-[hsl(var(--status-done))]',
  },
};

const logTypeColors: Record<string, string> = {
  error: 'text-red-400',
  system: 'text-yellow-400',
  tool: 'text-cyan-400',
  output: 'text-green-400',
  text: 'text-gray-300',
};

export function TaskDetail({ task: initialTask, onClose, onUpdate, onDelete }: TaskDetailProps) {
  const [task, setTask] = useState(initialTask);
  const [logs, setLogs] = useState<TaskLog[]>([]);
  const [loading, setLoading] = useState(false);
  const [actionLoading, setActionLoading] = useState<string | null>(null);
  const [isEditing, setIsEditing] = useState(false);
  const [editForm, setEditForm] = useState({
    title: task.title,
    body: task.body || '',
  });
  const [showRetryDialog, setShowRetryDialog] = useState(false);
  const [retryFeedback, setRetryFeedback] = useState('');
  const [logsExpanded, setLogsExpanded] = useState(true);
  const logsEndRef = useRef<HTMLDivElement>(null);
  const pollIntervalRef = useRef<number | undefined>(undefined);

  // Update local task when prop changes
  useEffect(() => {
    setTask(initialTask);
    setEditForm({ title: initialTask.title, body: initialTask.body || '' });
  }, [initialTask]);

  // Fetch logs
  const fetchLogs = useCallback(async () => {
    try {
      const taskLogs = await tasks.getLogs(task.id, 200);
      setLogs(taskLogs.reverse());
    } catch (err) {
      console.error('Failed to fetch logs:', err);
    }
  }, [task.id]);

  useEffect(() => {
    setLoading(true);
    fetchLogs().finally(() => setLoading(false));

    // Poll for new logs while task is processing
    if (task.status === 'processing' || task.status === 'queued') {
      pollIntervalRef.current = window.setInterval(fetchLogs, 1500);
    }

    return () => {
      if (pollIntervalRef.current) {
        clearInterval(pollIntervalRef.current);
      }
    };
  }, [task.id, task.status, fetchLogs]);

  // Scroll to bottom when logs update
  useEffect(() => {
    if (logsExpanded) {
      logsEndRef.current?.scrollIntoView({ behavior: 'smooth' });
    }
  }, [logs, logsExpanded]);

  // Keyboard shortcut to close
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !isEditing && !showRetryDialog) {
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [onClose, isEditing, showRetryDialog]);

  const handleQueue = async () => {
    setActionLoading('queue');
    try {
      const updated = await tasks.queue(task.id);
      setTask(updated);
      onUpdate(updated);
    } catch (err) {
      console.error('Failed to queue task:', err);
    } finally {
      setActionLoading(null);
    }
  };

  const handleRetry = async () => {
    setActionLoading('retry');
    try {
      const updated = await tasks.retry(task.id, retryFeedback || undefined);
      setTask(updated);
      onUpdate(updated);
      setShowRetryDialog(false);
      setRetryFeedback('');
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
      setTask(updated);
      onUpdate(updated);
    } catch (err) {
      console.error('Failed to close task:', err);
    } finally {
      setActionLoading(null);
    }
  };

  const handleDelete = async () => {
    if (!window.confirm('Are you sure you want to delete this task? This cannot be undone.')) return;

    setActionLoading('delete');
    try {
      await tasks.delete(task.id);
      onDelete?.();
      onClose();
    } catch (err) {
      console.error('Failed to delete task:', err);
    } finally {
      setActionLoading(null);
    }
  };

  const handleSaveEdit = async () => {
    if (!editForm.title.trim()) return;

    setActionLoading('save');
    try {
      const updated = await tasks.update(task.id, {
        title: editForm.title.trim(),
        body: editForm.body.trim(),
      });
      setTask(updated);
      onUpdate(updated);
      setIsEditing(false);
    } catch (err) {
      console.error('Failed to update task:', err);
    } finally {
      setActionLoading(null);
    }
  };

  const handleToggleDangerousMode = async () => {
    // This requires an API endpoint - for now just show the current state
    // In a real implementation, this would call an API to toggle the mode
    alert('Dangerous mode toggle requires the task to be running. Use the TUI for now.');
  };

  const formatDate = (date: string) => {
    return new Date(date).toLocaleString(undefined, {
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
    });
  };

  const formatDuration = (start: string, end?: string) => {
    const startDate = new Date(start);
    const endDate = end ? new Date(end) : new Date();
    const diffMs = endDate.getTime() - startDate.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    const diffHours = Math.floor(diffMins / 60);

    if (diffHours > 0) {
      return `${diffHours}h ${diffMins % 60}m`;
    }
    return `${diffMins}m`;
  };

  const config = statusConfig[task.status];
  const StatusIcon = config.icon;

  return (
    <AnimatePresence>
      <motion.div
        className="fixed inset-0 z-50 flex"
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        exit={{ opacity: 0 }}
      >
        {/* Backdrop */}
        <motion.div
          className="absolute inset-0 bg-black/60 backdrop-blur-sm"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          onClick={onClose}
        />

        {/* Panel */}
        <motion.div
          className="relative ml-auto w-full max-w-2xl bg-background border-l border-border flex flex-col h-full shadow-2xl"
          initial={{ x: '100%' }}
          animate={{ x: 0 }}
          exit={{ x: '100%' }}
          transition={{ type: 'spring', damping: 30, stiffness: 300 }}
        >
          {/* Header */}
          <div className="flex items-start gap-4 p-5 border-b border-border">
            <div className="flex-1 min-w-0">
              {isEditing ? (
                <Input
                  value={editForm.title}
                  onChange={(e) => setEditForm(f => ({ ...f, title: e.target.value }))}
                  className="text-xl font-semibold mb-2"
                  autoFocus
                />
              ) : (
                <h2 className="text-xl font-semibold mb-2 pr-8">{task.title}</h2>
              )}

              <div className="flex flex-wrap items-center gap-2">
                <Badge className={cn('gap-1', config.bgClass, 'text-white')}>
                  <StatusIcon className="h-3 w-3" />
                  {config.label}
                </Badge>
                {task.project && task.project !== 'personal' && (
                  <Badge variant="outline">{task.project}</Badge>
                )}
                {task.type && (
                  <Badge variant="secondary">{task.type}</Badge>
                )}
                {task.dangerous_mode && (
                  <Badge variant="destructive" className="gap-1">
                    <ShieldAlert className="h-3 w-3" />
                    Dangerous
                  </Badge>
                )}
              </div>
            </div>

            <div className="flex items-center gap-1">
              {!isEditing && (
                <Button
                  variant="ghost"
                  size="icon"
                  className="h-8 w-8"
                  onClick={() => setIsEditing(true)}
                >
                  <Edit3 className="h-4 w-4" />
                </Button>
              )}
              <Button variant="ghost" size="icon" className="h-8 w-8" onClick={onClose}>
                <X className="h-4 w-4" />
              </Button>
            </div>
          </div>

          {/* Body */}
          <div className="flex-1 overflow-y-auto scrollbar-thin">
            {/* Description */}
            <div className="p-5 border-b border-border">
              {isEditing ? (
                <div className="space-y-3">
                  <Textarea
                    value={editForm.body}
                    onChange={(e) => setEditForm(f => ({ ...f, body: e.target.value }))}
                    placeholder="Task description..."
                    rows={4}
                    className="font-mono text-sm"
                  />
                  <div className="flex justify-end gap-2">
                    <Button variant="ghost" size="sm" onClick={() => setIsEditing(false)}>
                      Cancel
                    </Button>
                    <Button
                      size="sm"
                      onClick={handleSaveEdit}
                      disabled={actionLoading === 'save' || !editForm.title.trim()}
                    >
                      <Save className="h-3.5 w-3.5 mr-1" />
                      Save
                    </Button>
                  </div>
                </div>
              ) : task.body ? (
                <pre className="text-sm text-muted-foreground whitespace-pre-wrap font-mono">
                  {task.body}
                </pre>
              ) : (
                <p className="text-sm text-muted-foreground italic">No description</p>
              )}
            </div>

            {/* Metadata */}
            <div className="p-5 border-b border-border">
              <div className="grid grid-cols-2 gap-4 text-sm">
                <div className="flex items-center gap-2 text-muted-foreground">
                  <Calendar className="h-4 w-4" />
                  <span>Created {formatDate(task.created_at)}</span>
                </div>
                {task.started_at && (
                  <div className="flex items-center gap-2 text-muted-foreground">
                    <Clock className="h-4 w-4" />
                    <span>
                      {task.completed_at
                        ? `Took ${formatDuration(task.started_at, task.completed_at)}`
                        : `Running for ${formatDuration(task.started_at)}`
                      }
                    </span>
                  </div>
                )}
                {task.branch_name && (
                  <div className="flex items-center gap-2 text-muted-foreground col-span-2">
                    <GitBranch className="h-4 w-4" />
                    <code className="text-xs bg-muted px-2 py-0.5 rounded">{task.branch_name}</code>
                  </div>
                )}
                {task.pr_url && (
                  <div className="flex items-center gap-2 col-span-2">
                    <GitPullRequest className="h-4 w-4 text-muted-foreground" />
                    <a
                      href={task.pr_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="text-sm text-blue-500 hover:text-blue-600 hover:underline flex items-center gap-1"
                    >
                      Pull Request #{task.pr_number}
                      <ExternalLink className="h-3 w-3" />
                    </a>
                  </div>
                )}
              </div>
            </div>

            {/* Actions */}
            <div className="p-5 border-b border-border">
              <div className="flex flex-wrap gap-2">
                {task.status === 'backlog' && (
                  <Button
                    onClick={handleQueue}
                    disabled={actionLoading !== null}
                    className="gap-2"
                  >
                    <Play className="h-4 w-4" />
                    {actionLoading === 'queue' ? 'Starting...' : 'Run Task'}
                  </Button>
                )}

                {task.status === 'blocked' && (
                  <Button
                    onClick={() => setShowRetryDialog(true)}
                    disabled={actionLoading !== null}
                    className="gap-2 bg-orange-500 hover:bg-orange-600"
                  >
                    <RotateCcw className="h-4 w-4" />
                    Retry with Feedback
                  </Button>
                )}

                {task.status === 'done' && (
                  <Button
                    variant="secondary"
                    onClick={() => setShowRetryDialog(true)}
                    disabled={actionLoading !== null}
                    className="gap-2"
                  >
                    <RotateCcw className="h-4 w-4" />
                    Run Again
                  </Button>
                )}

                {(task.status === 'processing' || task.status === 'blocked' || task.status === 'queued') && (
                  <Button
                    variant="secondary"
                    onClick={handleClose}
                    disabled={actionLoading !== null}
                    className="gap-2"
                  >
                    <CheckCircle className="h-4 w-4" />
                    {actionLoading === 'close' ? 'Closing...' : 'Mark Done'}
                  </Button>
                )}

                {(task.status === 'processing' || task.status === 'blocked') && (
                  <Button
                    variant="outline"
                    onClick={handleToggleDangerousMode}
                    className={cn(
                      'gap-2',
                      task.dangerous_mode
                        ? 'text-green-500 hover:text-green-600'
                        : 'text-orange-500 hover:text-orange-600'
                    )}
                  >
                    {task.dangerous_mode ? (
                      <>
                        <Shield className="h-4 w-4" />
                        Switch to Safe
                      </>
                    ) : (
                      <>
                        <ShieldAlert className="h-4 w-4" />
                        Switch to Dangerous
                      </>
                    )}
                  </Button>
                )}

                <Button
                  variant="ghost"
                  onClick={handleDelete}
                  disabled={actionLoading !== null}
                  className="gap-2 text-destructive hover:text-destructive hover:bg-destructive/10 ml-auto"
                >
                  <Trash2 className="h-4 w-4" />
                  Delete
                </Button>
              </div>
            </div>

            {/* Logs Section */}
            <div className="flex flex-col">
              <button
                onClick={() => setLogsExpanded(!logsExpanded)}
                className="flex items-center justify-between px-5 py-3 hover:bg-muted/50 transition-colors"
              >
                <div className="flex items-center gap-2">
                  <Terminal className="h-4 w-4 text-muted-foreground" />
                  <span className="font-medium text-sm">Execution Logs</span>
                  {logs.length > 0 && (
                    <Badge variant="secondary" className="text-xs">
                      {logs.length}
                    </Badge>
                  )}
                </div>
                {logsExpanded ? (
                  <ChevronUp className="h-4 w-4 text-muted-foreground" />
                ) : (
                  <ChevronDown className="h-4 w-4 text-muted-foreground" />
                )}
              </button>

              <AnimatePresence>
                {logsExpanded && (
                  <motion.div
                    initial={{ height: 0 }}
                    animate={{ height: 'auto' }}
                    exit={{ height: 0 }}
                    className="overflow-hidden"
                  >
                    <div className="bg-gray-950 p-4 font-mono text-xs max-h-[400px] overflow-y-auto scrollbar-thin">
                      {loading ? (
                        <div className="text-muted-foreground animate-pulse">Loading logs...</div>
                      ) : logs.length === 0 ? (
                        <div className="text-muted-foreground">No logs yet. Run the task to see output.</div>
                      ) : (
                        logs.map((log) => (
                          <div
                            key={log.id}
                            className={cn('py-0.5 leading-relaxed', logTypeColors[log.line_type] || 'text-gray-300')}
                          >
                            {log.content}
                          </div>
                        ))
                      )}
                      <div ref={logsEndRef} />
                    </div>
                  </motion.div>
                )}
              </AnimatePresence>
            </div>
          </div>

          {/* Retry Dialog */}
          <AnimatePresence>
            {showRetryDialog && (
              <motion.div
                className="absolute inset-0 bg-black/50 flex items-center justify-center p-6"
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                exit={{ opacity: 0 }}
              >
                <motion.div
                  className="bg-card rounded-lg shadow-xl w-full max-w-md p-6"
                  initial={{ scale: 0.95, opacity: 0 }}
                  animate={{ scale: 1, opacity: 1 }}
                  exit={{ scale: 0.95, opacity: 0 }}
                >
                  <div className="flex items-center gap-2 mb-4">
                    <MessageSquare className="h-5 w-5 text-primary" />
                    <h3 className="font-semibold">Add Feedback</h3>
                  </div>
                  <p className="text-sm text-muted-foreground mb-4">
                    Provide additional context or instructions for the AI to consider when retrying this task.
                  </p>
                  <Textarea
                    value={retryFeedback}
                    onChange={(e) => setRetryFeedback(e.target.value)}
                    placeholder="e.g., 'Please also update the tests' or 'Use the existing helper function instead'"
                    rows={4}
                    className="mb-4"
                    autoFocus
                  />
                  <div className="flex justify-end gap-2">
                    <Button variant="ghost" onClick={() => {
                      setShowRetryDialog(false);
                      setRetryFeedback('');
                    }}>
                      Cancel
                    </Button>
                    <Button
                      onClick={handleRetry}
                      disabled={actionLoading === 'retry'}
                    >
                      {actionLoading === 'retry' ? 'Retrying...' : 'Retry Task'}
                    </Button>
                  </div>
                </motion.div>
              </motion.div>
            )}
          </AnimatePresence>
        </motion.div>
      </motion.div>
    </AnimatePresence>
  );
}
