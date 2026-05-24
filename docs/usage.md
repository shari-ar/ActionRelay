# Usage

ActionRelay is intended to run as a local whole-device route agent. Eligible
requests are queued locally, sent to the server side once per second, processed
as a batch, and returned as one result package.

## Prerequisites

- GitHub repository with the ActionRelay workflow.
- GitHub Actions enabled.
- GitHub token for workflow dispatch, run read, and results branch read.
- Local ActionRelay Go route agent.
- Permission to install or configure a local route on the device.

## First-Time Client Setup

The steps below assume the ActionRelay workflow is already present in the target
GitHub repository and GitHub Actions is enabled for that repository.

Every user needs to:

1. Download the correct release asset for their operating system and CPU.
2. Extract the archive into a local folder.
3. Create a GitHub personal access token (PAT) with the required permissions.
4. Set `ACTIONRELAY_GITHUB_TOKEN` on the machine that will run the client.
5. Initialize ActionRelay against the repository that hosts the workflow.
6. Run a manual fetch test before starting the local route agent.

### Pick The Correct Release Asset

Open the repository on GitHub, open `Releases`, then open the latest release.
Under `Assets`, download the archive that matches the local machine.

- Windows x64: `actionrelay_<version>_windows_amd64.zip` or
  `actionrelay_<version>_windows_amd64.tar.gz`
- Windows ARM64: `actionrelay_<version>_windows_arm64.zip` or
  `actionrelay_<version>_windows_arm64.tar.gz`
- macOS Intel: `actionrelay_<version>_darwin_amd64.tar.gz`
- macOS Apple Silicon: `actionrelay_<version>_darwin_arm64.tar.gz`
- Linux x64: `actionrelay_<version>_linux_amd64.tar.gz`
- Linux ARM64: `actionrelay_<version>_linux_arm64.tar.gz`

If the user is not sure which architecture to choose:

- Windows: open `Settings` -> `System` -> `About` -> `System type`
- macOS: run `uname -m`
- Linux: run `uname -m`

### Windows Setup

1. Open the latest GitHub release and download:
   - `windows_amd64` for almost all Intel/AMD Windows PCs
   - `windows_arm64` only for Windows on ARM devices
2. Create a folder such as `C:\ActionRelay`.
3. Extract the downloaded archive into `C:\ActionRelay`.
   - For `.zip`, right-click the file and choose `Extract All`
   - For `.tar.gz`, use Windows built-in extraction if available, or 7-Zip
4. Confirm the extracted folder contains:
   - `actionrelay.exe`
   - `README.md`
5. Open PowerShell.
6. Move into the extracted folder:

```powershell
cd C:\ActionRelay
```

7. Confirm the binary is present:

```powershell
dir
```

8. Create a fine-grained GitHub PAT for the repository that hosts the
   ActionRelay workflow.
9. Set the token for the current PowerShell session:

```powershell
$env:ACTIONRELAY_GITHUB_TOKEN="your_token_here"
```

10. Persist the token for the current Windows user:

```powershell
[System.Environment]::SetEnvironmentVariable("ACTIONRELAY_GITHUB_TOKEN", "your_token_here", "User")
```

11. Close PowerShell, open a new PowerShell window, then verify the token:

```powershell
[System.Environment]::GetEnvironmentVariable("ACTIONRELAY_GITHUB_TOKEN", "User")
```

12. Return to the client folder:

```powershell
cd C:\ActionRelay
```

13. Initialize ActionRelay with the repository that contains the workflow:

```powershell
.\actionrelay.exe init --repo owner/repo --workflow actionrelay.yml
```

14. Run a manual test before route mode:

```powershell
.\actionrelay.exe fetch https://api.github.com
```

15. If the manual test succeeds, install local route state:

```powershell
.\actionrelay.exe route install --yes --config .actionrelay/config.json
```

16. Start the local route agent:

```powershell
.\actionrelay.exe serve --config .actionrelay/config.json
```

17. In a second PowerShell window, check runtime status:

```powershell
cd C:\ActionRelay
.\actionrelay.exe status --config .actionrelay/config.json
```

18. When finished, remove route state:

```powershell
.\actionrelay.exe route uninstall --yes --config .actionrelay/config.json
```

### macOS Setup

1. Open the latest GitHub release and download:
   - `darwin_amd64` for Intel Macs
   - `darwin_arm64` for Apple Silicon Macs
2. Create a local folder such as `~/actionrelay`.
3. Extract the archive into that folder:

```sh
mkdir -p ~/actionrelay
tar -xzf ~/Downloads/actionrelay_<version>_darwin_<arch>.tar.gz -C ~/actionrelay
```

4. Open Terminal and move into the extracted folder:

```sh
cd ~/actionrelay
```

5. Confirm the binary is present:

```sh
ls -l
```

6. Make sure the binary is executable:

```sh
chmod +x ./actionrelay
```

7. Create a fine-grained GitHub PAT for the repository that hosts the
   ActionRelay workflow.
8. Set the token for the current shell session:

```sh
export ACTIONRELAY_GITHUB_TOKEN='your_token_here'
```

9. Persist the token for future shells:
   - For `zsh`:

```sh
echo "export ACTIONRELAY_GITHUB_TOKEN='your_token_here'" >> ~/.zshrc
source ~/.zshrc
```

   - For `bash`:

```sh
echo "export ACTIONRELAY_GITHUB_TOKEN='your_token_here'" >> ~/.bashrc
source ~/.bashrc
```

10. Verify the token is available:

```sh
echo "$ACTIONRELAY_GITHUB_TOKEN"
```

11. Initialize ActionRelay:

```sh
./actionrelay init --repo owner/repo --workflow actionrelay.yml
```

12. Run a manual fetch test:

```sh
./actionrelay fetch https://api.github.com
```

13. If the manual test succeeds, install local route state:

```sh
./actionrelay route install --yes --config .actionrelay/config.json
```

14. Start the local route agent:

```sh
./actionrelay serve --config .actionrelay/config.json
```

15. In a second Terminal window, check runtime status:

```sh
cd ~/actionrelay
./actionrelay status --config .actionrelay/config.json
```

16. When finished, remove route state:

```sh
./actionrelay route uninstall --yes --config .actionrelay/config.json
```

### Linux Setup

1. Open the latest GitHub release and download:
   - `linux_amd64` for most x64 Linux systems
   - `linux_arm64` for ARM64 Linux systems
2. Create a local folder such as `~/actionrelay`.
3. Extract the archive into that folder:

```sh
mkdir -p ~/actionrelay
tar -xzf ~/Downloads/actionrelay_<version>_linux_<arch>.tar.gz -C ~/actionrelay
```

4. Open a terminal and move into the extracted folder:

```sh
cd ~/actionrelay
```

5. Confirm the binary is present:

```sh
ls -l
```

6. Make sure the binary is executable:

```sh
chmod +x ./actionrelay
```

7. Create a fine-grained GitHub PAT for the repository that hosts the
   ActionRelay workflow.
8. Set the token for the current shell session:

```sh
export ACTIONRELAY_GITHUB_TOKEN='your_token_here'
```

9. Persist the token for future shells:
   - For `bash`:

```sh
echo "export ACTIONRELAY_GITHUB_TOKEN='your_token_here'" >> ~/.bashrc
source ~/.bashrc
```

   - For `zsh`:

```sh
echo "export ACTIONRELAY_GITHUB_TOKEN='your_token_here'" >> ~/.zshrc
source ~/.zshrc
```

10. Verify the token is available:

```sh
echo "$ACTIONRELAY_GITHUB_TOKEN"
```

11. Initialize ActionRelay:

```sh
./actionrelay init --repo owner/repo --workflow actionrelay.yml
```

12. Run a manual fetch test:

```sh
./actionrelay fetch https://api.github.com
```

13. If the manual test succeeds, install local route state:

```sh
./actionrelay route install --yes --config .actionrelay/config.json
```

14. Start the local route agent:

```sh
./actionrelay serve --config .actionrelay/config.json
```

15. In a second terminal, check runtime status:

```sh
cd ~/actionrelay
./actionrelay status --config .actionrelay/config.json
```

16. When finished, remove route state:

```sh
./actionrelay route uninstall --yes --config .actionrelay/config.json
```

## GitHub Token Setup

ActionRelay requires a GitHub personal access token (PAT) on the machine that
runs the local client. The client uses this token to dispatch the GitHub Actions
workflow, poll workflow runs, and read result files from the
`actionrelay-results` branch through the GitHub Contents API.

The current implementation reads the token from the environment variable
`ACTIONRELAY_GITHUB_TOKEN`.

### Required Token Permissions

For a fine-grained PAT, grant:

- Repository access to the ActionRelay repository only.
- `Actions: Read and write`
- `Contents: Read-only`
- `Metadata: Read-only`

### Important Handling Rules

- Copy the token value when GitHub shows it; GitHub may not display it again.
- Do not commit the token into repository files.
- Do not place the token in `.actionrelay/config.json`.
- If the token is exposed in logs, screenshots, or chat, revoke it and create a
  new one.

### Windows PowerShell

Set the token for the current PowerShell session:

```powershell
$env:ACTIONRELAY_GITHUB_TOKEN="your_token_here"
```

Verify the value is available in the current session:

```powershell
echo $env:ACTIONRELAY_GITHUB_TOKEN
```

Persist the token for the current Windows user:

```powershell
[System.Environment]::SetEnvironmentVariable("ACTIONRELAY_GITHUB_TOKEN", "your_token_here", "User")
```

Close and reopen PowerShell, then verify the persisted value:

```powershell
[System.Environment]::GetEnvironmentVariable("ACTIONRELAY_GITHUB_TOKEN", "User")
```

### Linux And macOS Shells

Set the token for the current shell session:

```sh
export ACTIONRELAY_GITHUB_TOKEN='your_token_here'
```

Verify the value is available in the current session:

```sh
echo "$ACTIONRELAY_GITHUB_TOKEN"
```

Persist the token for future `bash` sessions:

```sh
echo "export ACTIONRELAY_GITHUB_TOKEN='your_token_here'" >> ~/.bashrc
source ~/.bashrc
```

Persist the token for future `zsh` sessions:

```sh
echo "export ACTIONRELAY_GITHUB_TOKEN='your_token_here'" >> ~/.zshrc
source ~/.zshrc
```

## Start The Route Agent

Target commands:

```sh
actionrelay init --repo owner/repo --workflow actionrelay.yml
actionrelay route install --yes --config .actionrelay/config.json
actionrelay serve --config .actionrelay/config.json
actionrelay status --config .actionrelay/config.json
```

## Runtime Behavior

The agent should:

- Capture eligible whole-device requests.
- Classify requests and reject unsupported traffic locally.
- Queue requests locally.
- Deduplicate safe identical requests in each batch cycle.
- Serve cache hits for safe responses when still fresh.
- Send one batch every second if the queue is non-empty.
- Enforce strict request-count and batch-byte caps before dispatch.
- Clamp worker concurrency to a low bounded limit.
- Apply temporary local backpressure when GitHub Actions runs are delayed.
- Validate request batches and result packages against protocol shape rules.
- Mark route state for cleanup if the agent exits while route remains installed.
- Receive one result package for the batch.
- Match each result to the original local request.
- Fail unsupported traffic quickly with a local error.

### Reliability Controls

The client supports reliability controls for GitHub Actions delay/failure paths:

- `reliability_mode`:
  - `fail_closed` (default): return explicit request errors when dispatch/run/result
    retrieval fails.
  - `fail_open`: for cacheable requests, return recent stale cached responses when
    recoverable GitHub-side errors occur.
- `stale_if_error_ttl_ms`:
  - Maximum age for stale-cache fallback in `fail_open` mode.
  - `0` disables stale fallback.

These settings are stored in `.actionrelay/config.json` and validated on startup.

## Local Agent Endpoints

Phase 4 exposes a local API from `serve`:

- `GET /healthz` for readiness checks.
- `GET /v1/status` for queue and batch runtime state.
- `POST /v1/requests` for request submission.

`POST /v1/requests` returns one request result object after the request is
processed within a batch cycle.

## Runtime Diagnostics And Troubleshooting

Use:

```sh
actionrelay status --config .actionrelay/config.json
```

The output now includes:

- `agent.runtime.last_dispatch_error_code`: normalized dispatch/runtime error class.
- `agent.runtime.last_dispatch_error_at`: timestamp of most recent dispatch failure.
- `agent.runtime.last_batch_latency_ms`: most recent batch end-to-end latency.
- `agent.runtime.total_fail_open_served`: count of stale fail-open responses served.
- `diagnostics.severity`: `ok`, `warning`, or `critical`.
- `diagnostics.issues`: machine-readable issue hints for quick triage.
- `policy.*`: operator-facing policy and runtime integration view:
  GitHub-only server/domain constraints, reliability mode, proxy listeners, and
  bypass defaults.

Quick triage guide:

- `agent_unreachable`: the local `serve` process is down or not reachable.
- `backpressure_active`: GitHub Actions start delays are currently throttling local submissions.
- `queue_pressure_high`: local queue depth is above 80% of capacity.
- `last_dispatch_error:*`: inspect dispatch/run/result retrieval failures.
- `dispatch_inflight_stuck`: in-flight batch likely stalled and needs operator attention.
- `batch_latency_high`: end-to-end batch latency is elevated for recent traffic.

Long-running service hardening:

- `serve` now runs explicit HTTP server instances for both agent and proxy
  listeners (when proxy mode is enabled).
- Both listeners use conservative idle/read-header timeouts.
- Shutdown now drains both listeners together on signal-driven cancellation,
  reducing orphaned background listener behavior.

## Manual Test Mode

Manual fetch mode remains useful for testing without installing the route:

```sh
actionrelay fetch https://api.example.com/status
```

Use route lifecycle commands to maintain local route state:

```sh
actionrelay route install --yes --config .actionrelay/config.json
actionrelay route uninstall --yes --config .actionrelay/config.json
```

## User Expectations

The one-second batch tick reduces resource usage but adds a small local wait
before dispatch. GitHub Actions can also add queueing, runner startup, artifact,
and polling delays. ActionRelay should be fast for small bounded requests, but it
should not promise real-time VPN latency.
