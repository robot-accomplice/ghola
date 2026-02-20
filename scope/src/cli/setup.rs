use crate::config::Config;
use std::process::Command;

/// Returns true if the `ghola` binary is reachable via PATH.
pub fn ghola_in_path() -> bool {
    Command::new("ghola")
        .arg("--help")
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .status()
        .map(|s| s.success())
        .unwrap_or(false)
}

/// During normal operation, emit a one-line warning if ghola is configured
/// but missing.  Called on every non-setup invocation.
pub fn check_ghola_quietly(cfg: &Config) {
    if cfg.use_ghola_sidecar && !ghola_in_path() {
        eprintln!(
            "\x1b[33m⚠️  Ghola not found in PATH. Sidecar mode will fall back to native HTTP.\n   \
             Run `scope setup` for details.\x1b[0m"
        );
    }
}

/// Full interactive setup report printed by `scope setup`.
pub fn run_full_setup(cfg: &Config) {
    println!("--- [Scope Environment Check] ---\n");

    if ghola_in_path() {
        println!("  ✓ ghola binary found in PATH");
    } else {
        println!(
            "  ⚠️  Ghola not found. Install Ghola (Go-based surgical scout) for stealth\n   \
             analysis, snoop mode, and enhanced RPC reliability.\n   \
             See docs/GHOLA_INTEGRATION.md"
        );
    }

    println!(
        "  {} use_ghola_sidecar = {}",
        if cfg.use_ghola_sidecar { "✓" } else { "○" },
        cfg.use_ghola_sidecar
    );

    println!("\n--- [Setup Complete] ---");
}
