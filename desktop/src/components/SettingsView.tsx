import { useEffect, useState } from "react";
import { MachineSettings } from "./MachineSettings";
import { RoutinesView } from "./RoutinesView";
import { cn } from "@/lib/utils";
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

const SETTINGS_SECTIONS = [
  ["appearance", "Appearance"], ["projects", "Projects"], ["types", "Task types"],
  ["routines", "Routines"], ["machines", "Machines"], ["executors", "Executors"],
] as const;
type SettingsSection = typeof SETTINGS_SECTIONS[number][0];

export function SettingsView() {
  const { theme, executors } = useAppState();
  const [section, setSection] = useState<SettingsSection>(() => {
    try {
      const saved = localStorage.getItem("ty:settings-section");
      return SETTINGS_SECTIONS.find(([key]) => key === saved)?.[0] ?? "projects";
    } catch { return "projects"; }
  });
  function selectSection(next: SettingsSection) {
    setSection(next);
    try { localStorage.setItem("ty:settings-section", next); } catch { /* optional persistence */ }
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col md:flex-row">
      <nav aria-label="Settings sections" className="grid shrink-0 grid-cols-3 gap-1 border-b p-3 md:flex md:w-48 md:flex-col md:border-r md:border-b-0">
        {SETTINGS_SECTIONS.map(([key, label]) => <Button key={key} variant={section === key ? "secondary" : "ghost"}
          className="h-11 shrink-0 justify-start" aria-current={section === key ? "page" : undefined} onClick={() => selectSection(key)}>{label}</Button>)}
      </nav>
      <div className="min-h-0 min-w-0 flex-1 overflow-y-auto p-4 md:p-6">
        <div className="mx-auto flex max-w-4xl flex-col">
          {section === "appearance" && <section className="space-y-4">
            <h2 className="text-lg font-semibold">Appearance</h2>
            <p className="text-sm text-muted-foreground">Choose how TaskYou looks in this browser or app.</p>
            <div role="group" aria-label="Color theme" className="flex flex-wrap gap-2">
              {(["light", "dark", "system"] as const).map((value) => <Button key={value} variant="outline" aria-pressed={theme === value}
                className={cn("h-11 capitalize", theme === value && "border-primary bg-accent")} onClick={() => store.setTheme(value)}>{value}</Button>)}
            </div>
            <ConnectionSettings />
          </section>}
          {section === "projects" && <ProjectSettings />}
          {section === "types" && <TypeSettings />}
          {section === "routines" && <RoutinesView />}
          {section === "machines" && <MachineSettings />}
          {section === "executors" && <section className="space-y-4">
            <h2 className="text-lg font-semibold">Executors</h2>
            <p className="text-sm text-muted-foreground">Availability on the TaskYou server. Choose an executor in the task form; remote availability may differ.</p>
            <div className="divide-y rounded-lg border">{executors.map((executor) => <div key={executor.name} className="flex flex-wrap items-center justify-between gap-2 p-4">
              <span className="font-medium">{executor.name}{executor.default && <span className="ml-2 text-xs text-muted-foreground">Default</span>}</span>
              <span className="text-sm text-muted-foreground">{executor.available ? "Available" : "Not installed"}</span>
            </div>)}</div>
            {executors.length === 0 && <p className="text-sm text-muted-foreground">No executor information is available.</p>}
          </section>}
        </div>
      </div>
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
      <div className="grid grid-cols-1 gap-3.5 sm:grid-cols-2">
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
        {/* Wraps on a phone: name on the first line, the full path on its own
            line instead of truncating to "/Us…", then the actions. */}
        {projects.map((p) => (
          <div
            key={p.name}
            className="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-3 py-2.5 text-[12.5px]"
          >
            <span
              className="inline-block size-2 shrink-0 rounded-full"
              style={{ background: p.color || "var(--muted-foreground)" }}
            />
            <span className="font-medium">{p.name}</span>
            <span className="w-full truncate text-muted-foreground md:w-auto">{p.path}</span>
            <span className="ml-auto shrink-0 text-muted-foreground">{p.task_count} tasks</span>
            <Button
              variant="ghost"
              size="sm"
              className="h-9 px-2 md:h-6"
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
              className="h-9 px-2 text-destructive md:h-6"
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
          <div className="grid grid-cols-1 gap-3.5 sm:grid-cols-2">
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
          <div
            key={t.name}
            className="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-3 py-2.5 text-[12.5px]"
          >
            <span className="font-medium">{t.name}</span>
            <span className="text-muted-foreground">{t.label}</span>
            <div className="ml-auto" />
            <Button
              variant="ghost"
              size="sm"
              className="h-9 px-2 md:h-6"
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
                className="h-9 px-2 text-destructive md:h-6"
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
          <div className="grid grid-cols-1 gap-3.5 sm:grid-cols-2">
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
