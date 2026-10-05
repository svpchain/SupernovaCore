package consensus

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/cometbft/cometbft/v2/crypto/bls12381"
	cmttypes "github.com/cometbft/cometbft/v2/types"
	"google.golang.org/protobuf/encoding/protowire"
)

// AppIdentity binds a HotStuff BLS validator to the Ed25519 consensus identity
// already known to SVP staking. The operator/account key is a separate key.
type AppIdentity struct {
	BLSPubKey   []byte
	AppPubKey   ed25519.PublicKey
	VotingPower int64
}

type AppIdentities struct {
	chainID string
	byBLS   map[string]AppIdentity
}

func NewAppIdentities(chainID string, entries []AppIdentity) (*AppIdentities, error) {
	if chainID == "" || len(entries) == 0 {
		return nil, errors.New("chain ID and app identities are required")
	}
	ids := &AppIdentities{chainID: chainID, byBLS: make(map[string]AppIdentity, len(entries))}
	addresses := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if len(entry.AppPubKey) != ed25519.PublicKeySize || entry.VotingPower <= 0 {
			return nil, errors.New("invalid BLS or Ed25519 public key")
		}
		if _, err := bls12381.NewPublicKeyFromBytes(entry.BLSPubKey); err != nil {
			return nil, fmt.Errorf("invalid BLS public key: %w", err)
		}
		addr := appAddress(entry.AppPubKey)
		if _, ok := ids.byBLS[string(entry.BLSPubKey)]; ok || addresses[string(addr)] {
			return nil, errors.New("duplicate BLS or app identity")
		}
		addresses[string(addr)] = true
		ids.byBLS[string(entry.BLSPubKey)] = AppIdentity{BLSPubKey: bytes.Clone(entry.BLSPubKey), AppPubKey: bytes.Clone(entry.AppPubKey), VotingPower: entry.VotingPower}
	}
	return ids, nil
}

func appAddress(pub ed25519.PublicKey) []byte {
	sum := sha256.Sum256(pub) // CometBFT Ed25519 address is SHA256(pubkey)[:20].
	return bytes.Clone(sum[:20])
}

func (ids *AppIdentities) forValidator(v *cmttypes.Validator) (AppIdentity, error) {
	if ids == nil || v == nil || v.PubKey == nil {
		return AppIdentity{}, errors.New("missing validator identity")
	}
	identity, ok := ids.byBLS[string(v.PubKey.Bytes())]
	if !ok {
		return AppIdentity{}, fmt.Errorf("missing app identity for BLS validator %X", v.Address)
	}
	if v.VotingPower != identity.VotingPower {
		return AppIdentity{}, fmt.Errorf("BLS and SVP application voting power differ for %X", v.Address)
	}
	return identity, nil
}

func (ids *AppIdentities) ValidateCommittee(vset *cmttypes.ValidatorSet) error {
	if ids == nil || vset == nil {
		return errors.New("missing identity mapping or committee")
	}
	for _, v := range vset.Validators {
		if _, err := ids.forValidator(v); err != nil {
			return err
		}
	}
	return nil
}

// canonicalExtensionSignBytes matches CometBFT v0.38 VoteExtensionSignBytes:
// delimited protobuf CanonicalVoteExtension{extension, fixed64 height, fixed64 round, chain_id}.
// In particular the app signature does NOT sign the HotStuff BlockID.
func canonicalExtensionSignBytes(chainID string, height int64, round int32, extension []byte) []byte {
	var msg []byte
	if len(extension) != 0 {
		msg = protowire.AppendTag(msg, 1, protowire.BytesType)
		msg = protowire.AppendBytes(msg, extension)
	}
	if height != 0 {
		msg = protowire.AppendTag(msg, 2, protowire.Fixed64Type)
		msg = protowire.AppendFixed64(msg, uint64(height))
	}
	if round != 0 {
		msg = protowire.AppendTag(msg, 3, protowire.Fixed64Type)
		msg = protowire.AppendFixed64(msg, uint64(int64(round)))
	}
	if chainID != "" {
		msg = protowire.AppendTag(msg, 4, protowire.BytesType)
		msg = protowire.AppendString(msg, chainID)
	}
	return protowire.AppendBytes(nil, msg)
}

func (ids *AppIdentities) verifyExtension(v *cmttypes.Validator, height int64, round int32, extension, sig []byte) bool {
	identity, err := ids.forValidator(v)
	return err == nil && len(sig) == ed25519.SignatureSize && ed25519.Verify(identity.AppPubKey, canonicalExtensionSignBytes(ids.chainID, height, round, extension), sig)
}

// ConfigureSVPIdentity opts this pacemaker into strict, dual-key SVP mode.
// Call before Start. The signer is the validator's existing Ed25519 consensus
// key, not the staking operator key. Application connection is wired separately.
func (p *Pacemaker) ConfigureSVPIdentity(ids *AppIdentities, privateKey ed25519.PrivateKey) error {
	if ids == nil {
		return errors.New("missing SVP identities")
	}
	if len(privateKey) != 0 && len(privateKey) != ed25519.PrivateKeySize {
		return errors.New("invalid Ed25519 private key")
	}
	if len(privateKey) != 0 && p.blsMaster != nil {
		identity, ok := ids.byBLS[string(p.blsMaster.CmtPubKey.Bytes())]
		if !ok || !bytes.Equal(privateKey[32:], identity.AppPubKey) {
			return errors.New("Ed25519 signer does not match local BLS identity")
		}
	}
	p.appIdentities = ids
	p.appPrivateKey = bytes.Clone(privateKey)
	if p.executor != nil {
		p.executor.appIdentities = ids
	}
	return nil
}

func (ids *AppIdentities) appAddressFor(validator *cmttypes.Validator) ([]byte, error) {
	identity, err := ids.forValidator(validator)
	if err != nil {
		return nil, err
	}
	return appAddress(identity.AppPubKey), nil
}
