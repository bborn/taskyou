import { useState } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { X, Sparkles, Folder, Tag, FileText } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import type { CreateTaskRequest, Project } from '@/api/types';
import { cn } from '@/lib/utils';

interface NewTaskDialogProps {
  projects: Project[];
  onSubmit: (data: CreateTaskRequest) => Promise<void>;
  onClose: () => void;
}

const taskTypes = [
  { value: 'code', label: 'Code', description: 'Write or modify code' },
  { value: 'writing', label: 'Writing', description: 'Documentation, content' },
  { value: 'thinking', label: 'Thinking', description: 'Research, analysis' },
];

export function NewTaskDialog({ projects, onSubmit, onClose }: NewTaskDialogProps) {
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [type, setType] = useState('code');
  const [project, setProject] = useState('personal');
  const [submitting, setSubmitting] = useState(false);
  const [startImmediately, setStartImmediately] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!title.trim()) return;

    setSubmitting(true);
    try {
      await onSubmit({
        title: title.trim(),
        body: body.trim() || undefined,
        type,
        project,
      });
      onClose();
    } catch (error) {
      console.error('Failed to create task:', error);
    } finally {
      setSubmitting(false);
    }
  };

  // Keyboard shortcut to close
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      onClose();
    }
  };

  return (
    <AnimatePresence>
      <motion.div
        className="fixed inset-0 z-50 flex items-center justify-center p-4"
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        exit={{ opacity: 0 }}
        onKeyDown={handleKeyDown}
      >
        {/* Backdrop */}
        <motion.div
          className="absolute inset-0 bg-black/60 backdrop-blur-sm"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          onClick={onClose}
        />

        {/* Dialog */}
        <motion.div
          className="relative w-full max-w-lg bg-card rounded-xl shadow-2xl border border-border overflow-hidden"
          initial={{ opacity: 0, scale: 0.95, y: 20 }}
          animate={{ opacity: 1, scale: 1, y: 0 }}
          exit={{ opacity: 0, scale: 0.95, y: 20 }}
          transition={{ type: 'spring', damping: 25, stiffness: 300 }}
        >
          <form onSubmit={handleSubmit}>
            {/* Header */}
            <div className="flex items-center justify-between px-6 py-4 border-b border-border">
              <div className="flex items-center gap-2">
                <div className="p-2 rounded-lg bg-primary/10">
                  <Sparkles className="h-5 w-5 text-primary" />
                </div>
                <div>
                  <h2 className="font-semibold">New Task</h2>
                  <p className="text-xs text-muted-foreground">What would you like AI to do?</p>
                </div>
              </div>
              <Button variant="ghost" size="icon" onClick={onClose} type="button">
                <X className="h-4 w-4" />
              </Button>
            </div>

            {/* Content */}
            <div className="p-6 space-y-5">
              {/* Title */}
              <div>
                <label className="flex items-center gap-2 text-sm font-medium mb-2">
                  <FileText className="h-4 w-4 text-muted-foreground" />
                  What needs to be done?
                </label>
                <Input
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  placeholder="e.g., Fix the login bug, Add dark mode support..."
                  className="text-base"
                  autoFocus
                />
              </div>

              {/* Description */}
              <div>
                <label className="text-sm font-medium mb-2 block text-muted-foreground">
                  Additional details (optional)
                </label>
                <Textarea
                  value={body}
                  onChange={(e) => setBody(e.target.value)}
                  placeholder="Provide context, requirements, or specific instructions..."
                  rows={3}
                  className="resize-none"
                />
              </div>

              {/* Type Selection */}
              <div>
                <label className="flex items-center gap-2 text-sm font-medium mb-2">
                  <Tag className="h-4 w-4 text-muted-foreground" />
                  Task type
                </label>
                <div className="grid grid-cols-3 gap-2">
                  {taskTypes.map((t) => (
                    <button
                      key={t.value}
                      type="button"
                      onClick={() => setType(t.value)}
                      className={cn(
                        'flex flex-col items-center gap-1 p-3 rounded-lg border-2 transition-all',
                        type === t.value
                          ? 'border-primary bg-primary/5 text-primary'
                          : 'border-transparent bg-muted/50 hover:bg-muted text-muted-foreground hover:text-foreground'
                      )}
                    >
                      <span className="font-medium text-sm">{t.label}</span>
                      <span className="text-[10px] opacity-70">{t.description}</span>
                    </button>
                  ))}
                </div>
              </div>

              {/* Project Selection */}
              <div>
                <label className="flex items-center gap-2 text-sm font-medium mb-2">
                  <Folder className="h-4 w-4 text-muted-foreground" />
                  Project
                </label>
                <select
                  value={project}
                  onChange={(e) => setProject(e.target.value)}
                  className="w-full h-10 rounded-lg border border-input bg-background px-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
                >
                  <option value="personal">Personal</option>
                  {projects.map((p) => (
                    <option key={p.id} value={p.name}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            {/* Footer */}
            <div className="flex items-center justify-between px-6 py-4 border-t border-border bg-muted/30">
              <label className="flex items-center gap-2 text-sm text-muted-foreground cursor-pointer">
                <input
                  type="checkbox"
                  checked={startImmediately}
                  onChange={(e) => setStartImmediately(e.target.checked)}
                  className="rounded border-input"
                />
                Start immediately
              </label>
              <div className="flex gap-2">
                <Button type="button" variant="ghost" onClick={onClose}>
                  Cancel
                </Button>
                <Button
                  type="submit"
                  disabled={!title.trim() || submitting}
                  className="gap-2 min-w-[100px]"
                >
                  {submitting ? (
                    <span className="flex items-center gap-2">
                      <span className="h-4 w-4 border-2 border-current border-t-transparent rounded-full animate-spin" />
                      Creating...
                    </span>
                  ) : (
                    'Create Task'
                  )}
                </Button>
              </div>
            </div>
          </form>
        </motion.div>
      </motion.div>
    </AnimatePresence>
  );
}
