use super::{HttpClient, Request, Response};
use anyhow::{Context, bail};
use async_trait::async_trait;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::net::TcpStream;
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
}

impl GholaHttpClient {
    pub fn new(stealth: bool) -> Self {
        Self {
            client: reqwest::Client::builder()
                .timeout(Duration::from_secs(30))
                .build()
                .expect("failed to build reqwest client"),
            stealth,
        }
    }

    /// Check whether the sidecar is reachable. If not, spawn `ghola --serve`
    /// and wait for it to become ready. Returns a configured client on success.
    pub fn ensure_ready(stealth: bool) -> anyhow::Result<Self> {
        if !is_bridge_running() {
            spawn_sidecar()?;
            wait_for_bridge(Duration::from_secs(5))?;
        }
        Ok(Self::new(stealth))
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

fn is_bridge_running() -> bool {
    TcpStream::connect_timeout(
        &SIDECAR_ADDR.parse().expect("valid socket addr"),
        Duration::from_millis(200),
    )
    .is_ok()
}

fn spawn_sidecar() -> anyhow::Result<()> {
    Command::new("ghola")
        .arg("--serve")
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::piped())
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
