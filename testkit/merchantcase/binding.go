package merchantcase

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type bindingEvent struct {
	EventID string `json:"event_id"`
	Status  string `json:"status"`
	OrderID string `json:"order_id"`
}

type VulnerableBinding struct {
	seen    map[string]bool
	state   string
	pending bool
	effect  bool
}

func NewVulnerableBinding() *VulnerableBinding {
	return &VulnerableBinding{seen: make(map[string]bool)}
}

func (m *VulnerableBinding) Deliver(body []byte) (bool, error) {
	event, err := decodeBindingEvent(body)
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

func (m *VulnerableBinding) Fulfill() error {
	if !m.pending {
		return fmt.Errorf("payment is not ready for fulfillment")
	}
	m.effect = true
	m.pending = false
	return nil
}

func (m *VulnerableBinding) Snapshot() Snapshot {
	effects := 0
	if m.effect {
		effects = 1
	}
	return Snapshot{State: m.state, CapturedOnce: m.state == "captured", Pending: m.pending, EffectCount: effects}
}

type CorrectBinding struct {
	seen         map[string]bool
	expected     string
	state        string
	capturedOnce bool
	pending      bool
	effect       bool
}

func NewCorrectBinding(expected string) *CorrectBinding {
	return &CorrectBinding{seen: make(map[string]bool), expected: expected}
}

func (m *CorrectBinding) Deliver(body []byte) (bool, error) {
	event, err := decodeBindingEvent(body)
	if err != nil {
		return false, err
	}
	if event.OrderID != m.expected {
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

func (m *CorrectBinding) Fulfill() error {
	if !m.pending {
		return fmt.Errorf("payment is not ready for fulfillment")
	}
	m.effect = true
	m.pending = false
	return nil
}

func (m *CorrectBinding) Snapshot() Snapshot {
	effects := 0
	if m.effect {
		effects = 1
	}
	return Snapshot{State: m.state, CapturedOnce: m.capturedOnce, Pending: m.pending, EffectCount: effects}
}

func decodeBindingEvent(body []byte) (bindingEvent, error) {
	var event bindingEvent
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return bindingEvent{}, fmt.Errorf("decode payment event: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return bindingEvent{}, fmt.Errorf("payment event has extra JSON data")
	}
	if event.EventID == "" || event.OrderID == "" || (event.Status != "captured" && event.Status != "failed") {
		return bindingEvent{}, fmt.Errorf("payment event is invalid")
	}
	return event, nil
}
