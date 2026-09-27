package node

import (
	"bytes"
	"testing"

	cmttypes "github.com/cometbft/cometbft/v2/types"
)

func TestTxBatchRoundTrip(t *testing.T) {
	in := []cmttypes.Tx{[]byte("a"), {}, bytes.Repeat([]byte{7}, 1000)}
	out, err := decodeTxBatch(encodeTxBatch(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("got %d txs, want %d", len(out), len(in))
	}
	for i := range in {
		if !bytes.Equal(in[i], out[i]) {
			t.Fatalf("tx %d differs", i)
		}
	}
}

func TestTxBatchRejectsMalformed(t *testing.T) {
	good := encodeTxBatch([]cmttypes.Tx{[]byte("abc"), []byte("de")})
	for name, data := range map[string][]byte{
		"empty":     {},
		"truncated": good[:len(good)-1],
		"trailing":  append(append([]byte{}, good...), 0),
		"huge len":  {1, 0xff, 0xff, 0xff, 0xff, 0x0f},
	} {
		if _, err := decodeTxBatch(data); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
