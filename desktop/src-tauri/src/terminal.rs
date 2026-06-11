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

fn tmux(args: &[&str]) -> Result<String, String> {
    let out = Command::new("tmux")
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
pub fn prepare_attach(
    task_id: i64,
    daemon_session: &str,
    window: &str,
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

    // Attach, then mark the session for destruction on detach. Chaining via
    // tmux's ";" separator means destroy-unattached only applies once a
    // client is actually connected.
    Ok(AttachPlan {
        command: vec![
            "tmux".into(),
            "attach-session".into(),
            "-t".into(),
            view_session.clone(),
            ";".into(),
            "set-option".into(),
            "destroy-unattached".into(),
            "on".into(),
        ],
        view_session,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_missing_daemon_session() {
        let err = prepare_attach(1, "", "task-1").unwrap_err();
        assert!(err.contains("daemon session"));
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
