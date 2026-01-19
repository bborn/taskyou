import { useState, useEffect, useCallback } from 'react';
import { motion } from 'framer-motion';
import { Header } from '@/components/layout/Header';
import { TaskBoard } from '@/components/tasks/TaskBoard';
import { TaskDetail } from '@/components/tasks/TaskDetail';
import { NewTaskDialog } from '@/components/tasks/NewTaskDialog';
import { CommandPalette } from '@/components/CommandPalette';
import { useTasks } from '@/hooks/useTasks';
import { projects as projectsApi } from '@/api/client';
import type { User, Task, Project } from '@/api/types';

interface DashboardPageProps {
  user: User;
  onLogout: () => void;
  onSettings: () => void;
}

export function DashboardPage({ user, onLogout, onSettings }: DashboardPageProps) {
  const { tasks, queueTask, closeTask, createTask, refresh } = useTasks();
  const [projects, setProjects] = useState<Project[]>([]);
  const [showNewTask, setShowNewTask] = useState(false);
  const [selectedTask, setSelectedTask] = useState<Task | null>(null);
  const [showCommandPalette, setShowCommandPalette] = useState(false);

  // Fetch projects
  useEffect(() => {
    projectsApi.list().then(setProjects).catch(console.error);
  }, []);

  // Global keyboard shortcuts
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Don't trigger shortcuts when typing in inputs
      const target = e.target as HTMLElement;
      if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable) {
        return;
      }

      // Command palette: Cmd/Ctrl + K
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        setShowCommandPalette((prev) => !prev);
        return;
      }

      // New task: N
      if (e.key === 'n' && !e.metaKey && !e.ctrlKey) {
        e.preventDefault();
        setShowNewTask(true);
        return;
      }

      // Refresh: R
      if (e.key === 'r' && !e.metaKey && !e.ctrlKey) {
        e.preventDefault();
        refresh();
        return;
      }

      // Settings: S (when no modal is open)
      if (e.key === 's' && !e.metaKey && !e.ctrlKey && !showNewTask && !selectedTask) {
        e.preventDefault();
        onSettings();
        return;
      }

      // Close modals: Escape
      if (e.key === 'Escape') {
        if (showCommandPalette) {
          setShowCommandPalette(false);
        } else if (showNewTask) {
          setShowNewTask(false);
        } else if (selectedTask) {
          setSelectedTask(null);
        }
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [showNewTask, selectedTask, showCommandPalette, refresh, onSettings]);

  const handleTaskClick = useCallback((task: Task) => {
    setSelectedTask(task);
  }, []);

  const handleTaskUpdate = useCallback((updatedTask: Task) => {
    setSelectedTask(updatedTask);
  }, []);

  const handleCreateTask = useCallback(async (data: Parameters<typeof createTask>[0]) => {
    await createTask(data);
  }, [createTask]);

  const handleDeleteTask = useCallback(() => {
    // Task was deleted, just close the panel
    setSelectedTask(null);
  }, []);

  const handleRetry = useCallback(async (id: number) => {
    // Find the task to open detail panel
    const task = tasks.find(t => t.id === id);
    if (task) {
      setSelectedTask(task);
    }
  }, [tasks]);

  return (
    <div className="min-h-screen bg-background">
      <Header
        user={user}
        onLogout={onLogout}
        onSettings={onSettings}
        onCommandPalette={() => setShowCommandPalette(true)}
      />

      <main className="container mx-auto p-4 md:p-6">
        <motion.div
          initial={{ opacity: 0, y: 20 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.3 }}
        >
          <TaskBoard
            tasks={tasks}
            onQueue={queueTask}
            onRetry={handleRetry}
            onClose={closeTask}
            onTaskClick={handleTaskClick}
            onNewTask={() => setShowNewTask(true)}
          />
        </motion.div>
      </main>

      {/* Keyboard shortcuts hint */}
      <div className="fixed bottom-4 right-4 hidden lg:flex items-center gap-4 text-xs text-muted-foreground">
        <span className="flex items-center gap-1">
          <kbd className="px-1.5 py-0.5 rounded bg-muted border border-border">N</kbd>
          new task
        </span>
        <span className="flex items-center gap-1">
          <kbd className="px-1.5 py-0.5 rounded bg-muted border border-border">R</kbd>
          refresh
        </span>
        <span className="flex items-center gap-1">
          <kbd className="px-1.5 py-0.5 rounded bg-muted border border-border">S</kbd>
          settings
        </span>
      </div>

      {/* Command Palette */}
      <CommandPalette
        isOpen={showCommandPalette}
        onClose={() => setShowCommandPalette(false)}
        tasks={tasks}
        onSelectTask={(task) => setSelectedTask(task)}
        onNewTask={() => setShowNewTask(true)}
        onSettings={onSettings}
      />

      {/* New Task Dialog */}
      {showNewTask && (
        <NewTaskDialog
          projects={projects}
          onSubmit={handleCreateTask}
          onClose={() => setShowNewTask(false)}
        />
      )}

      {/* Task Detail Panel */}
      {selectedTask && (
        <TaskDetail
          task={selectedTask}
          onClose={() => setSelectedTask(null)}
          onUpdate={handleTaskUpdate}
          onDelete={handleDeleteTask}
        />
      )}
    </div>
  );
}
