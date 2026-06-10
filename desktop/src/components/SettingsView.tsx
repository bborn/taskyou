import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { Project, TaskType } from "../api/types";
import { inTauri, supervisorGetConfig, supervisorSetConfig, supervisorStatus } from "../tauri";
import { store, useAppState } from "../store";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";

function SectionHeading({ children }: { children: React.ReactNode }) {
  return (
    <h2 className="mb-2.5 mt-7 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground first:mt-0">
      {children}
    </h2>
  );
}

export function SettingsView() {
  return (
    <div className="max-w-3xl flex-1 overflow-y-auto px-6 py-5">
      <ConnectionSettings />
      <ProjectSettings />
      <TypeSettings />
    </div>
  );
}

function ConnectionSettings() {
  const [port, setPort] = useState("");
  const [tyPath, setTyPath] = useState("");
  const [status, setStatus] = useState("");

  useEffect(() => {
    if (!inTauri()) return;
    void (async () => {
      const config = await supervisorGetConfig();
      setPort(String(config.port));
      setTyPath(config.ty_path ?? "");
      const s = await supervisorStatus();
      setStatus(
        `server ${s.server_running ? "✓" : "✗"} · daemon ${s.daemon_running ? "✓" : "✗"} · ty: ${s.ty_path ?? "not found"}`,
      );
    })();
  }, []);

  if (!inTauri()) return null;

  return (
    <>
      <SectionHeading>Connection</SectionHeading>
      <div className="grid grid-cols-2 gap-3.5">
        <div className="grid gap-1.5">
          <Label>API port</Label>
          <Input value={port} onChange={(e) => setPort(e.target.value)} />
        </div>
        <div className="grid gap-1.5">
          <Label>ty binary path (blank = auto-detect)</Label>
          <Input
            value={tyPath}
            placeholder="/usr/local/bin/ty"
            onChange={(e) => setTyPath(e.target.value)}
          />
        </div>
      </div>
      <p className="mt-2 text-xs text-muted-foreground">{status}</p>
      <Button
        variant="outline"
        size="sm"
        className="mt-2"
        onClick={async () => {
          const parsed = parseInt(port, 10);
          if (!parsed || parsed < 1 || parsed > 65535) {
            store.toast({ title: "Invalid port", kind: "error" });
            return;
          }
          await supervisorSetConfig({ port: parsed, ty_path: tyPath || null });
          store.toast({ title: "Saved — restart the app to apply", kind: "info" });
        }}
      >
        Save connection settings
      </Button>
    </>
  );
}

const EMPTY_PROJECT: Partial<Project> = { name: "", path: "", color: "", instructions: "", aliases: "" };

function ProjectSettings() {
  const { projects } = useAppState();
  const [editing, setEditing] = useState<Partial<Project> | null>(null);
  const [isNew, setIsNew] = useState(false);

  async function save() {
    if (!editing?.name) {
      store.toast({ title: "Project name required", kind: "warning" });
      return;
    }
    try {
      if (isNew) {
        await api.createProject(editing);
      } else {
        await api.updateProject(editing.name, editing);
      }
      setEditing(null);
      await store.loadAll();
    } catch (e) {
      store.toast({
        title: "Failed to save project",
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    }
  }

  return (
    <>
      <SectionHeading>Projects</SectionHeading>
      <div className="divide-y rounded-lg border">
        {projects.map((p) => (
          <div key={p.name} className="flex items-center gap-3 px-3 py-2 text-[12.5px]">
            <span
              className="inline-block size-2 shrink-0 rounded-full"
              style={{ background: p.color || "var(--muted-foreground)" }}
            />
            <span className="font-medium">{p.name}</span>
            <span className="truncate text-muted-foreground">{p.path}</span>
            <span className="ml-auto shrink-0 text-muted-foreground">{p.task_count} tasks</span>
            <Button
              variant="ghost"
              size="sm"
              className="h-6 px-2"
              onClick={() => {
                setEditing({ ...p });
                setIsNew(false);
              }}
            >
              Edit
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className="h-6 px-2 text-destructive"
              onClick={() =>
                store.setDialog({
                  kind: "confirm",
                  title: `Delete project ${p.name}?`,
                  message: "Tasks keep their project label but lose project settings.",
                  danger: true,
                  onConfirm: async () => {
                    await api
                      .deleteProject(p.name)
                      .catch((e) => store.toast({ title: "Delete failed", body: String(e), kind: "error" }));
                    await store.loadAll();
                  },
                })
              }
            >
              Delete
            </Button>
          </div>
        ))}
        {projects.length === 0 && (
          <div className="px-3 py-4 text-center text-xs text-muted-foreground">No projects yet</div>
        )}
      </div>
      <Button
        variant="outline"
        size="sm"
        className="mt-2"
        onClick={() => {
          setEditing({ ...EMPTY_PROJECT });
          setIsNew(true);
        }}
      >
        Add project
      </Button>

      <Dialog open={editing !== null} onOpenChange={(open) => !open && setEditing(null)}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{isNew ? "Add project" : `Edit project ${editing?.name ?? ""}`}</DialogTitle>
          </DialogHeader>
          {editing && (
            <>
          <div className="grid grid-cols-2 gap-3.5">
            <div className="grid gap-1.5">
              <Label>Name</Label>
              <Input
                value={editing.name ?? ""}
                disabled={!isNew}
                onChange={(e) => setEditing({ ...editing, name: e.target.value })}
              />
            </div>
            <div className="grid gap-1.5">
              <Label>Path</Label>
              <Input
                value={editing.path ?? ""}
                placeholder="/Users/you/Projects/app"
                onChange={(e) => setEditing({ ...editing, path: e.target.value })}
              />
            </div>
            <div className="grid gap-1.5">
              <Label>Color</Label>
              <Input
                value={editing.color ?? ""}
                placeholder="#7aa2f7"
                onChange={(e) => setEditing({ ...editing, color: e.target.value })}
              />
            </div>
            <div className="grid gap-1.5">
              <Label>Aliases (comma-separated)</Label>
              <Input
                value={editing.aliases ?? ""}
                onChange={(e) => setEditing({ ...editing, aliases: e.target.value })}
              />
            </div>
            <div className="grid gap-1.5">
              <Label>Claude config dir</Label>
              <Input
                value={editing.claude_config_dir ?? ""}
                onChange={(e) => setEditing({ ...editing, claude_config_dir: e.target.value })}
              />
            </div>
            <div className="flex items-center gap-2 self-end pb-1.5">
              <Switch
                id="use-worktrees"
                checked={editing.use_worktrees !== false}
                onCheckedChange={(v) => setEditing({ ...editing, use_worktrees: v })}
              />
              <Label htmlFor="use-worktrees" className="font-normal">
                Use worktrees
              </Label>
            </div>
          </div>
          <div className="mt-3 grid gap-1.5">
            <Label>Instructions</Label>
            <Textarea
              rows={4}
              value={editing.instructions ?? ""}
              onChange={(e) => setEditing({ ...editing, instructions: e.target.value })}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setEditing(null)}>
              Cancel
            </Button>
            <Button size="sm" onClick={() => void save()}>
              Save project
            </Button>
          </DialogFooter>
            </>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}

function TypeSettings() {
  const { types } = useAppState();
  const [editing, setEditing] = useState<Partial<TaskType> | null>(null);
  const [isNew, setIsNew] = useState(false);

  async function save() {
    if (!editing?.name) {
      store.toast({ title: "Type name required", kind: "warning" });
      return;
    }
    try {
      if (isNew) {
        await api.createType(editing);
      } else {
        await api.updateType(editing.name, editing);
      }
      setEditing(null);
      await store.loadAll();
    } catch (e) {
      store.toast({
        title: "Failed to save type",
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    }
  }

  return (
    <>
      <SectionHeading>Task types</SectionHeading>
      <div className="divide-y rounded-lg border">
        {types.map((t) => (
          <div key={t.name} className="flex items-center gap-3 px-3 py-2 text-[12.5px]">
            <span className="font-medium">{t.name}</span>
            <span className="text-muted-foreground">{t.label}</span>
            <div className="ml-auto" />
            <Button
              variant="ghost"
              size="sm"
              className="h-6 px-2"
              onClick={() => {
                setEditing({ ...t });
                setIsNew(false);
              }}
            >
              Edit
            </Button>
            {!t.is_builtin && (
              <Button
                variant="ghost"
                size="sm"
                className="h-6 px-2 text-destructive"
                onClick={() =>
                  store.setDialog({
                    kind: "confirm",
                    title: `Delete type ${t.name}?`,
                    message: "Existing tasks keep the label.",
                    danger: true,
                    onConfirm: async () => {
                      await api
                        .deleteType(t.name)
                        .catch((e) => store.toast({ title: "Delete failed", body: String(e), kind: "error" }));
                      await store.loadAll();
                    },
                  })
                }
              >
                Delete
              </Button>
            )}
          </div>
        ))}
      </div>
      <Button
        variant="outline"
        size="sm"
        className="mt-2"
        onClick={() => {
          setEditing({ name: "", label: "", instructions: "" });
          setIsNew(true);
        }}
      >
        Add type
      </Button>

      <Dialog open={editing !== null} onOpenChange={(open) => !open && setEditing(null)}>
        <DialogContent className="max-w-xl">
          <DialogHeader>
            <DialogTitle>{isNew ? "Add task type" : `Edit type ${editing?.name ?? ""}`}</DialogTitle>
          </DialogHeader>
          {editing && (
            <>
          <div className="grid grid-cols-2 gap-3.5">
            <div className="grid gap-1.5">
              <Label>Name</Label>
              <Input
                value={editing.name ?? ""}
                disabled={!isNew}
                onChange={(e) => setEditing({ ...editing, name: e.target.value })}
              />
            </div>
            <div className="grid gap-1.5">
              <Label>Label</Label>
              <Input
                value={editing.label ?? ""}
                onChange={(e) => setEditing({ ...editing, label: e.target.value })}
              />
            </div>
          </div>
          <div className="mt-3 grid gap-1.5">
            <Label>Instructions (added to executor prompt)</Label>
            <Textarea
              rows={4}
              value={editing.instructions ?? ""}
              onChange={(e) => setEditing({ ...editing, instructions: e.target.value })}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setEditing(null)}>
              Cancel
            </Button>
            <Button size="sm" onClick={() => void save()}>
              Save type
            </Button>
          </DialogFooter>
            </>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
