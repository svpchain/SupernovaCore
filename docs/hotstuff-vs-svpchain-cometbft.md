# Supernova HotStuff 与 SVPChain CometBFT 对照

> 整理日期：2026-09-23。本文描述当前仓库的实现行为，不等同于 HotStuff 论文的完整协议，也不将 README 的「drop-in replacement」声明视为兼容性证明。
>
> 比较对象：`../svpchain/protocol/go.mod` 中的 `github.com/cometbft/cometbft v0.38.21`，实际由 `replace` 指向 `github.com/svpchain/cometbft v0.0.0-20260921123703-2b58c064db1f`。下文称其为 **SVP Comet fork**。其本地源码通常位于 `$GOMODCACHE/github.com/svpchain/cometbft@v0.0.0-20260921123703-2b58c064db1f/`。Supernova 则依赖 `github.com/cometbft/cometbft/v2 v2.0.0-rc1`；两者不是同一版本的 ABCI 接口。

## 1. 一句话结论

Supernova 用**跨区块提案链、BLS 聚合 QC 和超时 TC**替代 CometBFT 的**同高度 Proposal → Prevote → Precommit → Commit**。它可能改善某些条件下的通信与流水线吞吐，但当前实现不具备 SVP Comet fork 的全部安全、恢复和 ABCI++ 数据语义，尤其不能直接替换依赖 Vote Extension 的 SVPChain 应用。

## 2. 两套正常路径

### SVP Comet fork

```text
高度 H，轮次 R
  提议者组装区块（交易、上一块 ExtendedCommit、证据）
    → ABCI PrepareProposal
    → 广播 Proposal / BlockParts
  验证人校验区块与锁定条件
    → ABCI ProcessProposal
    → Prevote(区块或 nil)
  达到超过 2/3 voting power 的 Prevote
    → 锁定 / 更新有效区块与轮次
    → Precommit(区块或 nil)
  达到超过 2/3 voting power 的非 nil Precommit
    → 保存区块及提交证明
    → ABCI FinalizeBlock → Commit
    → 更新状态，进入 H+1
```

没有取得提案或本轮投票不足时，它保留同一高度并进入新 Round；`LockedBlock/LockedRound`、`ValidBlock/ValidRound` 和 POL 约束是否能改变锁定或重投旧提案。参见 SVP Comet fork 的 `consensus/state.go:1343-1515,1551-1653,1686-1907`、`state/execution.go:110-213,228-369`。

### Supernova

```text
Epoch E，Round R
  选出 round % committeeSize 对应的提议者
    → 从自身 txpool 读取交易
    → ABCI PrepareProposal
    → 基于 QCHigh 对应的父区块创建 DraftBlock
    → 将父区块 QC 放进新 Block，广播 PMProposal
  其他验证人校验签名、父节点、QC、Round/TC
    → ABCI ProcessProposal
    → 满足本地投票条件则广播 PMVote(区块 ID 的 BLS 签名)
  下一轮提议者收票
    → 达到门槛后聚合为 QC
    → 更新 QCHigh，进入下一轮，继续提议子区块
  收到后继提案携带的 QC，满足 Update() 的父子关系
    → 提交尚未提交的祖先区块
    → ABCI FinalizeBlock → Commit
```

**实际提交条件要以代码为准**：`Update(qc)` 找到被 `qc` 证明的 `bPrime`，再取 `bPrime.Justify.QCNode` 为 `b`；当 `bPrime.Parent == b` 时，准备提交相关祖先。例如 B2 带 `QC(B1)`、B3 带 `QC(B2)`，处理 B3 时尝试提交 B1。不要直接把该条件称为「标准 HotStuff 三链规则」，也不能直接套用标准三链的安全证明。参见 `consensus/pacemaker.go:179-221,280-455`。

**吞吐与最终确认延迟是不同指标**：Supernova 的区块可以流水线提议，但某笔交易在 B1 中被提出并不意味着 B1 当即提交；需等待后继区块及相应 QC 推进。

## 3. 数据结构与网络路径

| 维度 | SVP Comet fork | Supernova |
| --- | --- | --- |
| 投票与证明 | Prevote / Precommit；投票集合和 Commit | `PMVote`，BLS 聚合的 `QuorumCert{Epoch, Round, BlockID, AggSig, BitArray}` |
| 故障轮次 | 同高度继续新 Round；可投 nil；POL/锁定规则 | `PMTimeout` 聚合 `TimeoutCert`；随提案附带 TC 跨 Round |
| 未提交区块 | Consensus RoundState、ProposalBlock / BlockParts | `DraftBlock` 父链及 `chain.ProposalMap` |
| 提议者 | 共识状态中的 ValidatorSet 提议者 | Epoch 委员会成员 `round % committeeSize`；KBlock/nonce 参与委员会排序 |
| 共识消息 | CometBFT reactor、Proposal、BlockParts、Vote | 自定义 Proposal/Vote/Timeout/Query，libp2p Gossip topic；已提交块另通过 `EscortedBlock` 传播 |
| 法定门槛 | **严格超过 2/3 voting power** | 当前 `ceil(2N/3)` **按人数**统计；不等价于按 voting power 判断 |

出处：SVP Comet fork `types/vote_set.go:452-481`、`consensus/state.go:2017-2055`；Supernova `consensus/epoch_state.go:38-83,255-264`、`consensus/qc_vote_manager.go:46-113`、`block/quorum_cert.go:19-26`、`consensus/pacemaker_send.go:153-199`。

## 4. ABCI 映射

Supernova 没有运行 CometBFT 的共识状态机；它复用 `proxy.AppConns`，由 `consensus.Executor` 将 HotStuff 的区块和 QC 映射为 ABCI 请求：

| ABCI 方法 | Supernova 调用点 | 需要注意的语义 |
| --- | --- | --- |
| `Info`、`InitChain` | `consensus/handshaker.go` | 启动时查询应用高度、初始化或尝试重放。 |
| `PrepareProposal` | `Executor.PrepareProposal` | 候选交易来自自有 txpool；`LocalLastCommit` 当前未使用传入的完整 `commitInfo`。 |
| `ProcessProposal` | `ValidateProposal` → `Executor.ProcessProposal` | 应用拒绝则不投票；接受时会从本地 txpool 删除提案交易，早于最终提交。 |
| `ExtendVote`、`VerifyVoteExtension` | Executor 有包装方法 | 接收提案时真正调用 `ExtendVote` 的代码被注释；当前投空扩展，未完整走应用验证。 |
| `FinalizeBlock`、`Commit` | `CommitBlock` → `Executor.ApplyBlock` | HotStuff 决定提交后才执行交易；由区块 QC 位图构造 ABCI `DecidedLastCommit`。 |
| `Query` | JSON-RPC → 应用查询连接 | 保留查询调用路径。 |
| `CheckTx` | 示例应用实现了接口 | 自定义 txpool 的 `Add` 不调用应用 `CheckTx`，不同于 SVP Comet fork 的 mempool。 |

相关代码：`node/node.go:145-155,187-193`、`consensus/executor.go:39-119,130-203`、`chain/chain.go:980-1022`、`txpool/tx_pool.go:88-140`。SVP Comet fork 的对应路径在 `state/execution.go:110-213,228-369`、`mempool/clist_mempool.go:308,866`。

应用返回的 `ValidatorUpdates` 会被转换为 BLS 验证人集合并进入后续 Epoch；这并不保证对原有 validator/consensus-param 更新语义完全等价。参见 `consensus/executor.go:192-200`、`consensus/pacemaker_commit.go:38-49`。

## 5. 相比 SVPChain 当前系统，缺少或未接完整的环节

1. **Vote Extension 的端到端链路。** `svpchain/protocol/app/app.go:1898-1899` 注册了 Extend/Verify 处理器；`app/prepare/prepare_proposal.go:78-87` 会使用上一块注入的扩展投票数据产生价格更新。SVP Comet fork 在 `consensus/state.go:2296-2333` 验证扩展，并在 `state/execution.go:147-158` 将完整 `LocalLastCommit` 交给应用。Supernova 当前投空 extension，`PrepareProposal` 也没有交付完整聚合数据（`consensus/pacemaker.go:377-390`、`consensus/executor.go:43-66`）。**这是替换 SVPChain 的直接阻塞项。**
2. **交易准入与重检。** SVP Comet fork 对 mempool 交易执行 ABCI `CheckTx` 和 recheck；Supernova 的自有 txpool 不调用该应用接口。参见 fork `mempool/clist_mempool.go:308,866` 和本仓库 `txpool/tx_pool.go:88-109`。
3. **证据与应用误行为信息。** SVP Comet fork 有 evidence pool、区块证据验证及向应用传递 Misbehavior；Supernova 在 Prepare/Process/Finalize 中暂填空 Misbehavior，并留有 FIXME。参见 fork `node/node.go:369-383`、`state/execution.go:110-153,204-214` 以及本仓库 `consensus/executor.go:57-85,153-163`。
4. **共识 WAL 与崩溃恢复闭环。** SVP Comet fork 持久化共识过程，并通过 WAL/Handshake 恢复；Supernova 有简化的 Handshake/区块重放，但未看到同等级共识 WAL。其 `replayBlocks` 循环调用 `replayBlock(storeBlockHeight, ...)` 而不是 `replayBlock(i, ...)`，需要修复并测试。参见 fork `consensus/state.go:1856-1897`、`consensus/replay.go:462-535` 和本仓库 `consensus/handshaker.go:209-276`。
5. **完整的状态同步和快照路径。** SVP Comet fork 节点装配 block sync、state sync 与 ABCI Snapshot；Supernova 有自定义已提交区块同步，但未看到等价的快照同步闭环。参见 fork `node/node.go:394-417` 和本仓库 `node/node.go:313-379`。
6. **区块与应用执行结果管理。** Fork 检查区块/共识状态、保存 `FinalizeBlockResponse`，提交后持久化 `state.AppHash`、更新 mempool 和事件；Supernova 的 `Executor.validateBlock` 尚为空，且缺少相应完整的结果/状态持久化路径。参见 fork `state/validation.go:15-149`、`state/execution.go:228-369` 与本仓库 `consensus/executor.go:119-130`。

### 单独需要审查的安全边界

- **按 voting power 的 quorum：**非等权验证人时，人数门槛不能代替投票权门槛；还需明确严格大于 2/3 的规则及 QC/TC 的位图验证。参见 `block/block.go:57-112`、`consensus/qc_vote_manager.go:68-71`、`consensus/tc_vote_manager.go:43-84`。
- **防止同高度双签及重启后重签：**SVP Comet fork 的 `privval.FilePV` 持久化最后签名状态（fork `privval/file.go:152-159`）；Supernova 直接使用 `blsMaster` 签票，`bnew.Height >= lastVotingHeight` 允许同高度再次满足投票条件，需要明确持久化防双签策略。参见 `consensus/pacemaker.go:377-400`、`consensus/pacemaker_send.go:52-80`。
- **预期提议者与 QC 目标：**SVP Comet fork 使用本轮预期提议者公钥验证 Proposal（fork `consensus/state.go:2017-2042`）；Supernova 接收路径检查委员会签名，但未见同等级的预期 Round 提议者约束；`VerifyQC` 也未显式比较传入父区块 ID 与 `escortQC.BlockID`。参见 `consensus/pacemaker_send.go:201-221`、`consensus/pacemaker.go:323-367`、`block/block.go:72-112`。
- **锁定和提交规则的安全论证：**`ExtendedFromLastCommitted` 只沿父链检查已提交区块，不能简单等同于 CometBFT 的 `LockedBlock/LockedRound`；Supernova 的 `Update` 提交条件需要配套证明和跨轮/分叉测试。参见 `consensus/pacemaker_assist.go:27-42`、`consensus/pacemaker.go:179-221`。

## 6. AppHash 与落盘顺序：不能简单说“先存区块就是错的”

两者都可能先保存已决定的区块，再调用 ABCI 执行应用；**区别是被保存的内容表示什么，以及崩溃后怎样恢复**：

- SVP Comet fork 在执行区块前，区块头的 `AppHash` 表示**此前的应用状态**，并有 WAL、应用状态存储、FinalizeBlockResponse 和 Handshake 重放配合。参见 fork `state/validation.go:54-58`、`consensus/state.go:1830-1897`、`state/execution.go:298-349`。
- Supernova 在 `chain.AddBlock` 保存区块后才 `ApplyBlock`，随后把返回的 `AppHash` 写到内存中的 `blk.BlockHeader.AppHash`；必须验证持久化区块与内存区块是否一致、重启时 `Info` 高度如何对账，以及多块重放是否正确。参见 `consensus/pacemaker_commit.go:22-36`、`chain/chain.go:319-388`、`consensus/handshaker.go:173-276`。

## 7. 能否直接替换，以及建议顺序

**目前不能按 README 所述当作 SVPChain 的无缝替换。**除了行为差异，SVPChain 使用的是 v0.38.21 fork，Supernova 使用 v2.0.0-rc1；需要验证 ABCI 类型和线协议、应用启动与存储格式、验证人身份及扩展投票语义。不能因为两边都有 `PrepareProposal` / `FinalizeBlock` 就认定兼容。

建议按顺序推进：

1. 明确协议不变量并补齐：预期提议者、QC/TC 目标与权重、锁定/提交安全、防双签与持久化。
2. 对齐 SVPChain 所依赖的 ABCI++：完整 Vote Extension、`LocalLastCommit`、证据、`CheckTx`、Validator/Consensus 参数及响应数据。
3. 建立崩溃与状态恢复测试：在保存区块、`FinalizeBlock`、`Commit`、保存状态各点注入故障；验证 AppHash、区块、高度与验证人集合。
4. 用**同一 SVP 应用、硬件、验证人集合与网络故障模型**测量 TPS、P50/P99 最终确认延迟、每节点带宽、超时恢复时间和重启恢复时间。你们的 Comet fork 已对 mempool lock 等路径做过优化（`svpchain/protocol/go.mod:514-517`；fork `state/execution.go:223-227`），应以其实际行为作为基线，而不是上游默认配置。

这份文档是**静态代码审阅**，尚未运行端到端兼容测试或性能基准，任何“更快／更安全”的结论都需要上述测试和安全审查支持。
