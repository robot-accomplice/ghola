# C4 Component Diagram

Shows the internal package structure of the ghola binary.

```mermaid
graph TB
    subgraph binary ["ghola binary"]
        cmd["cmd/ghola<br/><i>Entrypoint</i><br/>Parses args, orchestrates<br/>the request pipeline,<br/>handles os.Exit"]

        cfg["internal/config<br/><i>Configuration</i><br/>CLI flag parsing,<br/>Options struct,<br/>validation, chain shortcuts"]

        client["internal/client<br/><i>HTTP Client</i><br/>Request execution,<br/>retry + backoff,<br/>drift jitter, ghost signing,<br/>concurrent requests"]

        output["internal/output<br/><i>Output</i><br/>Response rendering,<br/>file/stdout dispatch,<br/>snoop mode report"]
    end

    cmd -->|"ParseFlags(args)"| cfg
    cmd -->|"FetchURL / RunConcurrent"| client
    cmd -->|"ProcessResponse / RunSnoop"| output
    client -.->|"passes *Request, *Response"| output
```

## Package Responsibilities

| Package | Responsibility | Key Types/Functions |
|---------|---------------|---------------------|
| `cmd/ghola` | Process entrypoint. Wires config, client, and output together. Only place that calls `os.Exit`. | `main()`, `run()` |
| `internal/config` | CLI flag parsing into a strongly-typed `Options` struct. All validation happens here. | `Options`, `ParseFlags()`, `ExitCode` |
| `internal/client` | HTTP transport layer. Handles retries, drift jitter, ghost identity signing, basic auth, and concurrent execution. Accepts a `Doer` interface for testability. | `FetchURL()`, `RunConcurrent()`, `Doer` |
| `internal/output` | Response rendering to stdout or file. Snoop mode security posture report. Accepts `io.Writer` for testability. | `ProcessResponse()`, `RunSnoop()` |

## Design Decisions

- **No global state**: `Options` is passed explicitly to every function.
- **Dependency injection**: `client.Doer` and `io.Writer` allow full test isolation without network calls or file I/O.
- **`internal/` packages**: Prevents external consumers from depending on implementation details. The public contract is the CLI itself.
