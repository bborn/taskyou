import { useState, useEffect, useCallback } from 'react';
import { tasks as tasksApi } from '@/api/client';
import type { Task, CreateTaskRequest, UpdateTaskRequest, WebSocketMessage } from '@/api/types';
import { useWebSocket } from './useWebSocket';

interface UseTasksReturn {
  tasks: Task[];
  loading: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
  createTask: (data: CreateTaskRequest) => Promise<Task>;
  updateTask: (id: number, data: UpdateTaskRequest) => Promise<Task>;
  deleteTask: (id: number) => Promise<void>;
  queueTask: (id: number) => Promise<Task>;
  retryTask: (id: number, feedback?: string) => Promise<Task>;
  closeTask: (id: number) => Promise<Task>;
}

export function useTasks(): UseTasksReturn {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);

  const fetchTasks = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const data = await tasksApi.list({ all: true });
      setTasks(data);
    } catch (err) {
      setError(err instanceof Error ? err : new Error('Failed to fetch tasks'));
    } finally {
      setLoading(false);
    }
  }, []);

  // Handle WebSocket messages
  const handleMessage = useCallback((message: WebSocketMessage) => {
    switch (message.type) {
      case 'task_update':
        setTasks(prev => {
          const index = prev.findIndex(t => t.id === message.data.id);
          if (index >= 0) {
            const updated = [...prev];
            updated[index] = message.data;
            return updated;
          }
          return [...prev, message.data];
        });
        break;
      case 'task_deleted':
        setTasks(prev => prev.filter(t => t.id !== message.data.id));
        break;
    }
  }, []);

  useWebSocket(handleMessage);

  useEffect(() => {
    fetchTasks();
  }, [fetchTasks]);

  const createTask = useCallback(async (data: CreateTaskRequest) => {
    const task = await tasksApi.create(data);
    setTasks(prev => [...prev, task]);
    return task;
  }, []);

  const updateTask = useCallback(async (id: number, data: UpdateTaskRequest) => {
    const task = await tasksApi.update(id, data);
    setTasks(prev => prev.map(t => t.id === id ? task : t));
    return task;
  }, []);

  const deleteTask = useCallback(async (id: number) => {
    await tasksApi.delete(id);
    setTasks(prev => prev.filter(t => t.id !== id));
  }, []);

  const queueTask = useCallback(async (id: number) => {
    const task = await tasksApi.queue(id);
    setTasks(prev => prev.map(t => t.id === id ? task : t));
    return task;
  }, []);

  const retryTask = useCallback(async (id: number, feedback?: string) => {
    const task = await tasksApi.retry(id, feedback);
    setTasks(prev => prev.map(t => t.id === id ? task : t));
    return task;
  }, []);

  const closeTask = useCallback(async (id: number) => {
    const task = await tasksApi.close(id);
    setTasks(prev => prev.map(t => t.id === id ? task : t));
    return task;
  }, []);

  return {
    tasks,
    loading,
    error,
    refresh: fetchTasks,
    createTask,
    updateTask,
    deleteTask,
    queueTask,
    retryTask,
    closeTask,
  };
}
