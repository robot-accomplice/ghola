use anyhow::{Context, Result};
use clap::Parser;

mod cli;
mod config;
mod http;

#[derive(Parser)]
#[command(name = "scope", version, about = "Security reconnaissance and analysis tool")]
struct Cli {
    /// Enable stealth mode (drift + ghost signing via ghola sidecar)
    #[arg(long, global = true)]
    stealth: bool,

    /// Target URL to analyze
    url: Option<String>,

    #[command(subcommand)]
    command: Option<Commands>,
}

#[derive(clap::Subcommand)]
enum Commands {
    /// Check environment and display setup recommendations
    Setup,
}

#[tokio::main]
async fn main() -> Result<()> {
    let cli = Cli::parse();
    let cfg = config::Config::load().unwrap_or_default();

    match cli.command {
        Some(Commands::Setup) => {
            cli::setup::run_full_setup(&cfg);
        }
        None => {
            let url = cli
                .url
                .context("URL is required (pass as positional argument)")?;

            cli::setup::check_ghola_quietly(&cfg);

            let client: Box<dyn http::HttpClient> = if cfg.use_ghola_sidecar {
                match http::ghola::GholaHttpClient::ensure_ready(cli.stealth) {
                    Ok(c) => {
                        eprintln!("[scope] using ghola sidecar");
                        Box::new(c)
                    }
                    Err(e) => {
                        eprintln!(
                            "[scope] ghola sidecar unavailable ({e}), \
                             falling back to native HTTP"
                        );
                        Box::new(http::native::NativeHttpClient::new())
                    }
                }
            } else {
                Box::new(http::native::NativeHttpClient::new())
            };

            let response = client
                .send(http::Request {
                    url,
                    method: "GET".into(),
                    headers: Default::default(),
                    body: None,
                })
                .await?;

            if response.status_code >= 400 {
                eprintln!(
                    "[scope] HTTP {} ({})",
                    response.status_code,
                    response
                        .headers
                        .get("content-type")
                        .map(String::as_str)
                        .unwrap_or("unknown"),
                );
            }
            println!("{}", response.body);
        }
    }

    Ok(())
}
