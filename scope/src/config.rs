use anyhow::Context;
use serde::Deserialize;
use std::path::PathBuf;

/// Top-level scope configuration, loaded from `config.yaml`.
#[derive(Debug, Deserialize)]
#[serde(default)]
pub struct Config {
    /// When true, scope routes HTTP requests through the ghola sidecar
    /// (127.0.0.1:18789) instead of using native reqwest.
    pub use_ghola_sidecar: bool,
}

impl Default for Config {
    fn default() -> Self {
        Self {
            use_ghola_sidecar: false,
        }
    }
}

impl Config {
    /// Load configuration from the first `config.yaml` found:
    ///   1. `./config.yaml`  (project-local)
    ///   2. `~/.config/scope/config.yaml`  (user-global)
    ///
    /// Returns `Config::default()` if neither file exists.
    pub fn load() -> anyhow::Result<Self> {
        let candidates = [
            PathBuf::from("config.yaml"),
            home_config_dir().join("config.yaml"),
        ];

        for path in &candidates {
            if path.exists() {
                let raw = std::fs::read_to_string(path)
                    .with_context(|| format!("reading {}", path.display()))?;
                return serde_yaml::from_str(&raw)
                    .with_context(|| format!("parsing {}", path.display()));
            }
        }

        Ok(Self::default())
    }
}

fn home_config_dir() -> PathBuf {
    let home = std::env::var("HOME").unwrap_or_else(|_| ".".into());
    PathBuf::from(home).join(".config").join("scope")
}
