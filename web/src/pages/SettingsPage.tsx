import { useState, useEffect } from 'react';
import { motion } from 'framer-motion';
import {
  ArrowLeft,
  Plus,
  Folder,
  Palette,
  Settings2,
  Download,
  Check,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card';
import { ProjectDialog } from '@/components/projects/ProjectDialog';
import { settings as settingsApi, projects as projectsApi, exportData } from '@/api/client';
import type { Project, CreateProjectRequest, UpdateProjectRequest } from '@/api/types';
import { cn } from '@/lib/utils';

interface SettingsPageProps {
  onBack: () => void;
}

const containerVariants = {
  hidden: { opacity: 0 },
  visible: {
    opacity: 1,
    transition: { staggerChildren: 0.1 },
  },
};

const itemVariants = {
  hidden: { opacity: 0, y: 20 },
  visible: { opacity: 1, y: 0 },
};

export function SettingsPage({ onBack }: SettingsPageProps) {
  const [settings, setSettings] = useState<Record<string, string>>({});
  const [projects, setProjects] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [editingProject, setEditingProject] = useState<Project | null>(null);
  const [showNewProject, setShowNewProject] = useState(false);
  const [exporting, setExporting] = useState(false);

  useEffect(() => {
    Promise.all([settingsApi.get(), projectsApi.list()])
      .then(([settingsData, projectsData]) => {
        setSettings(settingsData);
        setProjects(projectsData);
      })
      .catch(console.error)
      .finally(() => setLoading(false));
  }, []);

  // Keyboard shortcut to go back
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onBack();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [onBack]);

  const handleSettingChange = (key: string, value: string) => {
    setSettings((prev) => ({ ...prev, [key]: value }));
    setSaved(false);
  };

  const handleSave = async () => {
    setSaving(true);
    try {
      await settingsApi.update(settings);
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
    } catch (err) {
      console.error('Failed to save settings:', err);
    } finally {
      setSaving(false);
    }
  };

  const handleExport = async () => {
    setExporting(true);
    try {
      await exportData.download();
    } catch (err) {
      console.error('Failed to export data:', err);
    } finally {
      setExporting(false);
    }
  };

  const handleCreateProject = async (data: CreateProjectRequest | UpdateProjectRequest) => {
    // When creating, we need name and path which CreateProjectRequest requires
    const createData = data as CreateProjectRequest;
    const newProject = await projectsApi.create(createData);
    setProjects((prev) => [...prev, newProject]);
  };

  const handleUpdateProject = async (data: CreateProjectRequest | UpdateProjectRequest) => {
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
        <div className="flex flex-col items-center gap-4">
          <div className="h-8 w-8 animate-spin rounded-full border-4 border-primary border-t-transparent" />
          <p className="text-muted-foreground">Loading settings...</p>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-background">
      <div className="container mx-auto p-4 md:p-6 max-w-2xl">
        {/* Header */}
        <motion.div
          initial={{ opacity: 0, y: -10 }}
          animate={{ opacity: 1, y: 0 }}
          className="flex items-center gap-4 mb-8"
        >
          <Button variant="ghost" size="icon" onClick={onBack} className="shrink-0">
            <ArrowLeft className="h-5 w-5" />
          </Button>
          <div>
            <h1 className="text-2xl font-bold">Settings</h1>
            <p className="text-sm text-muted-foreground">
              Customize your taskyou experience
            </p>
          </div>
        </motion.div>

        <motion.div
          variants={containerVariants}
          initial="hidden"
          animate="visible"
          className="space-y-6"
        >
          {/* Display Settings */}
          <motion.div variants={itemVariants}>
            <Card>
              <CardHeader>
                <div className="flex items-center gap-2">
                  <div className="p-2 rounded-lg bg-primary/10">
                    <Palette className="h-4 w-4 text-primary" />
                  </div>
                  <div>
                    <CardTitle className="text-base">Appearance</CardTitle>
                    <CardDescription>Customize how taskyou looks</CardDescription>
                  </div>
                </div>
              </CardHeader>
              <CardContent className="space-y-4">
                <div>
                  <label className="text-sm font-medium mb-2 block">Theme</label>
                  <div className="grid grid-cols-3 gap-2">
                    {['light', 'dark', 'system'].map((theme) => (
                      <button
                        key={theme}
                        onClick={() => handleSettingChange('theme', theme)}
                        className={cn(
                          'flex items-center justify-center gap-2 p-3 rounded-lg border-2 transition-all capitalize',
                          settings.theme === theme || (!settings.theme && theme === 'system')
                            ? 'border-primary bg-primary/5'
                            : 'border-transparent bg-muted/50 hover:bg-muted'
                        )}
                      >
                        {theme}
                      </button>
                    ))}
                  </div>
                </div>
              </CardContent>
            </Card>
          </motion.div>

          {/* Default Values */}
          <motion.div variants={itemVariants}>
            <Card>
              <CardHeader>
                <div className="flex items-center gap-2">
                  <div className="p-2 rounded-lg bg-blue-500/10">
                    <Settings2 className="h-4 w-4 text-blue-500" />
                  </div>
                  <div>
                    <CardTitle className="text-base">Defaults</CardTitle>
                    <CardDescription>Default values for new tasks</CardDescription>
                  </div>
                </div>
              </CardHeader>
              <CardContent className="space-y-4">
                <div>
                  <label className="text-sm font-medium mb-2 block">Default Project</label>
                  <select
                    className="w-full h-10 rounded-lg border border-input bg-background px-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
                    value={settings.default_project || 'personal'}
                    onChange={(e) => handleSettingChange('default_project', e.target.value)}
                  >
                    <option value="personal">Personal</option>
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
                    className="w-full h-10 rounded-lg border border-input bg-background px-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
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
          </motion.div>

          {/* Projects */}
          <motion.div variants={itemVariants}>
            <Card>
              <CardHeader>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <div className="p-2 rounded-lg bg-green-500/10">
                      <Folder className="h-4 w-4 text-green-500" />
                    </div>
                    <div>
                      <CardTitle className="text-base">Projects</CardTitle>
                      <CardDescription>Manage your projects</CardDescription>
                    </div>
                  </div>
                  <Button size="sm" onClick={() => setShowNewProject(true)} className="gap-1">
                    <Plus className="h-4 w-4" />
                    Add
                  </Button>
                </div>
              </CardHeader>
              <CardContent>
                {projects.length === 0 ? (
                  <div className="text-center py-8 text-muted-foreground">
                    <Folder className="h-8 w-8 mx-auto mb-2 opacity-30" />
                    <p className="text-sm">No projects yet</p>
                    <Button
                      variant="link"
                      size="sm"
                      onClick={() => setShowNewProject(true)}
                    >
                      Create your first project
                    </Button>
                  </div>
                ) : (
                  <div className="space-y-2">
                    {projects.map((project) => (
                      <motion.div
                        key={project.id}
                        initial={{ opacity: 0, x: -10 }}
                        animate={{ opacity: 1, x: 0 }}
                        className="flex items-center justify-between p-3 rounded-lg border hover:bg-muted/50 transition-colors group"
                      >
                        <div className="flex items-center gap-3 min-w-0">
                          <div
                            className="w-3 h-3 rounded-full shrink-0"
                            style={{ backgroundColor: project.color || '#888' }}
                          />
                          <div className="min-w-0">
                            <div className="font-medium text-sm">{project.name}</div>
                            <div className="text-xs text-muted-foreground truncate max-w-[200px]">
                              {project.path}
                            </div>
                          </div>
                        </div>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="opacity-0 group-hover:opacity-100 transition-opacity"
                          onClick={() => setEditingProject(project)}
                        >
                          Edit
                        </Button>
                      </motion.div>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>
          </motion.div>

          {/* Data Management */}
          <motion.div variants={itemVariants}>
            <Card>
              <CardHeader>
                <div className="flex items-center gap-2">
                  <div className="p-2 rounded-lg bg-orange-500/10">
                    <Download className="h-4 w-4 text-orange-500" />
                  </div>
                  <div>
                    <CardTitle className="text-base">Data</CardTitle>
                    <CardDescription>Export or manage your data</CardDescription>
                  </div>
                </div>
              </CardHeader>
              <CardContent>
                <div className="flex flex-wrap gap-2">
                  <Button
                    variant="outline"
                    onClick={handleExport}
                    disabled={exporting}
                    className="gap-2"
                  >
                    <Download className="h-4 w-4" />
                    {exporting ? 'Exporting...' : 'Export Database'}
                  </Button>
                </div>
                <p className="text-xs text-muted-foreground mt-3">
                  Download a copy of your task database. You can use this to migrate to a local setup.
                </p>
              </CardContent>
            </Card>
          </motion.div>

          {/* Save Button */}
          <motion.div variants={itemVariants} className="flex justify-end pb-8">
            <Button
              onClick={handleSave}
              disabled={saving}
              className={cn(
                'gap-2 min-w-[140px] transition-all',
                saved && 'bg-green-500 hover:bg-green-600'
              )}
            >
              {saving ? (
                <>
                  <span className="h-4 w-4 border-2 border-current border-t-transparent rounded-full animate-spin" />
                  Saving...
                </>
              ) : saved ? (
                <>
                  <Check className="h-4 w-4" />
                  Saved!
                </>
              ) : (
                'Save Settings'
              )}
            </Button>
          </motion.div>
        </motion.div>
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
