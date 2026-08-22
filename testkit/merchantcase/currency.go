package merchantcase

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type currencyEvent struct {
	EventID  string `json:"event_id"`
	Status   string `json:"status"`
	Currency string `json:"currency"`
}

type VulnerableCurrency struct {
	seen    map[string]bool
	state   string
	pending bool
	effect  bool
}

func NewVulnerableCurrency() *VulnerableCurrency {
	return &VulnerableCurrency{seen: make(map[string]bool)}
}

func (m *VulnerableCurrency) Deliver(body []byte) (bool, error) {
	event, err := decodeCurrencyEvent(body)
	if err != nil {
		return false, err
	}
	if m.seen[event.EventID] {
		return true, nil
	}
	m.seen[event.EventID] = true
	if m.state != "captured" {
		m.state = event.Status
	}
	if m.state == "captured" {
		m.pending = true
	}
	return true, nil
}

func (m *VulnerableCurrency) Fulfill() error {
	if !m.pending {
		return fmt.Errorf("payment is not ready for fulfillment")
	}
	m.effect = true
	m.pending = false
	return nil
}

func (m *VulnerableCurrency) Snapshot() Snapshot {
	effects := 0
	if m.effect {
		effects = 1
	}
	return Snapshot{State: m.state, CapturedOnce: m.state == "captured", Pending: m.pending, EffectCount: effects}
}

type CorrectCurrency struct {
	seen         map[string]bool
	expected     string
	state        string
	capturedOnce bool
	pending      bool
	effect       bool
}

func NewCorrectCurrency(expected string) *CorrectCurrency {
	return &CorrectCurrency{seen: make(map[string]bool), expected: expected}
}

func (m *CorrectCurrency) Deliver(body []byte) (bool, error) {
	event, err := decodeCurrencyEvent(body)
	if err != nil {
		return false, err
	}
	if event.Currency != m.expected {
		return false, nil
	}
	if m.seen[event.EventID] {
		return true, nil
	}
	m.seen[event.EventID] = true
	if m.state != "captured" {
		m.state = event.Status
	}
	if m.state == "captured" {
		m.capturedOnce = true
		m.pending = true
	}
	return true, nil
}

func (m *CorrectCurrency) Fulfill() error {
	if !m.pending {
		return fmt.Errorf("payment is not ready for fulfillment")
	}
	m.effect = true
	m.pending = false
	return nil
}

func (m *CorrectCurrency) Snapshot() Snapshot {
	effects := 0
	if m.effect {
		effects = 1
	}
	return Snapshot{State: m.state, CapturedOnce: m.capturedOnce, Pending: m.pending, EffectCount: effects}
}

func decodeCurrencyEvent(body []byte) (currencyEvent, error) {
	var event currencyEvent
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return currencyEvent{}, fmt.Errorf("decode payment event: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return currencyEvent{}, fmt.Errorf("payment event has extra JSON data")
	}
	if event.EventID == "" || event.Currency == "" || (event.Status != "captured" && event.Status != "failed") {
		return currencyEvent{}, fmt.Errorf("payment event is invalid")
	}
	return event, nil
}
