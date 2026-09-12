//! Executor terminal attach: grouped tmux session orchestration.
//!
//! To show a task's executor window we never move panes (join-pane is the
//! TUI's approach and is destructive across clients). Instead we create a
//! throwaway *grouped* session targeting the daemon session — grouped sessions
//! share windows but keep an independent current-window — select the task's
//! window in it, and attach a real PTY tmux client to it. `destroy-unattached`
//! makes tmux garbage-collect the view session when the client goes away.

use std::process::Command;
use std::sync::atomic::{AtomicU64, Ordering};

static VIEW_COUNTER: AtomicU64 = AtomicU64::new(0);

#[derive(Debug)]
pub struct AttachPlan {
    /// Name of the grouped view session (kill on close).
    pub view_session: String,
    /// Command to run inside the PTY.
    pub command: Vec<String>,
}

/// Whether the window containing `pane` is currently zoomed.
fn zoomed(pane: &str) -> bool {
    tmux(&["display-message", "-p", "-t", pane, "#{window_zoomed_flag}"])
        .map(|out| out.trim() == "1")
        .unwrap_or(false)
}

/// The `-L` name of the tmux server ty runs agents on, from the value that
/// chooses it: `None` means tmux's own default server.
fn socket_from(choice: &str) -> Option<String> {
    match choice.trim() {
        "" | "default" => None,
        name => Some(name.to_string()),
    }
}

/// The agent server's socket, by the same rule as ty's Go side
/// (internal/tmuxctl): the TASKYOU_TMUX_SOCKET override, else the choice ty
/// records next to its database, else tmux's default server. Read on every
/// call: ty records the choice the first time it touches tmux, which can be
/// after this app started.
fn agent_socket() -> Option<String> {
    if let Ok(choice) = std::env::var("TASKYOU_TMUX_SOCKET") {
        return socket_from(&choice);
    }
    let db = std::env::var("WORKTREE_DB_PATH")
        .ok()
        .filter(|p| !p.is_empty())
        .map(std::path::PathBuf::from)
        .or_else(|| {
            std::env::var_os("HOME")
                .map(|home| std::path::PathBuf::from(home).join(".local/share/task/tasks.db"))
        })?;
    let choice = std::fs::read_to_string(db.parent()?.join("tmux-socket")).ok()?;
    socket_from(&choice)
}

/// `-L <socket>` for the agent server, or nothing for the default server.
pub(crate) fn socket_args() -> Vec<String> {
    match agent_socket() {
        Some(socket) => vec!["-L".into(), socket],
        None => Vec::new(),
    }
}

fn tmux(args: &[&str]) -> Result<String, String> {
    let out = Command::new("tmux")
        .args(socket_args())
        .args(args)
        .output()
        .map_err(|e| format!("tmux not available: {e}"))?;
    if !out.status.success() {
        return Err(format!(
            "tmux {} failed: {}",
            args.first().unwrap_or(&""),
            String::from_utf8_lossy(&out.stderr).trim()
        ));
    }
    Ok(String::from_utf8_lossy(&out.stdout).into_owned())
}

/// Prepare a grouped tmux view session focused on `window` (a window name like
/// "task-42" or index) of `daemon_session`, and return the attach plan.
///
/// When `pane` is given (a global tmux pane ID like "%42"), that pane is
/// focused and zoomed so the GUI shows exactly one pane instead of the raw
/// side-by-side tmux layout — the Agent and Shell tabs each attach their own
/// view zoomed to their own pane. Zoom is a window-level flag shared across
/// the session group, so callers attach at most one view per window at a time
/// (the GUI mounts only the active tab).
pub fn prepare_attach(
    task_id: i64,
    daemon_session: &str,
    window: &str,
    pane: Option<&str>,
) -> Result<AttachPlan, String> {
    if daemon_session.is_empty() {
        return Err("task has no daemon session".into());
    }

    let view_session = format!(
        "ty-gui-{}-{}-{}",
        task_id,
        std::process::id(),
        VIEW_COUNTER.fetch_add(1, Ordering::SeqCst)
    );

    // Grouped session: shares the daemon session's windows, independent focus.
    tmux(&[
        "new-session",
        "-d",
        "-s",
        &view_session,
        "-t",
        daemon_session,
    ])?;

    // View-session chrome: no status bar inside the GUI pane, mouse support
    // for pane focus/scroll. (destroy-unattached is set during attach — see
    // below — to avoid tmux GC'ing the session before the client connects.)
    let _ = tmux(&["set-option", "-t", &view_session, "status", "off"]);
    let _ = tmux(&["set-option", "-t", &view_session, "mouse", "on"]);

    // Focus the task's window inside the view session. Window names/indexes
    // are shared across the session group.
    let target = format!("{}:{}", view_session, window);
    if let Err(e) = tmux(&["select-window", "-t", &target]) {
        let _ = tmux(&["kill-session", "-t", &view_session]);
        return Err(e);
    }

    // Single-pane view: focus the requested pane and zoom it. Reset any
    // existing zoom first — `resize-pane -Z` toggles, and a previous attach
    // may have left the window zoomed on a different pane.
    if let Some(pane) = pane.filter(|p| !p.is_empty()) {
        if zoomed(pane) {
            let _ = tmux(&["resize-pane", "-Z", "-t", pane]);
        }
        if let Err(e) = tmux(&["select-pane", "-t", pane]) {
            let _ = tmux(&["kill-session", "-t", &view_session]);
            return Err(e);
        }
        let multi_pane = tmux(&["display-message", "-p", "-t", pane, "#{window_panes}"])
            .map(|out| out.trim() != "1")
            .unwrap_or(false);
        if multi_pane {
            let _ = tmux(&["resize-pane", "-Z", "-t", pane]);
        }
    }

    // Attach, then mark the session for destruction on detach. Chaining via
    // tmux's ";" separator means destroy-unattached only applies once a
    // client is actually connected.
    // The PTY's client must reach the same server the view session is on.
    let mut command: Vec<String> = vec!["tmux".into()];
    command.extend(socket_args());
    command.extend([
        "attach-session".into(),
        "-t".into(),
        view_session.clone(),
        ";".into(),
        "set-option".into(),
        "destroy-unattached".into(),
        "on".into(),
    ]);
    Ok(AttachPlan {
        command,
        view_session,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_missing_daemon_session() {
        let err = prepare_attach(1, "", "task-1", None).unwrap_err();
        assert!(err.contains("daemon session"));
    }

    #[test]
    fn rejects_missing_daemon_session_with_pane() {
        let err = prepare_attach(1, "", "task-1", Some("%5")).unwrap_err();
        assert!(err.contains("daemon session"));
    }

    #[test]
    fn socket_choice_matches_ty() {
        assert_eq!(socket_from("taskyou\n"), Some("taskyou".to_string()));
        assert_eq!(socket_from("default"), None);
        assert_eq!(socket_from("  "), None);
    }

    #[test]
    fn view_session_names_are_unique() {
        let a = format!(
            "ty-gui-1-{}-{}",
            std::process::id(),
            VIEW_COUNTER.fetch_add(1, Ordering::SeqCst)
        );
        let b = format!(
            "ty-gui-1-{}-{}",
            std::process::id(),
            VIEW_COUNTER.fetch_add(1, Ordering::SeqCst)
        );
        assert_ne!(a, b);
    }
}
