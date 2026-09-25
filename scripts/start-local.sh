#!/usr/bin/env bash
# Start the embedded KV-store demo; never touches the default ~/.supernova home.
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
BIN_DIR="$ROOT/.build/bin"
INIT_BIN="$BIN_DIR/supernova-init"
DEMO_BIN="$BIN_DIR/supernova-demo"
HOME_DIR=""
CHAIN_ID=1001
INIT_ONLY=false
REBUILD=false

usage() {
  cat <<'HELP'
Usage: ./scripts/start-local.sh [--home DIR] [--chain-id NUMBER] [--init-only] [--rebuild]

Default: initialize a fresh /tmp/supernova-demo.* home, build binaries if needed,
then run the embedded KV-store demo in the foreground. It does not use ~/.supernova.

  --home DIR          Use a specified home (reuses it if already initialized).
  --chain-id NUMBER   Numeric chain ID for a new home (default: 1001).
  --init-only         Initialize/validate the home without starting the node.
  --rebuild           Force both binaries to rebuild from source.
  -h, --help          Show this message.
HELP
}

while (($#)); do
  case "$1" in
    --home|--chain-id)
      if (($# < 2)); then echo "Missing value for $1" >&2; exit 2; fi
      if [[ "$1" == --home ]]; then HOME_DIR=$2; else CHAIN_ID=$2; fi
      shift 2
      ;;
    --init-only) INIT_ONLY=true; shift ;;
    --rebuild) REBUILD=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ ! "$CHAIN_ID" =~ ^[0-9]+$ ]]; then
  echo "Chain ID must be numeric (got: $CHAIN_ID)" >&2
  exit 2
fi
for tool in go jq; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "Missing required command: $tool" >&2
    exit 1
  fi
done

if [[ -z "$HOME_DIR" ]]; then
  HOME_DIR=$(mktemp -d "${TMPDIR:-/tmp}/supernova-demo.XXXXXX")
else
  mkdir -p -- "$HOME_DIR"
fi
HOME_DIR=$(cd -- "$HOME_DIR" && pwd -P)
GENESIS="$HOME_DIR/config/genesis.json"
mkdir -p "$BIN_DIR"

# Ignore binaries/docs/DB files; rebuild only when Go sources or module files change.
needs_build() {
  local bin=$1
  [[ "$REBUILD" == true || ! -x "$bin" ]] && return 0
  [[ -n "$(find "$ROOT" \( -path "$ROOT/.git" -o -path "$ROOT/.build" -o -path "$ROOT/vendor" \) -prune -o \
    -type f \( -name '*.go' -o -name go.mod -o -name go.sum \) -newer "$bin" -print -quit)" ]]
}

if [[ ! -f "$GENESIS" ]]; then
  if [[ -e "$HOME_DIR/badger" || -e "$HOME_DIR/data/maindb.db" ]]; then
    echo "Refusing to initialize: $HOME_DIR contains chain/application data but no genesis" >&2
    exit 1
  fi
  if needs_build "$INIT_BIN"; then
    echo 'Building initializer (first run can take a while)...'
    (cd "$ROOT" && go build -tags bls12381 -o "$INIT_BIN" ./cmd/supernova)
  fi
  # This CLI currently uses relative file paths internally, even with --home.
  (cd "$HOME_DIR" && "$INIT_BIN" init --home "$HOME_DIR" --key-type bls12_381)
  if [[ ! -f "$GENESIS" ]]; then
    echo "Initializer did not create $GENESIS" >&2
    exit 1
  fi
  UPDATED="$HOME_DIR/config/genesis.updated.json"
  jq --arg chain_id "$CHAIN_ID" \
    '.chain_id = $chain_id | .consensus_params.validator.pub_key_types = ["bls12_381"]' \
    "$GENESIS" > "$UPDATED"
  mv -- "$UPDATED" "$GENESIS"
  echo "Initialized fresh demo chain: $HOME_DIR"
else
  if ! jq -e --arg chain_id "$CHAIN_ID" \
    '.chain_id == $chain_id and .consensus_params.validator.pub_key_types == ["bls12_381"]
     and (.validators | length > 0)
     and all(.validators[]; .pub_key.type == "cometbft/PubKeyBls12_381")' \
    "$GENESIS" >/dev/null; then
    echo "Existing genesis in $HOME_DIR is not a BLS demo chain with chain ID $CHAIN_ID" >&2
    echo "Choose a fresh --home directory; do not rewrite genesis for an existing chain." >&2
    exit 1
  fi
  if [[ ! -f "$HOME_DIR/config/priv_validator_key.json" || ! -f "$HOME_DIR/config/node_key.json" ]]; then
    echo "Existing home is missing validator or node keys: $HOME_DIR" >&2
    exit 1
  fi
  echo "Using existing demo home: $HOME_DIR"
fi

if [[ "$INIT_ONLY" == true ]]; then
  echo "Demo home ready: $HOME_DIR"
  exit 0
fi

if needs_build "$DEMO_BIN"; then
  echo 'Building embedded KV-store demo (first run can take a while)...'
  (cd "$ROOT" && go build -tags bls12381 -o "$DEMO_BIN" main.go sample_app.go)
fi

echo "Starting local demo with home: $HOME_DIR"
echo 'P2P ports: TCP/QUIC 13000, UDP 12000; API: 26657. Press Ctrl-C to stop.'
exec "$DEMO_BIN" -cmt-home "$HOME_DIR"
