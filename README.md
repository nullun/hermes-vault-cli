# Hermes Vault CLI

A local-first CLI interface for interacting with the [Hermes Vault](https://hermesvault.org) shielded pool on Algorand.

## Overview

`hermes` is a standalone tool that enables private deposits and withdrawals to the Hermes Vault shielded pool. It provides a local-first experience, allowing you to manage your secret notes and perform transactions directly from your terminal.

Your secret notes are the keys to your funds — they encode the amount and two random nonces, and are required to make withdrawals.

## Features

- **Works out of the box** — Connects to public Algorand endpoints by default, no node setup required
- **Private deposits** — Deposit ALGO into the shielded pool with zero-knowledge proofs
- **Private withdrawals** — Withdraw from the pool using your secret notes
- **Note management** — Securely store and retrieve your secret notes
- **Pure Go** — No CGo dependencies, easy cross-compilation

## Installation

### From Source (go install)

You can install the latest version of the `hermes` binary directly using Go:

```bash
go install github.com/nullun/hermes-vault-cli/cmd/hermes@latest
```

Ensure your `GOBIN` (usually `$GOPATH/bin`) is in your `PATH`.

### Pre-built Binaries

Cross-compiled binaries for macOS, Linux, and Windows are available on the [Releases](https://github.com/nullun/hermes-vault-cli/releases) page.

### Building from Source

If you have the repository cloned locally:

```bash
go build -o hermes ./cmd/hermes
```

## Configuration

Hermes works without a configuration file — it connects to public AlgoNode endpoints on mainnet by default.

To customise settings, create a config file using the provided [hermes.conf.example](./hermes.conf.example) as a template:

```bash
cp hermes.conf.example hermes.conf
# Edit hermes.conf with your preferences
```

### Configuration Locations

Hermes looks for the configuration file in the following order:

1. **Explicit flag**: `--config /path/to/hermes.conf`
2. **Environment variable**: `HERMES_CONFIG=/path/to/hermes.conf`
3. **Local directory**: A file named `hermes.conf` in the current working directory.
4. **XDG Config directory**: `~/.config/hermes/config` (Note: the filename is `config` without an extension in this directory).

### Configuration Options

```
Network = mainnet
AlgodUrl =
AlgodToken =
IndexerUrl =
IndexerToken =
UserAddress =
#Mnemonic = word1 word2 word3 ...
```

When `AlgodUrl` is not set, Hermes uses the free AlgoNode public endpoints (`mainnet-api.algonode.cloud` or `testnet-api.algonode.cloud` depending on `Network`). This is fine for interactive use.

If you want to point at your own node:

```bash
AlgodUrl=http://localhost:4001
AlgodToken=your-token-here
```

`IndexerUrl` and `IndexerToken` are optional. When using the default AlgoNode endpoints, the matching indexer is also enabled automatically — syncing many blocks via algod means one request per round, which is excessive against public endpoints. The indexer can fetch the same data in bulk. Hermes tries `algod` first even when an indexer is configured, and only falls back to the indexer when the gap is large enough to warrant it.

`UserAddress` sets a default Algorand address for deposits so you don't need to pass `--address` every time.

`Mnemonic` lets you provide your 25-word mnemonic so deposit transactions are signed automatically without a prompt. Only use this on a personal machine with a secure config file (`chmod 600`).

## Usage

```bash
# Deposit ALGO
hermes deposit 10 --address <your-address>

# Deposit entire balance
hermes deposit --all --address <your-address>

# Dry-run a deposit against the simulation endpoint (no submission)
hermes deposit 10 --address <your-address> --simulate

# Withdraw using a secret note
hermes withdraw <note-text> 5 <recipient-address>

# Dry-run a withdrawal against the simulation endpoint
hermes withdraw <note-text> 5 <recipient-address> --simulate

# Import an existing note from another machine or backup
hermes import <note-text>

# List your saved notes
hermes notes

# View pool statistics
hermes stats
```

## Advanced: Sync & Daemon

Every command automatically syncs the local database before it runs. You do not need to sync manually under normal use.

### Manual sync

If you suspect the local state is stale, you can force a sync:

```bash
hermes sync

# Force indexer use even if algod history is available
hermes sync --force-indexer
```

### Background daemon

The daemon keeps the local database continuously up to date so that commands respond instantly without a catchup step. This is useful if you run commands frequently and want to skip the brief sync delay.

**Important:** If you run a daemon, use your own Algorand node rather than the public AlgoNode endpoints. Public endpoints are fine for occasional interactive commands, but continuous polling from a daemon may be rate-limited.

```bash
# Start background sync daemon
hermes daemon start

# Start daemon and pre-approve indexer fallback if needed
hermes daemon start --allow-indexer

# Check daemon status
hermes daemon status

# Stop the daemon
hermes daemon stop
```

## Sync behavior

Hermes always tries to sync with `algod` first.

If the local watermark is more than 1,000 rounds behind and an indexer is configured, Hermes may fall back to the indexer because older block history may no longer be available from `algod`.

Interactive commands prompt before using the indexer. Non-interactive runs fail with a message telling you to rerun with `--allow-indexer`.

`hermes daemon start` performs that check before spawning the background process. If fallback would be required, it asks up front or accepts `--allow-indexer` so the daemon never needs to prompt after it has detached.

## Security

- Your secret notes are stored locally in `hermes.db`
- Notes are also logged to `hermes.log` for recovery
- Never share your notes — anyone with a note can withdraw the funds
- The log file has mode 0600 (owner-only readable)

## License

MIT
