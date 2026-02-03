import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import type { Project, CreateProjectRequest, UpdateProjectRequest } from '@/api/types';

interface ProjectDialogProps {
  project?: Project | null;
  onSubmit: (data: CreateProjectRequest | UpdateProjectRequest) => Promise<void>;
  onDelete?: () => Promise<void>;
  onClose: () => void;
}

const COLORS = [
  '#C678DD', // Purple
  '#61AFEF', // Blue
  '#56B6C2', // Cyan
  '#98C379', // Green
  '#E5C07B', // Yellow
  '#E06C75', // Red/Pink
  '#D19A66', // Orange
  '#ABB2BF', // Gray
];

export function ProjectDialog({ project, onSubmit, onDelete, onClose }: ProjectDialogProps) {
  const [name, setName] = useState(project?.name || '');
  const [path, setPath] = useState(project?.path || '');
  const [aliases, setAliases] = useState(project?.aliases || '');
  const [instructions, setInstructions] = useState(project?.instructions || '');
  const [color, setColor] = useState(project?.color || COLORS[0]);
  const [loading, setLoading] = useState(false);
  const [deleting, setDeleting] = useState(false);

  const isEditing = !!project;
  const isPersonal = project?.name === 'personal';

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name || !path) return;

    setLoading(true);
    try {
      await onSubmit({
        name,
        path,
        aliases,
        instructions,
        color,
      });
      onClose();
    } catch (err) {
      console.error('Failed to save project:', err);
    } finally {
      setLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!onDelete) return;
    if (!window.confirm(`Delete project "${name}"? Tasks will not be deleted.`)) return;

    setDeleting(true);
    try {
      await onDelete();
      onClose();
    } catch (err) {
      console.error('Failed to delete project:', err);
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
      <div className="relative bg-background border rounded-lg shadow-xl w-full max-w-lg p-6">
        <h2 className="text-lg font-semibold mb-4">
          {isEditing ? 'Edit Project' : 'New Project'}
        </h2>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="text-sm font-medium mb-1 block">Name</label>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="project-name"
              disabled={isPersonal}
              required
            />
            {isPersonal && (
              <p className="text-xs text-muted-foreground mt-1">
                The personal project cannot be renamed
              </p>
            )}
          </div>

          <div>
            <label className="text-sm font-medium mb-1 block">Path</label>
            <Input
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder="/path/to/project"
              required
            />
          </div>

          <div>
            <label className="text-sm font-medium mb-1 block">Aliases (comma-separated)</label>
            <Input
              value={aliases}
              onChange={(e) => setAliases(e.target.value)}
              placeholder="alias1, alias2"
            />
          </div>

          <div>
            <label className="text-sm font-medium mb-1 block">Instructions</label>
            <Textarea
              value={instructions}
              onChange={(e) => setInstructions(e.target.value)}
              placeholder="Project-specific instructions for the AI..."
              rows={4}
            />
          </div>

          <div>
            <label className="text-sm font-medium mb-2 block">Color</label>
            <div className="flex gap-2 flex-wrap">
              {COLORS.map((c) => (
                <button
                  key={c}
                  type="button"
                  className={`w-8 h-8 rounded-full border-2 transition-all ${
                    color === c ? 'border-white scale-110' : 'border-transparent'
                  }`}
                  style={{ backgroundColor: c }}
                  onClick={() => setColor(c)}
                />
              ))}
            </div>
          </div>

          <div className="flex justify-between pt-4">
            <div>
              {isEditing && !isPersonal && onDelete && (
                <Button
                  type="button"
                  variant="destructive"
                  onClick={handleDelete}
                  disabled={deleting}
                >
                  {deleting ? 'Deleting...' : 'Delete'}
                </Button>
              )}
            </div>
            <div className="flex gap-2">
              <Button type="button" variant="ghost" onClick={onClose}>
                Cancel
              </Button>
              <Button type="submit" disabled={loading || !name || !path}>
                {loading ? 'Saving...' : isEditing ? 'Save' : 'Create'}
              </Button>
            </div>
          </div>
        </form>
      </div>
    </div>
  );
}
