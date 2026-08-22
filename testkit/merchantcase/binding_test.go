package merchantcase

import "testing"

func TestBindingMerchantsDisagreeOnTheWrongOrder(t *testing.T) {
	body := []byte(`{"event_id":"event-1","status":"captured","order_id":"order-other"}`)
	vulnerable := NewVulnerableBinding()
	accepted, err := vulnerable.Deliver(body)
	if err != nil || !accepted || vulnerable.Snapshot().State != "captured" {
		t.Fatal("vulnerable merchant did not accept the order mismatch")
	}
	correct := NewCorrectBinding("order-expected")
	accepted, err = correct.Deliver(body)
	if err != nil || accepted || correct.Snapshot().State != "" {
		t.Fatal("correct merchant did not reject the order mismatch")
	}
}
