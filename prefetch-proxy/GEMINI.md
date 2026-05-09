# Prefetch Proxy

A specialized reverse proxy for `subconverter` written in Go. It intercepts subscription requests and, for configured target domains, pre-fetches subscription content through a temporary `mihomo` (Clash) instance. This ensures that `subconverter` can process subscriptions that require proxy access or special handling.

## Project Overview

- **Core Functionality**: Acts as a middleware between clients and a `subconverter` backend.
- **Key Technologies**:
    - **Go**: Primary application logic.
    - **Mihomo (Clash)**: Used as a temporary proxy engine for pre-fetching.
    - **Docker**: Containerized deployment with multi-stage builds.
- **Main Components**:
    - `main.go`: Contains the reverse proxy logic, cache management, and `mihomo` process orchestration.
    - `Dockerfile`: Multi-stage build process for the proxy and `mihomo`.
    - `docker-compose-exap.yaml`: Example deployment configuration.

## Architecture & Logic

1.  **Proxying**: Intercepts requests to `/sub`.
2.  **Interception**: If the `url` parameter contains domains listed in `TARGET_DOMAINS`:
    - It fetches the "pre-nodes" from the target subscription.
    - It starts a temporary `mihomo` instance using these nodes.
    - It fetches the actual subscription content through this temporary proxy.
    - It caches the result and returns an internal link to `subconverter`.
3.  **Chain Proxy Injection**: If a `chain_token` is provided and `private_nodes` are configured, it injects these nodes and sets up a chain (relay) configuration in the generated YAML.
4.  **Rule Updates**: Optionally updates rule lists (`DOMAIN-SUFFIX`) and fake-IP filters based on the discovered proxy server domains.

## Building and Running

### Commands
- **Build**: `go build -o prefetch-proxy .`
- **Run**: `./prefetch-proxy` (Configuration via Environment Variables)
- **Docker Build**: `docker build -t prefetch-proxy .`
- **Docker Run**: `docker-compose -f docker-compose-exap.yaml up`

### Environment Variables
- `LISTEN_ADDR`: Address to listen on (default: `:8080`).
- `SUBCONVERTER_URL`: Backend `subconverter` address (default: `http://subconverter:25500`).
- `TARGET_DOMAINS`: Comma-separated list of domains to trigger pre-fetching.
- `MIHOMO_PATH`: Path to the `mihomo` binary.
- `PROXY_PORT`: Port for the temporary `mihomo` SOCKS5 proxy.
- `INTERNAL_BASE_URL`: Base URL used for internal links returned to `subconverter`.
- `CHAIN_TOKEN`: Token required to trigger private node injection.
- `PRIVATE_NODES_PATH`: Path to a YAML file containing private nodes.
- `RULE_LIST_PATH`: Path to append discovered proxy domains as `DOMAIN-SUFFIX`.
- `FAKE_IP_FILTER_PATH`: Path to update fake-IP filters.

## Development Conventions

- **Configuration**: Strictly via environment variables as defined in `initConfig()`.
- **Logging**: Use `log.Printf` for general info and `debugLog` for verbose debugging (enabled via `DEBUG=true`).
- **Dependencies**: `gopkg.in/yaml.v3` for YAML processing.
- **Concurrency**: Use `sync.RWMutex` for cache and `sync.Map`/`sync.Mutex` for file/lock operations to ensure thread safety.
- **Testing**: Currently lacks automated tests. New features should ideally include unit tests for logic in `main.go`.
