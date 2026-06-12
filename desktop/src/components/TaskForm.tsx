import { useRef, useState } from "react";
import { X } from "lucide-react";
import { api } from "../api/client";
import { store, useAppState, type FormState } from "../store";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

async function fileToBase64(file: File): Promise<string> {
  const buf = await file.arrayBuffer();
  const bytes = new Uint8Array(buf);
  let bin = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    bin += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(bin);
}

// Radix Select forbids empty-string item values; map "" through a sentinel.
const NONE = "_default";
const fromSelect = (v: string) => (v === NONE ? "" : v);
const toSelect = (v: string) => (v === "" ? NONE : v);

const PERMISSION_MODES = [
  { value: NONE, label: "default (prompt)" },
  { value: "accept-edits", label: "accept edits" },
  { value: "auto", label: "auto-approve safe" },
  { value: "dangerous", label: "dangerous (skip prompts)" },
];

const EFFORT_LEVELS = [NONE, "low", "medium", "high"];

const WORKTREE_MODES = [
  { value: NONE, label: "Project default" },
  { value: "worktree", label: "Worktree" },
  { value: "in-place", label: "In place" },
];

export function TaskForm({ form }: { form: NonNullable<FormState> }) {
  const { projects, types, executors, tasks, permissionMode } = useAppState();
  const editing = form.kind === "edit" ? tasks.find((t) => t.id === form.taskId) : null;

  const [title, setTitle] = useState(editing?.title ?? "");
  const [body, setBody] = useState(editing?.body ?? "");
  const [project, setProject] = useState(
    editing?.project ?? (form.kind === "new" ? (form.initialProject ?? projects[0]?.name ?? "") : ""),
  );
  const [type, setType] = useState(editing?.type ?? types[0]?.name ?? "");
  const [executor, setExecutor] = useState(editing?.executor ?? "");
  const [effort, setEffort] = useState(editing?.effort_level ?? "");
  const [permission, setPermission] = useState(
    editing && editing.permission_mode !== "default" ? editing.permission_mode : permissionMode,
  );
  const [worktreeMode, setWorktreeMode] = useState(editing?.worktree_mode ?? "");
  const [baseBranch, setBaseBranch] = useState(editing?.base_branch ?? "");
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [files, setFiles] = useState<File[]>([]);
  const [executeNow, setExecuteNow] = useState(false);
  const [saving, setSaving] = useState(false);
  const [ghost, setGhost] = useState("");

  const titleRef = useRef<HTMLInputElement>(null);
  const ghostTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Base branch only applies when a fresh worktree will be created: forced per
  // task, or inherited from a project with worktrees enabled.
  const projectUsesWorktrees = projects.find((p) => p.name === project)?.use_worktrees !== false;
  const baseBranchRelevant =
    worktreeMode === "worktree" || (worktreeMode === "" && projectUsesWorktrees);
  const effectiveBaseBranch = baseBranchRelevant ? baseBranch.trim() : "";

  // Ghost-text autocomplete on the title, debounced; best-effort.
  function onTitleChange(value: string) {
    setTitle(value);
    setGhost("");
    if (ghostTimer.current) clearTimeout(ghostTimer.current);
    if (value.trim().length < 4) return;
    ghostTimer.current = setTimeout(async () => {
      try {
        const res = await api.autocomplete(value, "title", project);
        if (res.suggestion && titleRef.current?.value === value) {
          setGhost(res.suggestion);
        }
      } catch {
        // autocomplete is optional (no API key, etc.)
      }
    }, 500);
  }

  function close() {
    store.setForm(null);
  }

  async function submit(dangerous = false) {
    if (!title.trim() && !body.trim()) {
      store.toast({ title: "Title or description required", kind: "warning" });
      return;
    }
    setSaving(true);
    try {
      if (form.kind === "new") {
        const created = await api.createTask({
          title: title.trim(),
          body,
          type,
          project,
          executor,
          execute: executeNow || dangerous,
          permission_mode: dangerous ? "dangerous" : permission,
          worktree_mode: worktreeMode,
          base_branch: effectiveBaseBranch,
        });
        if (effort) await api.updateTask(created.id, { effort_level: effort }).catch(() => {});
        for (const file of files) {
          const data = await fileToBase64(file);
          await api.addAttachment(created.id, file.name, data, file.type || undefined).catch(() => {});
        }
        store.toast({ title: `Created #${created.id}`, kind: "success", taskId: created.id });
      } else if (editing) {
        await api.updateTask(editing.id, {
          title: title.trim(),
          body,
          type,
          project,
          executor,
          effort_level: effort,
          permission_mode: permission,
          worktree_mode: worktreeMode,
          base_branch: effectiveBaseBranch,
        });
        for (const file of files) {
          const data = await fileToBase64(file);
          await api.addAttachment(editing.id, file.name, data, file.type || undefined).catch(() => {});
        }
        store.toast({ title: `Updated #${editing.id}`, kind: "success" });
      }
      close();
      await store.refreshTasks();
    } catch (e) {
      store.toast({
        title: "Save failed",
        body: e instanceof Error ? e.message : String(e),
        kind: "error",
      });
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && close()}>
      <DialogContent
        className="max-w-2xl"
        onOpenAutoFocus={(e) => {
          e.preventDefault();
          titleRef.current?.focus();
        }}
        onDragOver={(e) => e.preventDefault()}
        onDrop={(e) => {
          e.preventDefault();
          if (e.dataTransfer.files.length) {
            setFiles((prev) => [...prev, ...Array.from(e.dataTransfer.files)]);
          }
        }}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === "s") {
            e.preventDefault();
            void submit();
          } else if ((e.metaKey || e.ctrlKey) && e.key === "d" && form.kind === "new") {
            e.preventDefault();
            void submit(true);
          }
        }}
      >
        <DialogHeader>
          <DialogTitle>{form.kind === "new" ? "New task" : `Edit #${editing?.id}`}</DialogTitle>
        </DialogHeader>

        <div className="flex max-h-[62vh] flex-col gap-3.5 overflow-y-auto pr-1">
          <div className="grid gap-1.5">
            <Label>Project</Label>
            <Select value={project || NONE} onValueChange={(v) => setProject(fromSelect(v))}>
              <SelectTrigger className="w-full">
                <SelectValue placeholder="Select project" />
              </SelectTrigger>
              <SelectContent>
                {projects.map((p) => (
                  <SelectItem key={p.name} value={p.name}>
                    <span
                      className="mr-1 inline-block size-2 rounded-full"
                      style={{ background: p.color || "var(--muted-foreground)" }}
                    />
                    {p.name}
                  </SelectItem>
                ))}
                {projects.length === 0 && <SelectItem value={NONE}>(no projects)</SelectItem>}
              </SelectContent>
            </Select>
          </div>

          <div className="grid gap-1.5">
            <Label>Title</Label>
            <div className="relative">
              <Input
                ref={titleRef}
                value={title}
                placeholder="What needs doing?"
                onChange={(e) => onTitleChange(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Tab" && ghost) {
                    e.preventDefault();
                    setTitle(title + ghost);
                    setGhost("");
                  }
                }}
              />
              {ghost && (
                <div className="pointer-events-none absolute inset-0 flex items-center overflow-hidden whitespace-pre px-3 text-sm text-muted-foreground">
                  <span className="invisible">{title}</span>
                  {ghost}
                </div>
              )}
            </div>
            {ghost && (
              <span className="text-[11px] text-muted-foreground">
                <span className="kbd">Tab</span> to accept suggestion
              </span>
            )}
          </div>

          <div className="grid gap-1.5">
            <Label>Description (markdown)</Label>
            <Textarea rows={7} value={body} onChange={(e) => setBody(e.target.value)} />
          </div>

          {files.length > 0 && (
            <div className="grid gap-1.5">
              <Label>Attachments</Label>
              <div className="flex flex-col gap-1">
                {files.map((f, i) => (
                  <div key={`${f.name}-${i}`} className="flex items-center gap-2 text-xs">
                    <span>{f.name}</span>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-5"
                      onClick={() => setFiles(files.filter((_, j) => j !== i))}
                    >
                      <X className="size-3" />
                    </Button>
                  </div>
                ))}
              </div>
            </div>
          )}

          <div className="grid grid-cols-2 gap-3.5">
            <div className="grid gap-1.5">
              <Label>Type</Label>
              <Select value={type || NONE} onValueChange={(v) => setType(fromSelect(v))}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {types.map((t) => (
                    <SelectItem key={t.name} value={t.name}>
                      {t.label || t.name}
                    </SelectItem>
                  ))}
                  {types.length === 0 && <SelectItem value={NONE}>default</SelectItem>}
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-1.5">
              <Label>Executor</Label>
              <Select value={toSelect(executor)} onValueChange={(v) => setExecutor(fromSelect(v))}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE}>default</SelectItem>
                  {executors.map((ex) => (
                    <SelectItem key={ex.name} value={ex.name} disabled={!ex.available}>
                      {ex.name}
                      {ex.available ? "" : " (not installed)"}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <button
            className="self-start text-xs text-muted-foreground hover:text-foreground"
            onClick={() => setShowAdvanced(!showAdvanced)}
          >
            {showAdvanced ? "▾" : "▸"} Advanced
          </button>
          {showAdvanced && (
            <div className="grid grid-cols-2 gap-3.5">
              <div className="grid gap-1.5">
                <Label>Worktree</Label>
                <Select value={toSelect(worktreeMode)} onValueChange={(v) => setWorktreeMode(fromSelect(v))}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {WORKTREE_MODES.map((m) => (
                      <SelectItem key={m.value} value={m.value}>
                        {m.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              {baseBranchRelevant && (
                <div className="grid gap-1.5">
                  <Label>Base branch</Label>
                  <Input
                    value={baseBranch}
                    placeholder="Default branch if empty"
                    onChange={(e) => setBaseBranch(e.target.value)}
                  />
                </div>
              )}
              <div className="grid gap-1.5">
                <Label>Effort</Label>
                <Select value={toSelect(effort)} onValueChange={(v) => setEffort(fromSelect(v))}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {EFFORT_LEVELS.map((l) => (
                      <SelectItem key={l} value={l}>
                        {l === NONE ? "default" : l}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-1.5">
                <Label>Permission mode</Label>
                <Select value={toSelect(permission)} onValueChange={(v) => setPermission(fromSelect(v))}>
                  <SelectTrigger className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {PERMISSION_MODES.map((m) => (
                      <SelectItem key={m.value} value={m.value}>
                        {m.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
          )}
        </div>

        <DialogFooter className="items-center">
          {form.kind === "new" && (
            <div className="mr-auto flex items-center gap-2">
              <Checkbox
                id="execute-now"
                checked={executeNow}
                onCheckedChange={(v) => setExecuteNow(v === true)}
              />
              <Label htmlFor="execute-now" className="text-xs font-normal">
                Execute immediately
              </Label>
            </div>
          )}
          <Button variant="outline" onClick={close}>
            Cancel
          </Button>
          <Button disabled={saving} onClick={() => void submit()}>
            {saving ? "Saving…" : form.kind === "new" ? "Create" : "Save"}
            <span className="kbd ml-1">⌘S</span>
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
