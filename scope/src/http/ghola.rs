use super::{HttpClient, Request, Response};
use anyhow::{Context, bail};
use async_trait::async_trait;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::io::{Read, Write};
use std::net::TcpStream;
use std::path::PathBuf;
use std::process::Command;
use std::time::Duration;

const SIDECAR_ADDR: &str = "127.0.0.1:18789";
const SIDECAR_URL: &str = "http://127.0.0.1:18789";

#[derive(Serialize)]
struct BridgeRequest {
    url: String,
    method: String,
    headers: HashMap<String, String>,
    body: String,
    drift: bool,
    ghost: bool,
    retries: i32,
}

#[derive(Deserialize)]
struct BridgeResponse {
    status_code: u16,
    headers: HashMap<String, String>,
    body: String,
    #[serde(default)]
    error: String,
}

/// HTTP client that forwards requests to the ghola sidecar bridge
/// running on 127.0.0.1:18789.  When `stealth` is true, the bridge
/// applies temporal drift and ghost signing to every request.
pub struct GholaHttpClient {
    client: reqwest::Client,
    stealth: bool,
    token: String,
}

impl GholaHttpClient {
    pub fn new(stealth: bool, token: String) -> Self {
        Self {
            client: reqwest::Client::builder()
                .timeout(Duration::from_secs(30))
                .build()
                .expect("failed to build reqwest client"),
            stealth,
            token,
        }
    }

    /// Check whether the sidecar answers its health check. If not, spawn
    /// `ghola --serve` and wait for it to become ready. Then read the bridge
    /// token. The bridge writes the token before it serves its first request,
    /// so a health answer means the token file is current.
    pub fn ensure_ready(stealth: bool) -> anyhow::Result<Self> {
        if !is_bridge_running() {
            spawn_sidecar()?;
            wait_for_bridge(Duration::from_secs(5))?;
        }
        Ok(Self::new(stealth, read_token()?))
    }
}

#[async_trait]
impl HttpClient for GholaHttpClient {
    async fn send(&self, request: Request) -> anyhow::Result<Response> {
        let bridge_req = BridgeRequest {
            url: request.url,
            method: request.method,
            headers: request.headers,
            body: request.body.unwrap_or_default(),
            drift: self.stealth,
            ghost: self.stealth,
            retries: 0,
        };

        let resp = self
            .client
            .post(SIDECAR_URL)
            .bearer_auth(&self.token)
            .json(&bridge_req)
            .send()
            .await
            .context("failed to reach ghola sidecar")?;

        let bridge_resp: BridgeResponse = resp.json().await.context("invalid sidecar response")?;

        if !bridge_resp.error.is_empty() {
            bail!("sidecar error: {}", bridge_resp.error);
        }

        Ok(Response {
            status_code: bridge_resp.status_code,
            headers: bridge_resp.headers,
            body: bridge_resp.body,
        })
    }
}

/// Path of the bridge token file. It must match `bridge.TokenFile` in
/// ghola (`internal/bridge/guard.go`): `os.UserConfigDir()/ghola/bridge-token`.
fn token_path() -> anyhow::Result<PathBuf> {
    let dir = dirs::config_dir().context("cannot find the user config directory")?;
    Ok(dir.join("ghola").join("bridge-token"))
}

fn read_token() -> anyhow::Result<String> {
    let path = token_path()?;
    let token = std::fs::read_to_string(&path).with_context(|| {
        format!(
            "cannot read the ghola bridge token at {} (a ghola bridge without token support may be running; stop it and retry)",
            path.display()
        )
    })?;
    Ok(token.trim().to_owned())
}

/// Report whether the bridge answers `GET /health` with status 200. A plain
/// TCP connect is not enough: the port opens before the token file is written.
fn is_bridge_running() -> bool {
    const CRLF: &str = "\r\n";
    let Ok(mut stream) = TcpStream::connect_timeout(
        &SIDECAR_ADDR.parse().expect("valid socket addr"),
        Duration::from_millis(200),
    ) else {
        return false;
    };
    let _ = stream.set_read_timeout(Some(Duration::from_millis(500)));
    let probe = format!(
        "GET /health HTTP/1.1{CRLF}Host: {SIDECAR_ADDR}{CRLF}Connection: close{CRLF}{CRLF}"
    );
    let mut status_line = [0u8; 12]; // "HTTP/1.1 200"
    stream.write_all(probe.as_bytes()).is_ok()
        && stream.read_exact(&mut status_line).is_ok()
        && &status_line[9..] == b"200"
}

fn spawn_sidecar() -> anyhow::Result<()> {
    Command::new("ghola")
        .arg("--serve")
        .stdout(std::process::Stdio::null())
        // Nobody reads the pipe, and it closes when the Child handle drops.
        // A bridge log line to a closed pipe would then kill ghola (SIGPIPE).
        .stderr(std::process::Stdio::null())
        .spawn()
        .context("failed to spawn ghola --serve (is ghola installed?)")?;
    Ok(())
}

fn wait_for_bridge(timeout: Duration) -> anyhow::Result<()> {
    let start = std::time::Instant::now();
    while start.elapsed() < timeout {
        if is_bridge_running() {
            return Ok(());
        }
        std::thread::sleep(Duration::from_millis(100));
    }
    bail!("ghola sidecar did not become ready within {timeout:?}")
}
