package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0ceanslim/grain/client/core/tools"
	nostr "github.com/0ceanslim/grain/server/types"
	"github.com/0ceanslim/grain/server/validation"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// The 0x0 frontend signs anonymous uploads with its own BIP-340 implementation
// (frontends/0x0/scripts/core/throwaway-key.js). This test runs that file
// under node against the first BIP-340 test vectors and then checks a signed
// kind-24242 event with the very code the server uses to verify uploads. It
// skips when node is not installed.

var bip340Vectors = []struct{ sk, aux, msg, pub, sig string }{
	{
		"0000000000000000000000000000000000000000000000000000000000000003",
		"0000000000000000000000000000000000000000000000000000000000000000",
		"0000000000000000000000000000000000000000000000000000000000000000",
		"f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9",
		"e907831f80848d1069a5371b402410364bdf1c5f8307b0084c55f1ce2dca821525f66a4a85ea8b71e482a74f382d2ce5ebeee8fdb2172f477df4900d310536c0",
	},
	{
		"b7e151628aed2a6abf7158809cf4f3c762e7160f38b4da56a784d9045190cfef",
		"0000000000000000000000000000000000000000000000000000000000000001",
		"243f6a8885a308d313198a2e03707344a4093822299f31d0082efa98ec4e6c89",
		"dff1d77f2a671c5f36183726db2341be58feae1da2deced843240f7b502ba659",
		"6896bd60eeae296db48a229ff71dfe071bde413e6d43f917dc8dcf8c78de33418906d11ac976abccb20b091292bff4ea897efcb639ea871cfa95f6de339e4b0a",
	},
	{
		"c90fdaa22168c234c4c6628b80dc1cd129024e088a67cc74020bbea63b14e5c9",
		"c87aa53824b4d7ae2eb035a2b5bbbccc080e76cdc6d1692c4b0b62d798e6d906",
		"7e2d58d8b3bcdf1abadec7829054f90dda9805aab56c77333024b9d0a508b75c",
		"dd308afec5777e13121fa72b9cc1b7cc0139715309b086c960e18fd969774eb8",
		"5831aaeed7b44bb74e5eab94ba9d4294c49bcf2a60728d8b4c200f50dd313c1bab745879a5ad954a72c45a91c3a51d3c7adea98d82f8481e0e1e03674a6f3fb7",
	},
}

const throwawayHarness = `
require(process.argv[2]);
const TK = globalThis.ThrowawayKey;
(async () => {
  const input = JSON.parse(process.argv[3]);
  const out = { vectors: [] };
  for (const v of input.vectors) {
    out.vectors.push({ pub: TK.getPublicKey(v.sk), sig: await TK.schnorrSign(v.msg, v.sk, v.aux) });
  }
  const key = await TK.generate();
  out.key = key;
  out.event = await TK.signEvent(input.template, key.privHex);
  console.log(JSON.stringify(out));
})().catch((e) => { console.error((e && e.stack) || e); process.exit(1); });
`

func TestThrowawayKeyJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; throwaway-key.js is checked where it is")
	}
	js, err := filepath.Abs(filepath.Join("..", "..", "frontends", "0x0", "scripts", "core", "throwaway-key.js"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(js); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(t.TempDir(), "harness.js")
	if err := os.WriteFile(harness, []byte(throwawayHarness), 0o644); err != nil {
		t.Fatal(err)
	}

	const hash = "6fe9e497859ad682ca2acdc93c891a8baa2fe10489d295497a5952b76242fbed"
	now := time.Now().Unix()
	type vec struct{ Sk, Aux, Msg string }
	vectors := make([]map[string]string, 0, len(bip340Vectors))
	for _, v := range bip340Vectors {
		vectors = append(vectors, map[string]string{"sk": v.sk, "aux": v.aux, "msg": v.msg})
	}
	input, _ := json.Marshal(map[string]any{
		"vectors": vectors,
		"template": map[string]any{
			"kind":       blossomAuthKind,
			"created_at": now,
			"tags":       [][]string{{"t", "upload"}, {"x", hash}, {"expiration", jsonInt(now + 300)}},
			"content":    "",
		},
	})

	cmd := exec.Command(node, harness, js, string(input))
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("node failed: %v\n%s", err, ee.Stderr)
		}
		t.Fatal(err)
	}

	var res struct {
		Vectors []struct{ Pub, Sig string } `json:"vectors"`
		Key     struct {
			PrivHex string `json:"privHex"`
			PubHex  string `json:"pubHex"`
			Nsec    string `json:"nsec"`
			Npub    string `json:"npub"`
		} `json:"key"`
		Event nostr.Event `json:"event"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("harness output: %v\n%s", err, out)
	}

	for i, v := range bip340Vectors {
		if i >= len(res.Vectors) {
			t.Fatalf("vector %d missing from output", i)
		}
		if got := strings.ToLower(res.Vectors[i].Pub); got != v.pub {
			t.Errorf("vector %d pubkey %s, want %s", i, got, v.pub)
		}
		if got := strings.ToLower(res.Vectors[i].Sig); got != v.sig {
			t.Errorf("vector %d signature %s, want %s", i, got, v.sig)
		}
	}

	// Key encoding round-trips through grain's decoders.
	if priv, err := tools.DecodeNsec(res.Key.Nsec); err != nil || priv != res.Key.PrivHex {
		t.Fatalf("nsec %q decodes to %q (%v), want %q", res.Key.Nsec, priv, err, res.Key.PrivHex)
	}
	if npub, err := tools.EncodePubkey(res.Key.PubHex); err != nil || npub != res.Key.Npub {
		t.Fatalf("npub %q, grain encodes %q (%v)", res.Key.Npub, npub, err)
	}

	// The signed event passes the server's own check and names the key.
	ev := res.Event
	if !validation.CheckSignature(ev) {
		t.Fatalf("server rejected the JS-signed event: %+v", ev)
	}
	if ev.PubKey != res.Key.PubHex || ev.Kind != blossomAuthKind {
		t.Fatalf("event pubkey/kind %s/%d, want %s/%d", ev.PubKey, ev.Kind, res.Key.PubHex, blossomAuthKind)
	}
	tampered := ev
	tampered.Content = "x"
	if validation.CheckSignature(tampered) {
		t.Fatal("a tampered event still verifies")
	}

	// And the whole Authorization header, base64url without padding as BUD-11
	// asks, gets through nostrAuthMiddleware with the right pubkey and hash.
	payload, _ := json.Marshal(ev)
	token := base64.RawURLEncoding.EncodeToString(payload)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var gotPk, gotX string
	r.PUT("/upload", nostrAuthMiddleware("upload", zap.NewNop()), func(c *gin.Context) {
		gotPk, gotX = c.GetString("pk"), c.GetString("x")
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPut, "/upload", nil)
	req.Header.Set("Authorization", "Nostr "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || gotPk != res.Key.PubHex || gotX != hash {
		t.Fatalf("middleware: %d pk=%s x=%s", w.Code, gotPk, gotX)
	}
}

func jsonInt(n int64) string { return strings.TrimSpace(string(mustJSON(n))) }

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
