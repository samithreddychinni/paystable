package merchantcase

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type Snapshot struct {
	State        string
	CapturedOnce bool
	Pending      bool
	EffectCount  int
}

type amountEvent struct {
	EventID string `json:"event_id"`
	Status  string `json:"status"`
	Amount  int64  `json:"amount"`
}

type VulnerableAmount struct {
	seen    map[string]bool
	state   string
	pending bool
	effect  bool
}

func NewVulnerableAmount() *VulnerableAmount {
	return &VulnerableAmount{seen: make(map[string]bool)}
}

func (m *VulnerableAmount) Deliver(body []byte) (bool, error) {
	event, err := decodeAmountEvent(body)
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

func (m *VulnerableAmount) Fulfill() error {
	if !m.pending {
		return fmt.Errorf("payment is not ready for fulfillment")
	}
	if !m.effect {
		m.effect = true
	}
	m.pending = false
	return nil
}

func (m *VulnerableAmount) Snapshot() Snapshot {
	effects := 0
	if m.effect {
		effects = 1
	}
	return Snapshot{State: m.state, CapturedOnce: m.state == "captured", Pending: m.pending, EffectCount: effects}
}

type CorrectAmount struct {
	seen         map[string]bool
	expected     int64
	state        string
	capturedOnce bool
	pending      bool
	effect       bool
}

func NewCorrectAmount(expected int64) *CorrectAmount {
	return &CorrectAmount{seen: make(map[string]bool), expected: expected}
}

func (m *CorrectAmount) Deliver(body []byte) (bool, error) {
	event, err := decodeAmountEvent(body)
	if err != nil {
		return false, err
	}
	if event.Amount != m.expected {
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

func (m *CorrectAmount) Fulfill() error {
	if !m.pending {
		return fmt.Errorf("payment is not ready for fulfillment")
	}
	if !m.effect {
		m.effect = true
	}
	m.pending = false
	return nil
}

func (m *CorrectAmount) Snapshot() Snapshot {
	effects := 0
	if m.effect {
		effects = 1
	}
	return Snapshot{State: m.state, CapturedOnce: m.capturedOnce, Pending: m.pending, EffectCount: effects}
}

func decodeAmountEvent(body []byte) (amountEvent, error) {
	var event amountEvent
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return amountEvent{}, fmt.Errorf("decode payment event: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return amountEvent{}, fmt.Errorf("payment event has extra JSON data")
	}
	if event.EventID == "" || (event.Status != "captured" && event.Status != "failed") || event.Amount <= 0 {
		return amountEvent{}, fmt.Errorf("payment event is invalid")
	}
	return event, nil
}
