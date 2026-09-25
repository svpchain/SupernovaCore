# SVPChain → SupernovaCore: first migration pass (2026-09-24)

**Target architecture:** SVPChain remains the application. SupernovaCore replaces the
consensus engine, not the application's proposal handlers. This work does **not**
reuse old testnet blocks. No sidecar consensus package is added to the SVPChain
repository; the previous experimental `protocol/consensus/supernova` bridge was
removed from that working tree.

## Ported here

- On vote-extension-enabled heights (currently read from genesis feature params),
  the validator calls ABCI `ExtendVote` with the candidate block's hash, height,
  time, transactions, proposer, and last commit. Received votes verify BLS vote
  extension signatures before calling ABCI `VerifyVoteExtension`; application
  rejection prevents forming the QC. Disabled heights do not call the app.
  Files: `consensus/pacemaker.go`, `consensus/pacemaker_vote_extensions.go`.
- A QC's `ExtendedCommitInfo` now carries votes in committee order **including
  absent votes**. `PrepareProposal` uses these real extension votes instead of a
  fabricated vote for the proposer. When vote extensions are enabled, a missing
  extended commit fails closed rather than manufacturing Slinky oracle data.
  Files: `consensus/qc_vote_manager.go`, `consensus/pacemaker.go`,
  `consensus/executor.go`.
- QC and TC formation and verification now require **strictly more than two
  thirds of the committee voting power**, rather than a number of signers. QC
  verification also checks the target BlockID and bitset length. Files:
  `block/block.go`, `consensus/qc_vote_manager.go`,
  `consensus/tc_vote_manager.go`, `consensus/pacemaker_assist.go`.
- SupernovaCore passes transactions returned by the application directly into
  the proposed block, retaining the order. SVPChain's existing
  `PrepareProposal` handler (in `protocol/app/prepare/prepare_proposal.go`)
  creates `MsgProposedOperations`, price updates, premium votes and bridge
  acknowledgements. These are **application** transactions, not consensus-side
  messages; duplicating their generation in SupernovaCore would give two
  competing algorithms. The executor bounds the returned byte size and rejects
  empty transactions. Its request time matches the proposed block time.

## Not connected / must not be mistaken for a usable replacement

1. `svpchaind` is still wired to its CometBFT v0.38 fork; SupernovaCore currently
   uses v2 ABCI types. That ABI, startup/configuration, and CLI/RPC paths require
   a deliberate integration in the SupernovaCore executable. Do not turn on the
   prototype against SVPChain without a compatibility test harness.
2. Existing SVP consensus keys and validator updates are Ed25519; this engine
   assumes BLS keys. BLS identity and validator-update mapping, persistent
   anti-equivocation state, and cross-epoch weighted quorums still need
   multi-node safety tests before using SVP staking/slashing.
3. SVP enables Vote Extensions through consensus-param upgrades. The first-pass
   implementation knows only the genesis enable height. It does **not** process
   `FinalizeBlockResponse.ConsensusParamUpdates`; Slinky cannot be enabled later
   without first persisting and applying those updates.
4. `commitInfoCache` is in-memory and not replicated in QC or block storage. A
   newly selected proposer after restart may lack signed extension data. The
   current enabled-height behavior fails instead of proposing incorrect data.
   Persist/recover each signed vote and bind it to the QC and committee.
5. The app currently sees the last committed state, while HotStuff can propose
   descendants of *uncommitted* blocks. SVP's order book and pricing handlers
   are stateful; disabling asynchronous execution **does not** eliminate this
   parent-state requirement. Need a synchronous speculative state per Draft
   branch (or a separately proven alternative commit rule) before enabling SVP
   proposals. Do not wait to commit the parent before proposing its child under
   the current commit rule: that can deadlock.
6. Slinky v0.38 verifies extended commit data and signatures under CometBFT
   rules. BLS signatures here use Supernova's `epoch|blockID|extension` domain.
   Signature verification, encoding, absent-vote representation, and height
   alignment must be reconciled against Slinky's checks, not just copied bytes.
7. Evidence, mempool `CheckTx`, transaction reinsertion on fork, full AppHash
   persistence/replay, snapshot sync, nonnumeric chain ID handling, and
   IBC/light-client semantics remain separate blockers. None are claimed done.

**Acceptance for next stage:** multi-node SVP app tests with explicit application
state at each proposal height; complete `ExtendVote` → QC → `LocalLastCommit` →
`MsgProposedOperations`/Slinky-injected transactions → `ProcessProposal` →
`FinalizeBlock`/`Commit` and identical AppHash after restart. Only then switch
`svpchaind` startup to this consensus engine. Existing history compatibility
and asynchronous `FinalizeBlock`/`PrepareProposal` are explicitly out of scope.
