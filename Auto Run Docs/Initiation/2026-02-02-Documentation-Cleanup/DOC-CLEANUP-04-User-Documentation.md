# Phase 04: User Documentation

This phase creates comprehensive user-facing documentation in the `docs/` folder that covers configuration, common workflows, provider-specific notes, and troubleshooting. These guides help users successfully deploy and operate Cloud GPU Shopper in production environments.

## Tasks

- [x] Create docs/CONFIGURATION.md with complete environment variable reference:
  - Document all environment variables:
    - `VASTAI_API_KEY`: Vast.ai API key (how to obtain)
    - `TENSORDOCK_API_KEY`: TensorDock API key
    - `TENSORDOCK_AUTH_ID`: TensorDock auth ID
    - `DATABASE_PATH`: SQLite database location
    - `SERVER_PORT`: API server port (default: 8080)
    - Any other env vars found in the codebase
  - Include example `.env` file with comments
  - Security best practices for API key management
  - Configuration for Docker deployment vs local development
  - Add YAML front matter: type: reference, tags: [configuration, deployment]

- [x] Create docs/WORKFLOWS.md with common usage patterns:
  - Workflow 1: Browsing available GPU inventory
    - Using filters (gpu_type, min_vram, max_price)
    - Understanding offer attributes
    - Comparing providers
  - Workflow 2: Provisioning a GPU session
    - Selecting an offer
    - Understanding workload types (llm, training, batch)
    - Setting appropriate reservation hours
    - Handling the SSH private key response
  - Workflow 3: Managing active sessions
    - Monitoring session status
    - Extending session time
    - Signaling completion with /done
    - Force destroying if needed
  - Workflow 4: Cost tracking and budgeting
    - Viewing per-session costs
    - Consumer-level cost aggregation
    - Monthly summaries
  - Add YAML front matter: type: reference, tags: [workflows, usage, guide]

- [x] Create docs/PROVIDERS.md with provider-specific information:
  - Vast.ai section:
    - Account setup and API key generation
    - Pricing model (spot vs on-demand)
    - Instance tagging behavior
    - Known limitations or quirks
  - TensorDock section:
    - Account setup and API credentials
    - Pricing model
    - Any provider-specific features
  - Provider comparison table (pricing, reliability, GPU availability)
  - Tips for choosing between providers
  - Add YAML front matter: type: reference, tags: [providers, vastai, tensordock]

- [x] Create docs/TROUBLESHOOTING.md with common issues and solutions:
  - Connection issues:
    - Server won't start (check port, database path)
    - Can't connect to API (firewall, URL issues)
  - Provider issues:
    - API key authentication failures
    - "No offers available" troubleshooting
    - SSH verification timeouts
  - Session issues:
    - Session stuck in "provisioning" state
    - Session terminated unexpectedly
    - SSH connection refused after provisioning
  - Cost tracking issues:
    - Missing cost data
    - Incorrect cost calculations
  - Debugging tips:
    - Enabling verbose logging
    - Checking /health endpoint
    - Reading /metrics for insights
  - Add YAML front matter: type: reference, tags: [troubleshooting, debugging, support]

- [x] Update docs/API.md with any missing details:
  - Review existing API.md for completeness
  - Add request/response examples if missing
  - Ensure all endpoints are documented
  - Add common error scenarios and their meanings
  - Cross-link to new documentation files

- [x] Commit user documentation:
  - Stage all new docs files
  - Create commit with message: "docs: add user-facing configuration, workflows, providers, and troubleshooting guides"

**Completed 2026-02-02**: Created comprehensive user documentation including:
- `docs/CONFIGURATION.md`: Complete environment variable reference with examples for local dev, Docker, and Docker Compose deployments
- `docs/WORKFLOWS.md`: Four detailed workflow guides covering inventory browsing, session provisioning, session management, and cost tracking
- `docs/PROVIDERS.md`: Provider-specific information for Vast.ai and TensorDock including setup, pricing, and comparison tables
- `docs/TROUBLESHOOTING.md`: Comprehensive troubleshooting guide covering connection, provider, session, and cost issues
- `docs/API.md`: Updated with YAML front matter, added /ready endpoint, /diagnostics endpoint, additional query parameters, stale inventory error handling, and cross-links to other documentation
