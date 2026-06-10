//! Real PTY management for terminal panes.
//!
//! Each terminal in the GUI is backed by a genuine PTY (via portable-pty, the
//! wezterm PTY layer). For executor terminals the PTY runs a tmux client
//! attached to a *grouped* session that mirrors the daemon's session, so the
//! GUI sees the task window (executor pane + shell pane) live without moving
//! any panes — multiple clients and the daemon can coexist.
//!
//! Output streams to the frontend over a Tauri Channel as base64 chunks
//! (terminal output is raw bytes; base64 avoids UTF-8 splitting issues).

use base64::Engine;
use portable_pty::{native_pty_system, ChildKiller, CommandBuilder, PtySize};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::io::{Read, Write};
use std::process::Command;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use tauri::ipc::Channel;

/// Message sent over the output channel to the frontend.
#[derive(Clone, Serialize)]
#[serde(tag = "type", rename_all = "snake_case")]
pub enum PtyEvent {
    Data { data: String }, // base64-encoded output bytes
    Exit,
}

/// Options for spawning a PTY.
#[derive(Deserialize)]
pub struct SpawnOptions {
    pub command: Vec<String>,
    pub cwd: Option<String>,
    #[serde(default)]
    pub env: HashMap<String, String>,
    pub cols: u16,
    pub rows: u16,
    /// tmux session to kill when this PTY closes (cleanup for grouped views).
    pub kill_tmux_session: Option<String>,
}

struct PtySession {
    master: Box<dyn portable_pty::MasterPty + Send>,
    writer: Box<dyn Write + Send>,
    killer: Box<dyn ChildKiller + Send + Sync>,
    kill_tmux_session: Option<String>,
}

#[derive(Default)]
pub struct PtyManager {
    sessions: Arc<Mutex<HashMap<u64, PtySession>>>,
    next_id: AtomicU64,
}

impl PtyManager {
    pub fn spawn(&self, opts: SpawnOptions, on_event: Channel<PtyEvent>) -> Result<u64, String> {
        if opts.command.is_empty() {
            return Err("command must not be empty".into());
        }

        let pty_system = native_pty_system();
        let pair = pty_system
            .openpty(PtySize {
                rows: opts.rows.max(2),
                cols: opts.cols.max(2),
                pixel_width: 0,
                pixel_height: 0,
            })
            .map_err(|e| format!("openpty: {e}"))?;

        let mut cmd = CommandBuilder::new(&opts.command[0]);
        cmd.args(&opts.command[1..]);
        if let Some(cwd) = &opts.cwd {
            cmd.cwd(cwd);
        }
        cmd.env("TERM", "xterm-256color");
        cmd.env("COLORTERM", "truecolor");
        for (k, v) in &opts.env {
            cmd.env(k, v);
        }

        let mut child = pair
            .slave
            .spawn_command(cmd)
            .map_err(|e| format!("spawn {:?}: {e}", opts.command))?;
        drop(pair.slave);

        let killer = child.clone_killer();
        let mut reader = pair
            .master
            .try_clone_reader()
            .map_err(|e| format!("clone reader: {e}"))?;
        let writer = pair
            .master
            .take_writer()
            .map_err(|e| format!("take writer: {e}"))?;

        let id = self.next_id.fetch_add(1, Ordering::SeqCst) + 1;
        let kill_tmux_session = opts.kill_tmux_session.clone();

        self.sessions.lock().unwrap().insert(
            id,
            PtySession {
                master: pair.master,
                writer,
                killer,
                kill_tmux_session,
            },
        );

        // Reader thread: stream output until EOF, then notify exit + clean up.
        let sessions = Arc::clone(&self.sessions);
        std::thread::spawn(move || {
            let mut buf = [0u8; 8192];
            loop {
                match reader.read(&mut buf) {
                    Ok(0) | Err(_) => break,
                    Ok(n) => {
                        let data = base64::engine::general_purpose::STANDARD.encode(&buf[..n]);
                        if on_event.send(PtyEvent::Data { data }).is_err() {
                            break;
                        }
                    }
                }
            }
            // Reap the child to avoid zombies; it has exited (or we're tearing down).
            let _ = child.wait();
            let _ = on_event.send(PtyEvent::Exit);
            if let Some(session) = sessions.lock().unwrap().remove(&id) {
                cleanup_session(session);
            }
        });

        Ok(id)
    }

    pub fn write(&self, id: u64, data: &str) -> Result<(), String> {
        let mut sessions = self.sessions.lock().unwrap();
        let session = sessions.get_mut(&id).ok_or("pty not found")?;
        session
            .writer
            .write_all(data.as_bytes())
            .map_err(|e| format!("write: {e}"))?;
        session.writer.flush().map_err(|e| format!("flush: {e}"))
    }

    pub fn resize(&self, id: u64, cols: u16, rows: u16) -> Result<(), String> {
        let sessions = self.sessions.lock().unwrap();
        let session = sessions.get(&id).ok_or("pty not found")?;
        session
            .master
            .resize(PtySize {
                rows: rows.max(2),
                cols: cols.max(2),
                pixel_width: 0,
                pixel_height: 0,
            })
            .map_err(|e| format!("resize: {e}"))
    }

    pub fn kill(&self, id: u64) -> Result<(), String> {
        let session = self.sessions.lock().unwrap().remove(&id);
        match session {
            Some(session) => {
                cleanup_session(session);
                Ok(())
            }
            None => Ok(()), // already gone (e.g. reader thread cleaned up)
        }
    }

    /// Kill all PTYs (app shutdown).
    pub fn kill_all(&self) {
        let sessions: Vec<PtySession> = {
            let mut map = self.sessions.lock().unwrap();
            map.drain().map(|(_, s)| s).collect()
        };
        for session in sessions {
            cleanup_session(session);
        }
    }
}

fn cleanup_session(mut session: PtySession) {
    let _ = session.killer.kill();
    // The grouped tmux view session has destroy-unattached on, so it dies when
    // the client detaches; kill-session is belt-and-braces (e.g. SIGKILLed client).
    if let Some(name) = &session.kill_tmux_session {
        let _ = Command::new("tmux")
            .args(["kill-session", "-t", name])
            .output();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn drain_channel() -> Channel<PtyEvent> {
        Channel::new(|_| Ok(()))
    }

    #[test]
    fn spawn_write_kill_roundtrip() {
        let manager = PtyManager::default();
        let id = manager
            .spawn(
                SpawnOptions {
                    command: vec!["/bin/cat".into()],
                    cwd: None,
                    env: HashMap::new(),
                    cols: 80,
                    rows: 24,
                    kill_tmux_session: None,
                },
                drain_channel(),
            )
            .expect("spawn cat");

        manager.write(id, "hello\n").expect("write");
        manager.resize(id, 100, 30).expect("resize");
        manager.kill(id).expect("kill");

        // Second kill is a no-op, not an error.
        manager.kill(id).expect("idempotent kill");
    }

    #[test]
    fn spawn_rejects_empty_command() {
        let manager = PtyManager::default();
        let err = manager
            .spawn(
                SpawnOptions {
                    command: vec![],
                    cwd: None,
                    env: HashMap::new(),
                    cols: 80,
                    rows: 24,
                    kill_tmux_session: None,
                },
                drain_channel(),
            )
            .unwrap_err();
        assert!(err.contains("empty"));
    }

    #[test]
    fn write_to_unknown_pty_errors() {
        let manager = PtyManager::default();
        assert!(manager.write(42, "x").is_err());
        assert!(manager.resize(42, 80, 24).is_err());
    }
}
