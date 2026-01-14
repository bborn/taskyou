import { useState, useEffect, useCallback } from 'react';
import { Header } from '@/components/layout/Header';
import { TaskBoard } from '@/components/tasks/TaskBoard';
import { TaskDetail } from '@/components/tasks/TaskDetail';
import { NewTaskDialog } from '@/components/tasks/NewTaskDialog';
import { useTasks } from '@/hooks/useTasks';
import { projects as projectsApi } from '@/api/client';
import type { User, Task, Project } from '@/api/types';

interface DashboardPageProps {
  user: User;
  onLogout: () => void;
  onSettings: () => void;
}

export function DashboardPage({ user, onLogout, onSettings }: DashboardPageProps) {
  const { tasks, queueTask, retryTask, closeTask, createTask } = useTasks();
  const [projects, setProjects] = useState<Project[]>([]);
  const [showNewTask, setShowNewTask] = useState(false);
  const [selectedTask, setSelectedTask] = useState<Task | null>(null);

  useEffect(() => {
    projectsApi.list().then(setProjects).catch(console.error);
  }, []);

  const handleTaskClick = useCallback((task: Task) => {
    setSelectedTask(task);
  }, []);

  const handleTaskUpdate = useCallback((updatedTask: Task) => {
    setSelectedTask(updatedTask);
  }, []);

  const handleCreateTask = useCallback(async (data: Parameters<typeof createTask>[0]) => {
    await createTask(data);
  }, [createTask]);

  return (
    <div className="min-h-screen bg-background">
      <Header user={user} onLogout={onLogout} onSettings={onSettings} />

      <main className="container mx-auto p-4">
        <TaskBoard
          tasks={tasks}
          onQueue={queueTask}
          onRetry={(id) => retryTask(id)}
          onClose={closeTask}
          onTaskClick={handleTaskClick}
          onNewTask={() => setShowNewTask(true)}
        />
      </main>

      {showNewTask && (
        <NewTaskDialog
          projects={projects}
          onSubmit={handleCreateTask}
          onClose={() => setShowNewTask(false)}
        />
      )}

      {selectedTask && (
        <TaskDetail
          task={selectedTask}
          onClose={() => setSelectedTask(null)}
          onUpdate={handleTaskUpdate}
        />
      )}
    </div>
  );
}
