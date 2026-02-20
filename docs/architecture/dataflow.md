# Request Lifecycle Dataflow

Shows how data flows through ghola from CLI invocation to output.

```mermaid
flowchart TD
    A[CLI Invocation] --> B[Parse Flags and Validate]
    B --> C{Upload file?}
    C -->|Yes| D[Read file into body]
    C -->|No| E[Use inline data or empty body]
    D --> F[Build Request]
    E --> F
    F --> G{Concurrency > 1?}
    G -->|Yes| H["Spawn N goroutines<br/>(sync.Once for output)"]
    G -->|No| I[Single fetchURL call]
    H --> I
    I --> J{Apply drift jitter?}
    J -->|Yes| K["Sleep random ms<br/>(crypto/rand)"]
    J -->|No| L[Set headers + method]
    K --> L
    L --> M{Ghost mode?}
    M -->|Yes| N["Compute SHA256 identity<br/>Add X-Ghola-Identity header"]
    M -->|No| O{Basic auth?}
    N --> O
    O -->|Yes| P[Add Authorization header]
    O -->|No| Q[Send via fasthttp]
    P --> Q
    Q --> R{Success?}
    R -->|No| S{Retries left?}
    S -->|Yes| T["Exponential backoff<br/>(base * 2^attempt ms)"] --> I
    S -->|No| U[Return error / exit SendFailed]
    R -->|Yes| V{Snoop mode?}
    V -->|Yes| W[Print security posture report]
    V -->|No| X{Output to file?}
    X -->|Yes| Y["Write body to file (0644)"]
    X -->|No| Z[Print to stdout]
```

## Key Data Transformations

| Stage | Input | Output |
|-------|-------|--------|
| Flag parsing | `os.Args` | `*config.Options` |
| Upload file read | File path | `Options.Data` (body string) |
| Ghost signing | Timestamp + URL | SHA256 hex → `X-Ghola-Identity` header |
| Basic auth | `user:password` | Base64 → `Authorization: Basic ...` header |
| Chain shortcut | Chain name (eth/base/sol) | `X-Chain: {chain}` header |
| Drift jitter | Max ms | Crypto-random sleep duration |
| Retry backoff | Base ms + attempt count | `base * 2^(attempt-1)` ms sleep |

## Error Flow

Errors propagate upward as return values. Only `cmd/ghola/main.go` converts errors into exit codes:

| Condition | Exit Code |
|-----------|-----------|
| Invalid flags or missing URL | `1` (BadFlag) |
| HTTP request failed after all retries | `2` (SendFailed) |
| Upload file unreadable | `3` (ReadFileFailed) |
| Output file unwritable | `4` (WriteFileFailed) |
| Success | `0` (NoError) |
