//! End-to-end terminal verification against a real (scratch) tmux server.
//!
//! Proves the requirement-critical path: a grouped tmux view session attached
//! through a real PTY streams live output AND accepts interactive input —
//! exactly what the GUI's executor terminal does, minus the webview.

use std::collections::HashMap;
use std::process::Command;
use std::sync::mpsc;
use std::time::{Duration, Instant};

use base64::Engine;
use taskyou_desktop_lib::pty::{PtyManager, SpawnOptions};
use taskyou_desktop_lib::terminal::prepare_attach;
use tauri::ipc::{Channel, InvokeResponseBody};

const DAEMON_SESSION: &str = "task-daemon-77777";

struct TmuxScratchServer {
    tmpdir: std::path::PathBuf,
}

impl TmuxScratchServer {
    fn tmux(&self, args: &[&str]) -> std::process::Output {
        Command::new("tmux")
            .env("TMUX_TMPDIR", &self.tmpdir)
            .args(args)
            .output()
            .expect("run tmux")
    }
}

impl Drop for TmuxScratchServer {
    fn drop(&mut self) {
        let _ = self.tmux(&["kill-server"]);
        let _ = std::fs::remove_dir_all(&self.tmpdir);
    }
}

fn tmux_available() -> bool {
    Command::new("tmux")
        .arg("-V")
        .output()
        .map(|o| o.status.success())
        .unwrap_or(false)
}

/// Collect decoded PTY output through the channel, as the frontend would.
fn output_channel() -> (
    Channel<taskyou_desktop_lib::pty::PtyEvent>,
    mpsc::Receiver<Vec<u8>>,
) {
    let (tx, rx) = mpsc::channel::<Vec<u8>>();
    let channel = Channel::new(move |body: InvokeResponseBody| {
        if let InvokeResponseBody::Json(json) = body {
            if let Ok(value) = serde_json::from_str::<serde_json::Value>(&json) {
                if value["type"] == "data" {
                    if let Some(b64) = value["data"].as_str() {
                        if let Ok(bytes) = base64::engine::general_purpose::STANDARD.decode(b64) {
                            let _ = tx.send(bytes);
                        }
                    }
                }
            }
        }
        Ok(())
    });
    (channel, rx)
}

fn wait_for_marker(rx: &mpsc::Receiver<Vec<u8>>, marker: &str, timeout: Duration) -> bool {
    let deadline = Instant::now() + timeout;
    let mut collected = Vec::new();
    while Instant::now() < deadline {
        if let Ok(bytes) = rx.recv_timeout(Duration::from_millis(200)) {
            collected.extend_from_slice(&bytes);
            if String::from_utf8_lossy(&collected).contains(marker) {
                return true;
            }
        }
    }
    false
}

#[test]
fn real_terminal_attach_streams_and_accepts_input() {
    if !tmux_available() {
        eprintln!("tmux not installed; skipping");
        return;
    }

    let tmpdir = std::env::temp_dir().join(format!("ty-gui-itest-{}", std::process::id()));
    std::fs::create_dir_all(&tmpdir).unwrap();
    // The library code shells out to plain `tmux`, which resolves the server
    // socket via TMUX_TMPDIR — point the whole process at the scratch server.
    std::env::set_var("TMUX_TMPDIR", &tmpdir);
    let server = TmuxScratchServer { tmpdir };

    // Simulate the daemon: a task-daemon session whose task window runs an
    // interactive process (cat echoes back what the terminal types — same
    // shape as an interactive executor).
    let out = server.tmux(&[
        "new-session",
        "-d",
        "-x",
        "120",
        "-y",
        "30",
        "-s",
        DAEMON_SESSION,
        "-n",
        "_placeholder",
        "tail",
        "-f",
        "/dev/null",
    ]);
    assert!(out.status.success(), "create daemon session: {:?}", out);
    let out = server.tmux(&[
        "new-window",
        "-d",
        "-t",
        DAEMON_SESSION,
        "-n",
        "task-42",
        "sh",
        "-c",
        "echo TY_BOOT_MARKER; exec cat",
    ]);
    assert!(out.status.success(), "create task window: {:?}", out);

    // GUI flow step 1: build the grouped view session focused on the window.
    let plan = prepare_attach(42, DAEMON_SESSION, "task-42").expect("prepare attach");

    // GUI flow step 2: real PTY running the tmux client.
    let manager = PtyManager::default();
    let (channel, rx) = output_channel();
    let pty_id = manager
        .spawn(
            SpawnOptions {
                command: plan.command.clone(),
                cwd: None,
                env: HashMap::new(),
                cols: 120,
                rows: 30,
                kill_tmux_session: Some(plan.view_session.clone()),
            },
            channel,
        )
        .expect("spawn tmux client in PTY");

    // Output path: the window's existing content must reach the terminal.
    assert!(
        wait_for_marker(&rx, "TY_BOOT_MARKER", Duration::from_secs(10)),
        "window content never reached the PTY terminal"
    );

    // Input path: type into the terminal; cat must echo it back through tmux.
    manager
        .write(pty_id, "hello-from-the-gui\r")
        .expect("write keystrokes");
    assert!(
        wait_for_marker(&rx, "hello-from-the-gui", Duration::from_secs(10)),
        "typed input was not echoed back through the executor window"
    );

    // Resize path must not error against a live client.
    manager.resize(pty_id, 100, 28).expect("resize");

    // Teardown: killing the PTY also kills the grouped view session; the
    // daemon session must survive (parity with daemon-owned windows).
    manager.kill(pty_id).expect("kill pty");
    std::thread::sleep(Duration::from_millis(300));

    let sessions = server.tmux(&["list-sessions", "-F", "#{session_name}"]);
    let list = String::from_utf8_lossy(&sessions.stdout).to_string();
    assert!(
        list.contains(DAEMON_SESSION),
        "daemon session was destroyed by the GUI terminal: {list}"
    );
    assert!(
        !list.contains(&plan.view_session),
        "grouped view session leaked after detach: {list}"
    );
}
