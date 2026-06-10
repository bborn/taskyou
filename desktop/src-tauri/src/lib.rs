pub mod pty;
pub mod supervisor;
pub mod terminal;

use pty::{PtyEvent, PtyManager, SpawnOptions};
use serde::Serialize;
use std::collections::HashMap;
use std::process::Command;
use supervisor::{DesktopConfig, Supervisor, SupervisorStatus};
use tauri::ipc::Channel;
use tauri::{Manager, RunEvent, State};

// --- PTY commands ---

#[tauri::command]
fn pty_spawn(
    manager: State<'_, PtyManager>,
    opts: SpawnOptions,
    on_event: Channel<PtyEvent>,
) -> Result<u64, String> {
    manager.spawn(opts, on_event)
}

#[tauri::command]
fn pty_write(manager: State<'_, PtyManager>, id: u64, data: String) -> Result<(), String> {
    manager.write(id, &data)
}

#[tauri::command]
fn pty_resize(manager: State<'_, PtyManager>, id: u64, cols: u16, rows: u16) -> Result<(), String> {
    manager.resize(id, cols, rows)
}

#[tauri::command]
fn pty_kill(manager: State<'_, PtyManager>, id: u64) -> Result<(), String> {
    manager.kill(id)
}

// --- Executor terminal attach ---

#[derive(Serialize)]
struct AttachResult {
    pty_id: u64,
    view_session: String,
}

/// Create a grouped tmux view of the task's daemon window and attach a real
/// PTY client to it. Returns the PTY id streaming to `on_event`.
#[tauri::command]
fn attach_task_terminal(
    manager: State<'_, PtyManager>,
    task_id: i64,
    daemon_session: String,
    window: String,
    cols: u16,
    rows: u16,
    on_event: Channel<PtyEvent>,
) -> Result<AttachResult, String> {
    let plan = terminal::prepare_attach(task_id, &daemon_session, &window)?;
    let pty_id = manager.spawn(
        SpawnOptions {
            command: plan.command,
            cwd: None,
            env: HashMap::new(),
            cols,
            rows,
            kill_tmux_session: Some(plan.view_session.clone()),
        },
        on_event,
    )?;
    Ok(AttachResult {
        pty_id,
        view_session: plan.view_session,
    })
}

// --- Supervisor commands ---

#[tauri::command]
fn supervisor_status(supervisor: State<'_, Supervisor>) -> SupervisorStatus {
    supervisor.status()
}

#[tauri::command]
fn supervisor_ensure(supervisor: State<'_, Supervisor>) -> Result<SupervisorStatus, String> {
    supervisor.ensure()
}

#[tauri::command]
fn supervisor_get_config(supervisor: State<'_, Supervisor>) -> DesktopConfig {
    supervisor.config.lock().unwrap().clone()
}

#[tauri::command]
fn supervisor_set_config(
    supervisor: State<'_, Supervisor>,
    config: DesktopConfig,
) -> Result<(), String> {
    supervisor::save_config(&config)?;
    *supervisor.config.lock().unwrap() = config;
    Ok(())
}

// --- Opening things on the host ---

/// Open a URL or path with the system default handler (macOS `open`).
#[tauri::command]
fn open_external(target: String) -> Result<(), String> {
    if !(target.starts_with("http://") || target.starts_with("https://") || target.starts_with('/'))
    {
        return Err("only absolute paths and http(s) URLs can be opened".into());
    }
    Command::new("open")
        .arg(&target)
        .status()
        .map_err(|e| e.to_string())
        .and_then(|s| {
            if s.success() {
                Ok(())
            } else {
                Err("open failed".into())
            }
        })
}

/// Open a directory in the user's code editor; falls back to Finder.
#[tauri::command]
fn open_in_editor(path: String) -> Result<(), String> {
    if !path.starts_with('/') {
        return Err("path must be absolute".into());
    }
    // Prefer VS Code's CLI when installed, then $EDITOR-style GUI fallbacks.
    for editor in ["code", "cursor", "zed"] {
        if Command::new("which")
            .arg(editor)
            .output()
            .map(|o| o.status.success())
            .unwrap_or(false)
        {
            return Command::new(editor)
                .arg(&path)
                .status()
                .map_err(|e| e.to_string())
                .and_then(|s| {
                    if s.success() {
                        Ok(())
                    } else {
                        Err(format!("{editor} failed"))
                    }
                });
        }
    }
    open_external(path)
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_notification::init())
        .manage(PtyManager::default())
        .manage(Supervisor::new(supervisor::load_config()))
        .invoke_handler(tauri::generate_handler![
            pty_spawn,
            pty_write,
            pty_resize,
            pty_kill,
            attach_task_terminal,
            supervisor_status,
            supervisor_ensure,
            supervisor_get_config,
            supervisor_set_config,
            open_external,
            open_in_editor,
        ])
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app, event| {
            if let RunEvent::Exit = event {
                // Tear down terminals (kills grouped tmux view sessions) and
                // any sidecars we spawned.
                app.state::<PtyManager>().kill_all();
                app.state::<Supervisor>().shutdown();
            }
        });
}
