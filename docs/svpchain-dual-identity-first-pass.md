# SVP dual-identity / vote-extension first pass

SVP staking retains its Ed25519 consensus pubkey/address and validator power.
The operator/account key remains separate. The new, one-to-one BLS key is only
for Supernova HotStuff votes, message authentication, QC and TC. An optional
`consensus.AppIdentities` mapping binds the two public keys and power; missing
bindings, duplicate addresses, or mismatched power fail closed. `ConfigureSVPIdentity`
configures an Ed25519 signer on the pacemaker **before Start**. It is not yet
wired to the production CLI, genesis format, a remote signer, or validator updates.

When configured, the node signs the application extension with the SVP/CometBFT
v0.38 canonical bytes (delimited protobuf extension/height/round/chain ID)
using the Ed25519 consensus key. The BLS vote, BLS extension signatures, and
BLS PMVote message signature continue to serve HotStuff. The BLS message now
authenticates the additional Ed25519 signature; timeout retransmission carries
it. Incoming votes validate both schemes before reaching the app or counting
for a QC. The extended commit supplies Ed25519 addresses/signatures in
power/address order; the QC signer bitset remains in BLS committee order.
`PrepareProposal`, `ProcessProposal`, `ExtendVote` and `FinalizeBlock` use the
application address view. Validator/consensus-param updates in `FinalizeBlock`
are rejected in this mode before `Commit` until dual-key updates are designed.
The existing single-key BLS demo is unchanged when the mapping is absent.

This does **not** make the SVP application usable yet: the v0.38/v2 ABCI adapter,
genesis/key distribution and proof of BLS-to-staking binding, anti-equivocation
persistence/remote signing, durable extended commits, parent Draft state, and
validator/param upgrades still need to be implemented and tested with the real
SVP app. No existing testnet history or async pipeline is supported. The
Ed25519 verifier here uses Go's standard library for locally generated signatures;
review exact CometBFT verifier semantics for external/remote signers before
production. A fixed canonical byte vector was cross-checked against the local
SVP CometBFT fork.
