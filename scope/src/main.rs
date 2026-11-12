use anyhow::Result;
use clap::error::ErrorKind;
use clap::{CommandFactory, Parser};

mod cli;
mod config;
mod http;

#[derive(Parser)]
#[command(
    name = "scope",
    version,
    about = "Security reconnaissance and analysis tool"
)]
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
    let cli = Cli::try_parse().unwrap_or_else(|e| match e.kind() {
        ErrorKind::DisplayHelp
        | ErrorKind::DisplayVersion
        | ErrorKind::DisplayHelpOnMissingArgumentOrSubcommand => e.exit(),
        _ => exit_with_arg_error(clap_message(&e.to_string())),
    });
    let cfg = config::Config::load().unwrap_or_default();

    match cli.command {
        Some(Commands::Setup) => {
            cli::setup::run_full_setup(&cfg);
        }
        None => {
            let url = cli.url.unwrap_or_else(|| {
                exit_with_arg_error("error: URL is required (pass as positional argument)")
            });

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

/// Exit status for an argument error. It matches clap's usage-error status.
const ARG_ERROR_EXIT: i32 = 2;

const ANSI_RED: &str = "\x1b[31m";
const ANSI_RESET: &str = "\x1b[0m";

/// Returns the error part of a rendered clap error. clap puts a short usage
/// line after the error; the full help replaces it in `format_arg_error`.
fn clap_message(rendered: &str) -> &str {
    rendered
        .split_once("\nUsage:")
        .map_or(rendered, |(msg, _)| msg)
        .trim_end()
}

/// Puts the help text before and after the error and makes the error red.
fn format_arg_error(help: &str, msg: &str) -> String {
    format!("{help}\n{ANSI_RED}{msg}{ANSI_RESET}\n\n{help}")
}

/// Prints an argument error with the help text around it, then exits.
/// anstream removes the colour when stderr is not a terminal or NO_COLOR is
/// set, and turns on ANSI support in the Windows console.
fn exit_with_arg_error(msg: &str) -> ! {
    let help = Cli::command().render_help().ansi().to_string();
    anstream::eprint!("{}", format_arg_error(&help, msg));
    std::process::exit(ARG_ERROR_EXIT)
}

#[cfg(test)]
mod tests {
    use super::*;

    // The user must see how to type the command before and after the
    // error, and the error must stand out in red.
    #[test]
    fn arg_error_is_red_between_two_help_blocks() {
        let out = format_arg_error("HELP\n", "error: URL is required");
        assert_eq!(
            out,
            "HELP\n\n\x1b[31merror: URL is required\x1b[0m\n\nHELP\n"
        );
    }

    // clap's own short usage line must not appear: the full help replaces it.
    #[test]
    fn clap_message_drops_short_usage() {
        let Err(err) = Cli::try_parse_from(["scope", "--nope"]) else {
            panic!("--nope parsed without error");
        };
        let msg = clap_message(&err.to_string()).to_string();
        assert!(
            msg.starts_with("error: unexpected argument '--nope'"),
            "{msg}"
        );
        assert!(!msg.contains("Usage:"), "{msg}");
    }

    // A clap error that has no usage line stays whole.
    #[test]
    fn clap_message_keeps_text_without_usage() {
        assert_eq!(clap_message("error: bad\n"), "error: bad");
    }
}
