import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { Project, TaskType } from "../api/types";
import { inTauri, supervisorGetConfig, supervisorSetConfig, supervisorStatus } from "../tauri";
import { store, useAppState } from "../store";

export function SettingsView() {
  return (
    <div className="settings">
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
      <h2>Connection</h2>
      <div className="form-grid">
        <div className="form-row">
          <label>API port</label>
          <input value={port} onChange={(e) => setPort(e.target.value)} />
        </div>
        <div className="form-row">
          <label>ty binary path (blank = auto-detect)</label>
          <input value={tyPath} placeholder="/usr/local/bin/ty" onChange={(e) => setTyPath(e.target.value)} />
        </div>
      </div>
      <div className="empty-hint">{status}</div>
      <button
        className="btn"
        style={{ marginTop: 8 }}
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
      </button>
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
      <h2>Projects</h2>
      <table>
        <tbody>
          {projects.map((p) => (
            <tr key={p.name}>
              <td style={{ color: p.color || undefined }}>{p.name}</td>
              <td style={{ color: "var(--text-dim)" }}>{p.path}</td>
              <td style={{ color: "var(--text-dim)" }}>{p.task_count} tasks</td>
              <td className="actions">
                <button
                  className="icon-btn"
                  onClick={() => {
                    setEditing({ ...p });
                    setIsNew(false);
                  }}
                >
                  Edit
                </button>
                <button
                  className="icon-btn"
                  onClick={() =>
                    store.setDialog({
                      kind: "confirm",
                      title: `Delete project ${p.name}?`,
                      message: "Tasks keep their project label but lose project settings.",
                      danger: true,
                      onConfirm: async () => {
                        await api.deleteProject(p.name).catch((e) =>
                          store.toast({ title: "Delete failed", body: String(e), kind: "error" }),
                        );
                        await store.loadAll();
                      },
                    })
                  }
                >
                  Delete
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <button
        className="btn"
        style={{ marginTop: 8 }}
        onClick={() => {
          setEditing({ ...EMPTY_PROJECT });
          setIsNew(true);
        }}
      >
        Add project
      </button>

      {editing && (
        <div style={{ marginTop: 12, padding: 12, border: "1px solid var(--border)", borderRadius: 8 }}>
          <div className="form-grid">
            <div className="form-row">
              <label>Name</label>
              <input
                value={editing.name ?? ""}
                disabled={!isNew}
                onChange={(e) => setEditing({ ...editing, name: e.target.value })}
              />
            </div>
            <div className="form-row">
              <label>Path</label>
              <input
                value={editing.path ?? ""}
                placeholder="/Users/you/Projects/app"
                onChange={(e) => setEditing({ ...editing, path: e.target.value })}
              />
            </div>
            <div className="form-row">
              <label>Color</label>
              <input
                value={editing.color ?? ""}
                placeholder="#7aa2f7"
                onChange={(e) => setEditing({ ...editing, color: e.target.value })}
              />
            </div>
            <div className="form-row">
              <label>Aliases (comma-separated)</label>
              <input
                value={editing.aliases ?? ""}
                onChange={(e) => setEditing({ ...editing, aliases: e.target.value })}
              />
            </div>
            <div className="form-row">
              <label>Claude config dir</label>
              <input
                value={editing.claude_config_dir ?? ""}
                onChange={(e) => setEditing({ ...editing, claude_config_dir: e.target.value })}
              />
            </div>
            <div className="form-row">
              <label>Use worktrees</label>
              <select
                value={editing.use_worktrees === false ? "no" : "yes"}
                onChange={(e) => setEditing({ ...editing, use_worktrees: e.target.value === "yes" })}
              >
                <option value="yes">yes</option>
                <option value="no">no</option>
              </select>
            </div>
          </div>
          <div className="form-row">
            <label>Instructions</label>
            <textarea
              rows={4}
              value={editing.instructions ?? ""}
              onChange={(e) => setEditing({ ...editing, instructions: e.target.value })}
            />
          </div>
          <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <button className="btn" onClick={() => setEditing(null)}>
              Cancel
            </button>
            <button className="btn primary" onClick={() => void save()}>
              Save project
            </button>
          </div>
        </div>
      )}
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
      <h2>Task types</h2>
      <table>
        <tbody>
          {types.map((t) => (
            <tr key={t.name}>
              <td>{t.name}</td>
              <td style={{ color: "var(--text-dim)" }}>{t.label}</td>
              <td className="actions">
                <button
                  className="icon-btn"
                  onClick={() => {
                    setEditing({ ...t });
                    setIsNew(false);
                  }}
                >
                  Edit
                </button>
                {!t.is_builtin && (
                  <button
                    className="icon-btn"
                    onClick={() =>
                      store.setDialog({
                        kind: "confirm",
                        title: `Delete type ${t.name}?`,
                        message: "Existing tasks keep the label.",
                        danger: true,
                        onConfirm: async () => {
                          await api.deleteType(t.name).catch((e) =>
                            store.toast({ title: "Delete failed", body: String(e), kind: "error" }),
                          );
                          await store.loadAll();
                        },
                      })
                    }
                  >
                    Delete
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <button
        className="btn"
        style={{ marginTop: 8 }}
        onClick={() => {
          setEditing({ name: "", label: "", instructions: "" });
          setIsNew(true);
        }}
      >
        Add type
      </button>

      {editing && (
        <div style={{ marginTop: 12, padding: 12, border: "1px solid var(--border)", borderRadius: 8 }}>
          <div className="form-grid">
            <div className="form-row">
              <label>Name</label>
              <input
                value={editing.name ?? ""}
                disabled={!isNew}
                onChange={(e) => setEditing({ ...editing, name: e.target.value })}
              />
            </div>
            <div className="form-row">
              <label>Label</label>
              <input
                value={editing.label ?? ""}
                onChange={(e) => setEditing({ ...editing, label: e.target.value })}
              />
            </div>
          </div>
          <div className="form-row">
            <label>Instructions (added to executor prompt)</label>
            <textarea
              rows={4}
              value={editing.instructions ?? ""}
              onChange={(e) => setEditing({ ...editing, instructions: e.target.value })}
            />
          </div>
          <div style={{ display: "flex", gap: 8, justifyContent: "flex-end" }}>
            <button className="btn" onClick={() => setEditing(null)}>
              Cancel
            </button>
            <button className="btn primary" onClick={() => void save()}>
              Save type
            </button>
          </div>
        </div>
      )}
    </>
  );
}
