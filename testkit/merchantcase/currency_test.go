package merchantcase

import "testing"

func TestCurrencyMerchantsDisagreeOnTheWrongCurrency(t *testing.T) {
	body := []byte(`{"event_id":"event-1","status":"captured","currency":"USD"}`)
	vulnerable := NewVulnerableCurrency()
	accepted, err := vulnerable.Deliver(body)
	if err != nil || !accepted || vulnerable.Snapshot().State != "captured" {
		t.Fatal("vulnerable merchant did not accept the currency mismatch")
	}
	correct := NewCorrectCurrency("INR")
	accepted, err = correct.Deliver(body)
	if err != nil || accepted || correct.Snapshot().State != "" {
		t.Fatal("correct merchant did not reject the currency mismatch")
	}
}
