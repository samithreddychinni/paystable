package merchantcase

import "testing"

func TestAmountMerchantsDisagreeOnlyOnTheMismatch(t *testing.T) {
	body := []byte(`{"event_id":"event-1","status":"captured","amount":1}`)
	vulnerable := NewVulnerableAmount()
	accepted, err := vulnerable.Deliver(body)
	if err != nil || !accepted || vulnerable.Snapshot().State != "captured" {
		t.Fatal("vulnerable merchant did not accept the amount mismatch")
	}
	correct := NewCorrectAmount(49900)
	accepted, err = correct.Deliver(body)
	if err != nil || accepted || correct.Snapshot().State != "" {
		t.Fatal("correct merchant did not reject the amount mismatch")
	}
}
