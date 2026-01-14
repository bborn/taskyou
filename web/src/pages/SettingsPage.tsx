import { useState, useEffect } from 'react';
import { ArrowLeft, Plus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
import { ProjectDialog } from '@/components/projects/ProjectDialog';
import { settings as settingsApi, projects as projectsApi } from '@/api/client';
import type { Project, CreateProjectRequest, UpdateProjectRequest } from '@/api/types';

interface SettingsPageProps {
  onBack: () => void;
}

export function SettingsPage({ onBack }: SettingsPageProps) {
  const [settings, setSettings] = useState<Record<string, string>>({});
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [editingProject, setEditingProject] = useState<Project | null>(null);
  const [showNewProject, setShowNewProject] = useState(false);

  useEffect(() => {
    Promise.all([
      settingsApi.get(),
      projectsApi.list(),
    ])
      .then(([settingsData, projectsData]) => {
        setSettings(settingsData);
        setProjects(projectsData);
      })
      .catch(console.error)
      .finally(() => setLoading(false));
  }, []);

  const handleSettingChange = (key: string, value: string) => {
    setSettings((prev) => ({ ...prev, [key]: value }));
  };

  const handleSave = async () => {
    setSaving(true);
    try {
      await settingsApi.update(settings);
    } catch (err) {
      console.error('Failed to save settings:', err);
    } finally {
      setSaving(false);
    }
  };

  const handleCreateProject = async (data: CreateProjectRequest) => {
    const newProject = await projectsApi.create(data);
    setProjects((prev) => [...prev, newProject]);
  };

  const handleUpdateProject = async (data: UpdateProjectRequest) => {
    if (!editingProject) return;
    const updated = await projectsApi.update(editingProject.id, data);
    setProjects((prev) =>
      prev.map((p) => (p.id === editingProject.id ? updated : p))
    );
  };

  const handleDeleteProject = async () => {
    if (!editingProject) return;
    await projectsApi.delete(editingProject.id);
    setProjects((prev) => prev.filter((p) => p.id !== editingProject.id));
  };

  if (loading) {
    return (
      <div className="min-h-screen bg-background flex items-center justify-center">
        <div className="text-muted-foreground">Loading settings...</div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-background">
      <div className="container mx-auto p-4 max-w-2xl">
        <div className="flex items-center gap-4 mb-6">
          <Button variant="ghost" size="icon" onClick={onBack}>
            <ArrowLeft className="h-5 w-5" />
          </Button>
          <h1 className="text-2xl font-bold">Settings</h1>
        </div>

        <div className="space-y-6">
          {/* Display Settings */}
          <Card>
            <CardHeader>
              <CardTitle>Display</CardTitle>
              <CardDescription>Customize the appearance of the UI</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div>
                <label className="text-sm font-medium mb-2 block">Theme</label>
                <select
                  className="w-full p-2 rounded-md border bg-background"
                  value={settings.theme || 'dark'}
                  onChange={(e) => handleSettingChange('theme', e.target.value)}
                >
                  <option value="dark">Dark</option>
                  <option value="light">Light</option>
                  <option value="system">System</option>
                </select>
              </div>
              <div>
                <label className="text-sm font-medium mb-2 block">Pane Height (%)</label>
                <Input
                  type="number"
                  min="20"
                  max="80"
                  value={settings.pane_height || '40'}
                  onChange={(e) => handleSettingChange('pane_height', e.target.value)}
                />
              </div>
            </CardContent>
          </Card>

          {/* Default Values */}
          <Card>
            <CardHeader>
              <CardTitle>Defaults</CardTitle>
              <CardDescription>Default values for new tasks</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div>
                <label className="text-sm font-medium mb-2 block">Default Project</label>
                <select
                  className="w-full p-2 rounded-md border bg-background"
                  value={settings.default_project || 'personal'}
                  onChange={(e) => handleSettingChange('default_project', e.target.value)}
                >
                  {projects.map((p) => (
                    <option key={p.id} value={p.name}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className="text-sm font-medium mb-2 block">Default Task Type</label>
                <select
                  className="w-full p-2 rounded-md border bg-background"
                  value={settings.default_type || 'code'}
                  onChange={(e) => handleSettingChange('default_type', e.target.value)}
                >
                  <option value="code">Code</option>
                  <option value="writing">Writing</option>
                  <option value="thinking">Thinking</option>
                </select>
              </div>
            </CardContent>
          </Card>

          {/* Projects */}
          <Card>
            <CardHeader>
              <div className="flex items-center justify-between">
                <div>
                  <CardTitle>Projects</CardTitle>
                  <CardDescription>Manage your projects</CardDescription>
                </div>
                <Button size="sm" onClick={() => setShowNewProject(true)}>
                  <Plus className="h-4 w-4 mr-1" />
                  New
                </Button>
              </div>
            </CardHeader>
            <CardContent>
              <div className="space-y-2">
                {projects.map((project) => (
                  <div
                    key={project.id}
                    className="flex items-center justify-between p-3 rounded-lg border"
                  >
                    <div className="flex items-center gap-3">
                      <div
                        className="w-3 h-3 rounded-full"
                        style={{ backgroundColor: project.color || '#888' }}
                      />
                      <div>
                        <div className="font-medium">{project.name}</div>
                        <div className="text-xs text-muted-foreground truncate max-w-xs">
                          {project.path}
                        </div>
                      </div>
                    </div>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => setEditingProject(project)}
                    >
                      Edit
                    </Button>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>

          {/* Save Button */}
          <div className="flex justify-end">
            <Button onClick={handleSave} disabled={saving}>
              {saving ? 'Saving...' : 'Save Settings'}
            </Button>
          </div>
        </div>
      </div>

      {/* New Project Dialog */}
      {showNewProject && (
        <ProjectDialog
          onSubmit={handleCreateProject}
          onClose={() => setShowNewProject(false)}
        />
      )}

      {/* Edit Project Dialog */}
      {editingProject && (
        <ProjectDialog
          project={editingProject}
          onSubmit={handleUpdateProject}
          onDelete={handleDeleteProject}
          onClose={() => setEditingProject(null)}
        />
      )}
    </div>
  );
}
