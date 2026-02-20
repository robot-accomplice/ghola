pub mod ghola;
pub mod native;

use async_trait::async_trait;
use std::collections::HashMap;

/// Generic HTTP request passed through the client abstraction.
pub struct Request {
    pub url: String,
    pub method: String,
    pub headers: HashMap<String, String>,
    pub body: Option<String>,
}

/// Generic HTTP response returned by any client implementation.
pub struct Response {
    pub status_code: u16,
    pub headers: HashMap<String, String>,
    pub body: String,
}

/// Trait implemented by both the native reqwest client and the ghola
/// sidecar client.  Scope selects the implementation at startup based
/// on `use_ghola_sidecar` in config.yaml.
#[async_trait]
pub trait HttpClient: Send + Sync {
    async fn send(&self, request: Request) -> anyhow::Result<Response>;
}
