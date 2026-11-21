package webchat

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
)

func TestHostReplySourceBinding(t *testing.T) {
	f := newFixture(t, Config{}, nil)
	s := mint(t, f, 0, "customer-one")
	other := mint(t, f, 0, "customer-two")
	foreign := mint(t, f, 1, "customer-one")
	msg := map[string]string{"event_id": "read-1", "content": "current status?", "host_reply": "confirmed"}
	key := sha256.Sum256([]byte(testHosts[0].Secret))
	mac := hmac.New(sha256.New, key[:])
	mac.Write([]byte("omni-host-reply-v1"))
	for _, p := range []string{s.AccessToken, msg["event_id"], msg["content"], msg["host_reply"]} {
		sum := sha256.Sum256([]byte(p))
		mac.Write(sum[:])
	}
	proof := hex.EncodeToString(mac.Sum(nil))
	run := func(token, signature string, status int) {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if signature != "" {
				r.Header.Set("X-Omni-Reply-Proof", signature)
			}
			f.g.ServeHTTP(w, r)
		})
		expect(t, call(h, "POST", "/chat/v1/messages", token, msg), status)
	}
	run(s.AccessToken, "", 400)
	run(s.AccessToken, "invalid", 403)
	run(other.AccessToken, proof, 403)
	run(foreign.AccessToken, proof, 403)
	msg["host_reply"] = "changed"
	run(s.AccessToken, proof, 403)
	msg["host_reply"] = "confirmed"
	run(s.AccessToken, proof, 200)
	run(s.AccessToken, proof, 200)
	if f.models.Load() != 0 {
		t.Fatal("trusted read invoked model")
	}
}
