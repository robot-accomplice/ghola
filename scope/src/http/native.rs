use super::{HttpClient, Request, Response};
use async_trait::async_trait;

/// Standard reqwest-based HTTP client used when ghola sidecar is disabled
/// or unavailable.
pub struct NativeHttpClient {
    client: reqwest::Client,
}

impl NativeHttpClient {
    pub fn new() -> Self {
        Self {
            client: reqwest::Client::new(),
        }
    }
}

#[async_trait]
impl HttpClient for NativeHttpClient {
    async fn send(&self, request: Request) -> anyhow::Result<Response> {
        let method: reqwest::Method = request.method.parse()?;
        let mut builder = self.client.request(method, &request.url);

        for (k, v) in &request.headers {
            builder = builder.header(k.as_str(), v.as_str());
        }

        if let Some(body) = request.body {
            builder = builder.body(body);
        }

        let resp = builder.send().await?;
        let status = resp.status().as_u16();

        let mut headers = std::collections::HashMap::new();
        for (k, v) in resp.headers() {
            headers.insert(k.to_string(), v.to_str().unwrap_or("").to_string());
        }

        let body = resp.text().await?;

        Ok(Response {
            status_code: status,
            headers,
            body,
        })
    }
}
