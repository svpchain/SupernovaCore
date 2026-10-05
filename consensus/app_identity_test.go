package consensus

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"testing"

	"github.com/OffchainLabs/prysm/v6/crypto/bls"
	v2 "github.com/cometbft/cometbft/api/cometbft/abci/v2"
	cmttypes "github.com/cometbft/cometbft/v2/types"
	"github.com/meterio/supernova/block"
	"github.com/meterio/supernova/types"
)

func TestCanonicalSVPExtensionSignBytes(t *testing.T) {
	// CometBFT v0.38 CanonicalVoteExtension: bytes extension, fixed64 height
	// and round, bytes chainID, with a delimited protobuf length prefix.
	want, _ := hex.DecodeString("200a0568656c6c6f11010000000000000019030000000000000022057376702d31")
	got := canonicalExtensionSignBytes("svp-1", 1, 3, []byte("hello"))
	if !bytes.Equal(got, want) {
		t.Fatalf("canonical sign bytes: %x, want %x", got, want)
	}
	if bytes.Equal(got, canonicalExtensionSignBytes("svp-1", 1, 4, []byte("hello"))) || bytes.Equal(got, canonicalExtensionSignBytes("other", 1, 3, []byte("hello"))) {
		t.Fatal("signature domain omitted height/round/chainID")
	}
}

func makeAppIdentityFixture(t *testing.T) (*types.BlsMaster, *cmttypes.Validator, ed25519.PrivateKey, *AppIdentities) {
	t.Helper()
	master := types.NewBlsMasterWithRandKey()
	validator := cmttypes.NewValidator(master.CmtPubKey, 10)
	seed := bytes.Repeat([]byte{1}, ed25519.SeedSize)
	key := ed25519.NewKeyFromSeed(seed)
	ids, err := NewAppIdentities("svp-1", []AppIdentity{{master.CmtPubKey.Bytes(), key.Public().(ed25519.PublicKey), 10}})
	if err != nil {
		t.Fatal(err)
	}
	return master, validator, key, ids
}

func TestIdentityMappingRejectsDuplicatesAndMissingValidators(t *testing.T) {
	master, v, key, ids := makeAppIdentityFixture(t)
	if err := ids.ValidateCommittee(cmttypes.NewValidatorSet([]*cmttypes.Validator{v})); err != nil {
		t.Fatal(err)
	}
	if _, err := NewAppIdentities("svp-1", []AppIdentity{{master.CmtPubKey.Bytes(), key.Public().(ed25519.PublicKey), 10}, {master.CmtPubKey.Bytes(), key.Public().(ed25519.PublicKey), 10}}); err == nil {
		t.Fatal("duplicate identities accepted")
	}
	badPower := cmttypes.NewValidator(master.CmtPubKey, 11)
	if err := ids.ValidateCommittee(cmttypes.NewValidatorSet([]*cmttypes.Validator{badPower})); err == nil {
		t.Fatal("mismatched staking voting power accepted")
	}
	other := types.NewBlsMasterWithRandKey()
	if err := ids.ValidateCommittee(cmttypes.NewValidatorSet([]*cmttypes.Validator{cmttypes.NewValidator(other.CmtPubKey, 1)})); err == nil {
		t.Fatal("missing mapping accepted")
	}
	p := &Pacemaker{blsMaster: master}
	if err := p.ConfigureSVPIdentity(ids, ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, 32))); err == nil {
		t.Fatal("wrong local signer accepted")
	}
	if err := p.ConfigureSVPIdentity(ids, key); err != nil {
		t.Fatal(err)
	}
}

func TestSVPExtensionSignatureAndQCUseAppIdentity(t *testing.T) {
	master, v, key, ids := makeAppIdentityFixture(t)
	committee := cmttypes.NewValidatorSet([]*cmttypes.Validator{v})
	v = committee.Validators[0]
	conn := &extensionConn{status: v2.VERIFY_VOTE_EXTENSION_STATUS_ACCEPT}
	p := &Pacemaker{blsMaster: master, appIdentities: ids, appPrivateKey: key, logger: slog.Default(), epochState: &EpochState{epoch: 1, committee: committee}, executor: &Executor{proxyApp: conn, appIdentities: ids}, voteExtensionEnableHeight: 1}
	var id types.Bytes32
	binary.BigEndian.PutUint32(id[:4], 2)
	ext := []byte("oracle")
	blsExtSig := master.SignMessage(types.GetMsgHashForVoteExtension(1, id[:], ext)).Marshal()
	nonRpSig := master.SignMessage(nil).Marshal()
	appSig := ed25519.Sign(key, canonicalExtensionSignBytes("svp-1", 2, 3, ext))
	if !p.verifyApplicationVoteExtension(0, id, 3, ext, blsExtSig, appSig, nil, nonRpSig) {
		t.Fatal("valid dual signature rejected")
	}
	if !bytes.Equal(conn.request.ValidatorAddress, appAddress(key.Public().(ed25519.PublicKey))) {
		t.Fatal("app received BLS address")
	}
	conn.request = nil
	if p.verifyApplicationVoteExtension(0, id, 4, ext, blsExtSig, appSig, nil, nonRpSig) || conn.request != nil {
		t.Fatal("wrong round reached app")
	}
	if p.verifyApplicationVoteExtension(0, id, 3, ext, blsExtSig, nil, nil, nonRpSig) {
		t.Fatal("missing Ed25519 signature accepted")
	}
	sig, err := bls.SignatureFromBytes(master.SignMessage(id[:]).Marshal())
	if err != nil {
		t.Fatal(err)
	}
	manager := NewQCVoteManager(committee)
	manager.appIdentities = ids
	if qc, _ := manager.AddVerifiedVote(0, v, 1, 3, id, sig, ext, blsExtSig, nil, nil, nonRpSig); qc != nil {
		t.Fatal("vote without app signature counted")
	}
	qc, info := manager.AddVerifiedVote(0, v, 1, 3, id, sig, ext, blsExtSig, appSig, nil, nonRpSig)
	if qc == nil || info == nil || !bytes.Equal(info.Votes[0].ExtensionSignature, appSig) || !bytes.Equal(info.Votes[0].Validator.Address, appAddress(key.Public().(ed25519.PublicKey))) {
		t.Fatal("QC did not preserve Ed25519 SVP vote extension view")
	}
	msg := &block.PMVoteMessage{VoteExtension: ext, AppExtensionSignature: appSig}
	oldHash := msg.GetMsgHash()
	msg.AppExtensionSignature = bytes.Repeat([]byte{3}, len(appSig))
	if oldHash == msg.GetMsgHash() {
		t.Fatal("BLS message signature does not bind app signature")
	}
}

func TestSVPExtendedCommitSortedByAppPowerAndAddress(t *testing.T) {
	var masters []*types.BlsMaster
	var keys []ed25519.PrivateKey
	var validators []*cmttypes.Validator
	var entries []AppIdentity
	for i, power := range []int64{10, 30, 20} {
		master := types.NewBlsMasterWithRandKey()
		key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{byte(i + 1)}, 32))
		masters = append(masters, master)
		keys = append(keys, key)
		validators = append(validators, cmttypes.NewValidator(master.CmtPubKey, power))
		entries = append(entries, AppIdentity{master.CmtPubKey.Bytes(), key.Public().(ed25519.PublicKey), power})
	}
	ids, err := NewAppIdentities("svp-1", entries)
	if err != nil {
		t.Fatal(err)
	}
	committee := cmttypes.NewValidatorSet(validators)
	manager := NewQCVoteManager(committee)
	manager.appIdentities = ids
	var id types.Bytes32
	binary.BigEndian.PutUint32(id[:4], 2)
	var info *v2.ExtendedCommitInfo
	for i, v := range committee.Validators {
		var key ed25519.PrivateKey
		var master *types.BlsMaster
		for j, m := range masters {
			if bytes.Equal(v.PubKey.Bytes(), m.CmtPubKey.Bytes()) {
				key = keys[j]
				master = m
				break
			}
		}
		if master == nil {
			t.Fatal("missing master")
		}
		sig, err := bls.SignatureFromBytes(master.SignMessage(id[:]).Marshal())
		if err != nil {
			t.Fatal(err)
		}
		ext := []byte{byte(i)}
		appSig := ed25519.Sign(key, canonicalExtensionSignBytes("svp-1", 2, 3, ext))
		qc, got := manager.AddVerifiedVote(uint32(i), v, 1, 3, id, sig, ext, nil, appSig, nil, nil)
		if qc != nil {
			info = got
		}
	}
	if info == nil || len(info.Votes) != 3 {
		t.Fatal("expected QC and extended commit")
	}
	for k := 1; k < len(info.Votes); k++ {
		if info.Votes[k-1].Validator.Power < info.Votes[k].Validator.Power {
			t.Fatal("app votes not sorted by power")
		}
		if info.Votes[k-1].Validator.Power == info.Votes[k].Validator.Power && bytes.Compare(info.Votes[k-1].Validator.Address, info.Votes[k].Validator.Address) > 0 {
			t.Fatal("app votes not sorted by address")
		}
	}
}
