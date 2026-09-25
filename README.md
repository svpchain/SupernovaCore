# SuperNovaCore 

SupernovaCore is a Cosmos SDK v2 compatible consensus engine designed to be a drop in replacement for CometBFT v1. 
The following features are implemented in this version of SupernovaCore

1. August, 2024 version of HotStuff 1 latency consensus alogrithm (the original HotStuff-2 consensus
has been running on Meter mainnet with more than 300 physical committee validator ndoes for more than 4 years).

2. BLS signature aggregations for validator votes

3. Dedicated validator messaging subnet to improve the communication efficiency



## Build & Run (local KV-store demo)

Quick start (the script initializes a fresh `/tmp` demo home and builds only when
Go sources change):

```sh
./scripts/start-local.sh
```

Use `./scripts/start-local.sh --home .build/my-demo` to reuse a specific demo
home, or `./scripts/start-local.sh --init-only` to create configuration without
starting the node. Run `./scripts/start-local.sh --help` for other options.

Manual equivalent:

The repository has two entry points. The root `main.go` embeds `sample_app.go` and
uses the `-cmt-home` flag; it does **not** parse a `start` subcommand. The CLI at
`./cmd/supernova` provides `init` and `start`, but its `start` uses a separately
configured ABCI app rather than the root program's embedded KV-store app.

To create a **fresh** demo home without touching `~/.supernova`:

```sh
go build -tags bls12381 -o ./supernova ./cmd/supernova
go build -tags bls12381 -o ./main main.go sample_app.go

DEMO_HOME=$(mktemp -d /tmp/supernova-demo.XXXXXX)
REPO=$(pwd)
# The current CLI wraps CometBFT's init command, whose file paths are relative
# to the working directory. Run it *inside* the selected home.
(cd "$DEMO_HOME" && "$REPO/supernova" init --home "$DEMO_HOME" --key-type bls12_381)

# The generated genesis uses an alphanumeric chain ID and defaults to ed25519
# validator policy. This demo requires a numeric ID and BLS validator policy.
jq '.chain_id = "1001" | .consensus_params.validator.pub_key_types = ["bls12_381"]' \
  "$DEMO_HOME/config/genesis.json" > "$DEMO_HOME/config/genesis.updated.json"
mv "$DEMO_HOME/config/genesis.updated.json" "$DEMO_HOME/config/genesis.json"

./main -cmt-home "$DEMO_HOME"
```

The demo expects network access and binds fixed ports (P2P TCP/QUIC 13000,
UDP 12000, API 26657). Use another home for a new chain; do not edit genesis
for an already initialized home. Do not use the default `init` key type:
Supernova consensus signs votes with BLS12-381 keys.

This is a smoke-test app, not a production-ready chain. The default local demo
keeps a single-validator set so it can continue producing blocks. To test
validator-set changes, supply a valid 96-byte BLS12-381 public key and run the
corresponding additional validator nodes; crash recovery/state sync are not
covered by this example.
