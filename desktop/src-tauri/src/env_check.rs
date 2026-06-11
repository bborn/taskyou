//! Environment checks and PATH repair.
//!
//! Finder-launched apps inherit a minimal PATH (`/usr/bin:/bin:...`) without
//! Homebrew or user bins — so tmux, claude, and friends would be "missing"
//! even when installed, and the bundled `ty` couldn't spawn tmux. We repair
//! PATH from the user's login shell once at startup, then probe the tools the
//! app actually needs and report them to the first-run check in the UI.

use serde::Serialize;
use std::process::Command;

/// Executor CLIs taskyou knows how to drive, in display order.
const EXECUTOR_CLIS: &[&str] = &["claude", "codex", "gemini", "opencode", "pi", "openclaw"];

/// Replace this process's PATH with the login shell's PATH so child processes
/// (ty → tmux → executors) resolve tools the way the user's terminal does.
pub fn fix_path_from_login_shell() {
    #[cfg(unix)]
    {
        let shell = std::env::var("SHELL").unwrap_or_else(|_| "/bin/zsh".to_string());
        // Markers guard against shell startup noise (motd, prompts, plugins).
        let out = Command::new(&shell)
            .args(["-ilc", "printf '__PATH_START__%s__PATH_END__' \"$PATH\""])
            .output();
        if let Ok(out) = out {
            let text = String::from_utf8_lossy(&out.stdout);
            if let (Some(start), Some(end)) =
                (text.find("__PATH_START__"), text.find("__PATH_END__"))
            {
                let path = &text[start + "__PATH_START__".len()..end];
                if !path.trim().is_empty() {
                    std::env::set_var("PATH", path.trim());
                }
            }
        }
    }
}

fn which(binary: &str) -> Option<String> {
    let out = Command::new("which").arg(binary).output().ok()?;
    if !out.status.success() {
        return None;
    }
    let path = String::from_utf8_lossy(&out.stdout).trim().to_string();
    (!path.is_empty()).then_some(path)
}

#[derive(Serialize)]
pub struct ToolCheck {
    pub name: String,
    pub path: Option<String>,
}

#[derive(Serialize)]
pub struct EnvironmentReport {
    /// Path to tmux, if found. Required for task execution and terminals.
    pub tmux: Option<String>,
    pub tmux_version: Option<String>,
    /// Known executor CLIs and whether each is installed.
    pub executors: Vec<ToolCheck>,
}

impl EnvironmentReport {
    pub fn ready(&self) -> bool {
        self.tmux.is_some() && self.executors.iter().any(|e| e.path.is_some())
    }
}

pub fn check_environment() -> EnvironmentReport {
    let tmux = which("tmux");
    let tmux_version = tmux.as_ref().and_then(|_| {
        Command::new("tmux")
            .arg("-V")
            .output()
            .ok()
            .filter(|o| o.status.success())
            .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
    });

    EnvironmentReport {
        tmux,
        tmux_version,
        executors: EXECUTOR_CLIS
            .iter()
            .map(|name| ToolCheck {
                name: (*name).to_string(),
                path: which(name),
            })
            .collect(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn which_finds_standard_tools() {
        // `ls` exists on every unix box; a nonsense name doesn't.
        assert!(which("ls").is_some());
        assert!(which("definitely-not-a-real-binary-xyz").is_none());
    }

    #[test]
    fn report_ready_requires_tmux_and_an_executor() {
        let report = EnvironmentReport {
            tmux: None,
            tmux_version: None,
            executors: vec![ToolCheck {
                name: "claude".into(),
                path: Some("/usr/local/bin/claude".into()),
            }],
        };
        assert!(!report.ready());

        let report = EnvironmentReport {
            tmux: Some("/opt/homebrew/bin/tmux".into()),
            tmux_version: Some("tmux 3.5a".into()),
            executors: vec![ToolCheck {
                name: "claude".into(),
                path: None,
            }],
        };
        assert!(!report.ready());

        let report = EnvironmentReport {
            tmux: Some("/opt/homebrew/bin/tmux".into()),
            tmux_version: None,
            executors: vec![ToolCheck {
                name: "claude".into(),
                path: Some("/x/claude".into()),
            }],
        };
        assert!(report.ready());
    }

    #[test]
    fn fix_path_preserves_a_nonempty_path() {
        fix_path_from_login_shell();
        assert!(!std::env::var("PATH").unwrap_or_default().is_empty());
    }
}
