//! Sidecar supervision: make sure `ty serve` (HTTP API) and `ty daemon`
//! (executor loop) are running. Children spawned by the GUI are terminated on
//! exit; pre-existing processes are left alone.

use serde::{Deserialize, Serialize};
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use std::time::Duration;

pub const DEFAULT_PORT: u16 = 8484;

#[derive(Clone, Serialize, Deserialize)]
pub struct DesktopConfig {
    #[serde(default = "default_port")]
    pub port: u16,
    /// Explicit path to the ty binary; when unset it is auto-discovered.
    #[serde(default)]
    pub ty_path: Option<String>,
}

fn default_port() -> u16 {
    DEFAULT_PORT
}

impl Default for DesktopConfig {
    fn default() -> Self {
        Self {
            port: DEFAULT_PORT,
            ty_path: None,
        }
    }
}

#[derive(Serialize)]
pub struct SupervisorStatus {
    pub port: u16,
    pub ty_path: Option<String>,
    pub server_running: bool,
    pub daemon_running: bool,
    pub server_managed: bool,
    pub daemon_managed: bool,
}

#[derive(Default)]
pub struct Supervisor {
    pub config: Mutex<DesktopConfig>,
    server_child: Mutex<Option<Child>>,
    daemon_child: Mutex<Option<Child>>,
}

fn config_path() -> Option<PathBuf> {
    dirs::config_dir().map(|d| d.join("taskyou-desktop").join("config.json"))
}

pub fn load_config() -> DesktopConfig {
    let Some(path) = config_path() else {
        return DesktopConfig::default();
    };
    std::fs::read_to_string(path)
        .ok()
        .and_then(|s| serde_json::from_str(&s).ok())
        .unwrap_or_default()
}

pub fn save_config(config: &DesktopConfig) -> Result<(), String> {
    let path = config_path().ok_or("no config dir")?;
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent).map_err(|e| e.to_string())?;
    }
    let data = serde_json::to_string_pretty(config).map_err(|e| e.to_string())?;
    std::fs::write(path, data).map_err(|e| e.to_string())
}

/// Locate the ty binary: explicit config, PATH, then common install spots.
pub fn discover_ty(config: &DesktopConfig) -> Option<String> {
    if let Some(path) = &config.ty_path {
        if !path.is_empty() && std::path::Path::new(path).exists() {
            return Some(path.clone());
        }
    }

    if let Ok(out) = Command::new("which").arg("ty").output() {
        if out.status.success() {
            let path = String::from_utf8_lossy(&out.stdout).trim().to_string();
            if !path.is_empty() {
                return Some(path);
            }
        }
    }

    let home = dirs::home_dir()?;
    let candidates = [
        home.join("go/bin/ty"),
        PathBuf::from("/opt/homebrew/bin/ty"),
        PathBuf::from("/usr/local/bin/ty"),
        home.join("Projects/workflow/bin/ty"),
    ];
    candidates
        .iter()
        .find(|p| p.exists())
        .map(|p| p.to_string_lossy().into_owned())
}

pub fn server_healthy(port: u16) -> bool {
    let url = format!("http://127.0.0.1:{port}/api/status");
    ureq::AgentBuilder::new()
        .timeout(Duration::from_millis(1500))
        .build()
        .get(&url)
        .call()
        .map(|r| r.status() == 200)
        .unwrap_or(false)
}

pub fn daemon_running(ty: &str) -> bool {
    Command::new(ty)
        .args(["daemon", "status"])
        .output()
        .map(|out| String::from_utf8_lossy(&out.stdout).contains("Daemon running"))
        .unwrap_or(false)
}

impl Supervisor {
    pub fn new(config: DesktopConfig) -> Self {
        Self {
            config: Mutex::new(config),
            ..Default::default()
        }
    }

    pub fn status(&self) -> SupervisorStatus {
        let config = self.config.lock().unwrap().clone();
        let ty = discover_ty(&config);
        SupervisorStatus {
            port: config.port,
            server_running: server_healthy(config.port),
            daemon_running: ty.as_deref().map(daemon_running).unwrap_or(false),
            server_managed: self.server_child.lock().unwrap().is_some(),
            daemon_managed: self.daemon_child.lock().unwrap().is_some(),
            ty_path: ty,
        }
    }

    /// Ensure both sidecars are running, spawning them when needed.
    pub fn ensure(&self) -> Result<SupervisorStatus, String> {
        let config = self.config.lock().unwrap().clone();
        let ty = discover_ty(&config).ok_or(
            "ty binary not found — install TaskYou or set its path in Settings",
        )?;

        if !server_healthy(config.port) {
            let child = Command::new(&ty)
                .args(["serve", "--port", &config.port.to_string()])
                .stdin(Stdio::null())
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .spawn()
                .map_err(|e| format!("start ty serve: {e}"))?;
            *self.server_child.lock().unwrap() = Some(child);

            // Wait for the API to come up.
            let deadline = std::time::Instant::now() + Duration::from_secs(10);
            while std::time::Instant::now() < deadline {
                if server_healthy(config.port) {
                    break;
                }
                std::thread::sleep(Duration::from_millis(250));
            }
            if !server_healthy(config.port) {
                return Err(format!("ty serve did not become healthy on port {}", config.port));
            }
        }

        if !daemon_running(&ty) {
            // `ty daemon` runs in the foreground and writes its own pid file.
            let child = Command::new(&ty)
                .arg("daemon")
                .stdin(Stdio::null())
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .spawn()
                .map_err(|e| format!("start ty daemon: {e}"))?;
            *self.daemon_child.lock().unwrap() = Some(child);
        }

        Ok(self.status())
    }

    /// Kill only the children we spawned.
    pub fn shutdown(&self) {
        for slot in [&self.server_child, &self.daemon_child] {
            if let Some(mut child) = slot.lock().unwrap().take() {
                let _ = child.kill();
                let _ = child.wait();
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_config_has_port() {
        let config = DesktopConfig::default();
        assert_eq!(config.port, DEFAULT_PORT);
    }

    #[test]
    fn config_roundtrips_through_json() {
        let config = DesktopConfig {
            port: 9999,
            ty_path: Some("/usr/local/bin/ty".into()),
        };
        let json = serde_json::to_string(&config).unwrap();
        let parsed: DesktopConfig = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed.port, 9999);
        assert_eq!(parsed.ty_path.as_deref(), Some("/usr/local/bin/ty"));
    }

    #[test]
    fn server_healthy_false_when_nothing_listens() {
        // Port 1 is essentially never listening locally.
        assert!(!server_healthy(1));
    }
}
