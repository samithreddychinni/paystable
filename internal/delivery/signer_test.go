package delivery

import (
	"strconv"
	"testing"
	"time"
)

func TestSign_V2Vector(t *testing.T) {
	body := []byte(`{"txn_id":"t1","status":"CONFIRMED"}`)
	const expected = "v2=cdacb59957e01e80f07059b698d2682948dea8e10ba77352fbd08d92822d59a7"
	if got := Sign(body, "evt_t1", "1700000000", "mysecret"); got != expected {
		t.Fatalf("signature = %q, want %q", got, expected)
	}
}

func TestVerify_V2(t *testing.T) {
	body := []byte(`{"txn_id":"t1","status":"CONFIRMED"}`)
	now := time.Now().Unix()
	timestamp := strconv.FormatInt(now, 10)
	signature := Sign(body, "evt_t1", timestamp, "mysecret")
	for _, tc := range []struct {
		name, body, key, timestamp, secret, signature string
		want                                          bool
	}{
		{"valid", string(body), "evt_t1", timestamp, "mysecret", signature, true},
		{"changed body", `{"txn_id":"t1","status":"FAILED"}`, "evt_t1", timestamp, "mysecret", signature, false},
		{"changed event key", string(body), "evt_other", timestamp, "mysecret", signature, false},
		{"changed timestamp", string(body), "evt_t1", strconv.FormatInt(now+1, 10), "mysecret", signature, false},
		{"wrong secret", string(body), "evt_t1", timestamp, "other", signature, false},
		{"empty secret", string(body), "evt_t1", timestamp, "", Sign(body, "evt_t1", timestamp, ""), false},
		{"empty key", string(body), "", timestamp, "mysecret", Sign(body, "", timestamp, "mysecret"), false},
		{"newline key", string(body), "evt\nother", timestamp, "mysecret", Sign(body, "evt\nother", timestamp, "mysecret"), false},
		{"carriage return key", string(body), "evt\rother", timestamp, "mysecret", Sign(body, "evt\rother", timestamp, "mysecret"), false},
		{"empty timestamp", string(body), "evt_t1", "", "mysecret", Sign(body, "evt_t1", "", "mysecret"), false},
		{"invalid timestamp", string(body), "evt_t1", "NaN", "mysecret", Sign(body, "evt_t1", "NaN", "mysecret"), false},
		{"noncanonical timestamp", string(body), "evt_t1", "0" + timestamp, "mysecret", Sign(body, "evt_t1", "0"+timestamp, "mysecret"), false},
		{"missing signature", string(body), "evt_t1", timestamp, "mysecret", "", false},
		{"invalid signature", string(body), "evt_t1", timestamp, "mysecret", "v2=zzz", false},
		{"legacy signature", string(body), "evt_t1", timestamp, "mysecret", "sha256=149d12490a60e0859d092babacc64edcf791b8484268a8aa3ea599f7f9db84f1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Verify([]byte(tc.body), tc.signature, tc.key, tc.timestamp, tc.secret); got != tc.want {
				t.Fatalf("Verify = %v, want %v", got, tc.want)
			}
		})
	}
	for _, offset := range []int64{-600, -240, 240, 600} {
		timestamp := strconv.FormatInt(now+offset, 10)
		signature := Sign(body, "evt_t1", timestamp, "mysecret")
		want := offset >= -300 && offset <= 300
		if got := Verify(body, signature, "evt_t1", timestamp, "mysecret"); got != want {
			t.Fatalf("offset %d: Verify = %v, want %v", offset, got, want)
		}
	}
}

func TestVerify_ReplayCannotChangeEventIdentity(t *testing.T) {
	body := []byte(`{"txn_id":"t1","status":"CONFIRMED"}`)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := Sign(body, "evt_t1", timestamp, "mysecret")
	seen := map[string]bool{}
	fulfilled := 0
	for _, key := range []string{"evt_t1", "evt_t1", "attacker_key"} {
		if !Verify(body, signature, key, timestamp, "mysecret") || seen[key] {
			continue
		}
		seen[key] = true
		fulfilled++
	}
	// A genuine retry is re-signed with a fresh timestamp and the same key.
	retryTimestamp := strconv.FormatInt(time.Now().Unix()+1, 10)
	retrySignature := Sign(body, "evt_t1", retryTimestamp, "mysecret")
	if !Verify(body, retrySignature, "evt_t1", retryTimestamp, "mysecret") {
		t.Fatal("genuine retry rejected")
	}
	if !seen["evt_t1"] {
		fulfilled++
	}
	if fulfilled != 1 {
		t.Fatalf("fulfilled %d times, want once", fulfilled)
	}
}
