import { useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import { store, useAppState, type FormState } from "../store";

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

const PERMISSION_MODES = [
  { value: "", label: "default (prompt)" },
  { value: "accept-edits", label: "accept edits" },
  { value: "auto", label: "auto-approve safe" },
  { value: "dangerous", label: "dangerous (skip prompts)" },
];

const EFFORT_LEVELS = ["", "low", "medium", "high"];

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
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [files, setFiles] = useState<File[]>([]);
  const [executeNow, setExecuteNow] = useState(false);
  const [saving, setSaving] = useState(false);
  const [ghost, setGhost] = useState("");

  const titleRef = useRef<HTMLInputElement>(null);
  const ghostTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (editing) {
      setTitle(editing.title);
      setBody(editing.body);
      setProject(editing.project);
      setType(editing.type || types[0]?.name || "");
      setExecutor(editing.executor || "");
      setEffort(editing.effort_level ?? "");
    }
    titleRef.current?.focus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

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
    <div className="overlay" onMouseDown={close}>
      <div
        className="modal wide"
        onMouseDown={(e) => e.stopPropagation()}
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
        <div className="modal-header">{form.kind === "new" ? "New task" : `Edit #${editing?.id}`}</div>
        <div className="modal-body">
          <div className="form-row">
            <label>Project</label>
            <select value={project} onChange={(e) => setProject(e.target.value)}>
              {projects.map((p) => (
                <option key={p.name} value={p.name}>
                  {p.name}
                </option>
              ))}
              {projects.length === 0 && <option value="">(no projects)</option>}
            </select>
          </div>

          <div className="form-row">
            <label>Title</label>
            <div className="ghost-wrap">
              <input
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
                <div className="ghost-suggestion">
                  <span style={{ visibility: "hidden" }}>{title}</span>
                  {ghost}
                </div>
              )}
            </div>
            {ghost && <span className="hint">Tab to accept suggestion</span>}
          </div>

          <div className="form-row">
            <label>Description (markdown)</label>
            <textarea rows={7} value={body} onChange={(e) => setBody(e.target.value)} />
          </div>

          {files.length > 0 && (
            <div className="form-row">
              <label>Attachments</label>
              <div className="attach-list">
                {files.map((f, i) => (
                  <div key={`${f.name}-${i}`} className="attach">
                    <span>{f.name}</span>
                    <button
                      className="icon-btn"
                      onClick={() => setFiles(files.filter((_, j) => j !== i))}
                    >
                      ✕
                    </button>
                  </div>
                ))}
              </div>
            </div>
          )}

          <div className="form-grid">
            <div className="form-row">
              <label>Type</label>
              <select value={type} onChange={(e) => setType(e.target.value)}>
                {types.map((t) => (
                  <option key={t.name} value={t.name}>
                    {t.label || t.name}
                  </option>
                ))}
                {types.length === 0 && <option value="task">task</option>}
              </select>
            </div>
            <div className="form-row">
              <label>Executor</label>
              <select value={executor} onChange={(e) => setExecutor(e.target.value)}>
                <option value="">default</option>
                {executors.map((ex) => (
                  <option key={ex.name} value={ex.name} disabled={!ex.available}>
                    {ex.name}
                    {ex.available ? "" : " (not installed)"}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <button className="icon-btn" onClick={() => setShowAdvanced(!showAdvanced)}>
            {showAdvanced ? "▾" : "▸"} Advanced
          </button>
          {showAdvanced && (
            <div className="form-grid" style={{ marginTop: 8 }}>
              <div className="form-row">
                <label>Effort</label>
                <select value={effort} onChange={(e) => setEffort(e.target.value)}>
                  {EFFORT_LEVELS.map((l) => (
                    <option key={l} value={l}>
                      {l || "default"}
                    </option>
                  ))}
                </select>
              </div>
              <div className="form-row">
                <label>Permission mode</label>
                <select value={permission} onChange={(e) => setPermission(e.target.value)}>
                  {PERMISSION_MODES.map((m) => (
                    <option key={m.value} value={m.value}>
                      {m.label}
                    </option>
                  ))}
                </select>
              </div>
            </div>
          )}
        </div>
        <div className="modal-footer">
          {form.kind === "new" && (
            <label style={{ display: "flex", gap: 6, alignItems: "center", marginRight: "auto" }}>
              <input
                type="checkbox"
                checked={executeNow}
                onChange={(e) => setExecuteNow(e.target.checked)}
              />
              Execute immediately
            </label>
          )}
          <button className="btn" onClick={close}>
            Cancel
          </button>
          <button className="btn primary" disabled={saving} onClick={() => void submit()}>
            {saving ? "Saving…" : form.kind === "new" ? "Create (⌘S)" : "Save (⌘S)"}
          </button>
        </div>
      </div>
    </div>
  );
}
