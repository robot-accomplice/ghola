# C4 Context Diagram

Shows ghola in the context of the systems and actors it interacts with.

```mermaid
graph TB
    operator["Operator<br/><i>Blockchain forensic analyst</i>"]

    ghola["Ghola<br/><i>High-performance HTTP client<br/>for stealthy data acquisition</i>"]

    rpc["Blockchain RPCs<br/><i>Ethereum, Base, Solana<br/>JSON-RPC endpoints</i>"]

    http["HTTP Targets<br/><i>Any HTTP/HTTPS endpoint</i>"]

    fs["Local Filesystem<br/><i>Upload source files,<br/>downloaded output</i>"]

    operator -->|"Invokes via CLI"| ghola
    ghola -->|"Chain-aware HTTP requests"| rpc
    ghola -->|"HTTP requests with<br/>stealth features"| http
    ghola -->|"Read upload files /<br/>write output files"| fs
```

## Actors

- **Operator**: A blockchain forensic analyst or security researcher who uses ghola as a tactical HTTP scout for data acquisition and endpoint reconnaissance.

## External Systems

| System | Description |
|--------|-------------|
| Blockchain RPCs | JSON-RPC endpoints for Ethereum, Base, Solana. Ghola pre-fills chain-specific headers via `--chain`. |
| HTTP Targets | Any HTTP/HTTPS endpoint. Ghola supports stealth features (drift, ghost signing) to avoid detection. |
| Local Filesystem | Source for `--upload-file` payloads and destination for `--output` / `--wget` downloads. |
