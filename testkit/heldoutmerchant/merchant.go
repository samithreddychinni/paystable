package heldoutmerchant

type Event struct {
	ID             string
	Status         string
	SignatureValid bool
}

type Snapshot struct {
	Running           bool
	State             string
	CapturedOnce      bool
	AcceptedUntrusted bool
	EffectCount       int
}

type Merchant interface {
	Deliver(Event, bool) bool
	Fulfill(string)
	Restart()
	Snapshot() Snapshot
}

type UnsafeDedup struct {
	running      bool
	seen         map[string]bool
	state        string
	capturedOnce bool
	effects      int
}

func NewUnsafeDedup() Merchant {
	return &UnsafeDedup{running: true, seen: make(map[string]bool)}
}

func (m *UnsafeDedup) Deliver(event Event, crashAfterEffect bool) bool {
	if !m.running || m.seen[event.ID] {
		return m.running
	}
	if m.state != "captured" {
		m.state = event.Status
	}
	if event.Status != "captured" {
		m.seen[event.ID] = true
		return true
	}
	m.capturedOnce = true
	m.effects++
	if crashAfterEffect {
		m.running = false
		return true
	}
	m.seen[event.ID] = true
	return true
}

func (m *UnsafeDedup) Fulfill(string) {}
func (m *UnsafeDedup) Restart()       { m.running = true }
func (m *UnsafeDedup) Snapshot() Snapshot {
	return Snapshot{Running: m.running, State: m.state, CapturedOnce: m.capturedOnce, EffectCount: m.effects}
}

type SafeDedup struct {
	running      bool
	seen         map[string]bool
	state        string
	capturedOnce bool
	effects      int
}

func NewSafeDedup() Merchant {
	return &SafeDedup{running: true, seen: make(map[string]bool)}
}

func (m *SafeDedup) Deliver(event Event, crashAfterEffect bool) bool {
	if !m.running || m.seen[event.ID] {
		return m.running
	}
	m.seen[event.ID] = true
	if m.state != "captured" {
		m.state = event.Status
	}
	if event.Status == "captured" {
		m.capturedOnce = true
		m.effects++
	}
	if crashAfterEffect {
		m.running = false
	}
	return true
}

func (m *SafeDedup) Fulfill(string) {}
func (m *SafeDedup) Restart()       { m.running = true }
func (m *SafeDedup) Snapshot() Snapshot {
	return Snapshot{Running: m.running, State: m.state, CapturedOnce: m.capturedOnce, EffectCount: m.effects}
}

type UnsafeState struct {
	state        string
	capturedOnce bool
}

func NewUnsafeState() Merchant { return &UnsafeState{} }
func (m *UnsafeState) Deliver(event Event, _ bool) bool {
	m.state = event.Status
	m.capturedOnce = m.capturedOnce || event.Status == "captured"
	return true
}
func (m *UnsafeState) Fulfill(string) {}
func (m *UnsafeState) Restart()       {}
func (m *UnsafeState) Snapshot() Snapshot {
	return Snapshot{Running: true, State: m.state, CapturedOnce: m.capturedOnce}
}

type SafeState struct {
	state        string
	capturedOnce bool
}

func NewSafeState() Merchant { return &SafeState{} }
func (m *SafeState) Deliver(event Event, _ bool) bool {
	if m.state != "captured" {
		m.state = event.Status
	}
	m.capturedOnce = m.capturedOnce || event.Status == "captured"
	return true
}
func (m *SafeState) Fulfill(string) {}
func (m *SafeState) Restart()       {}
func (m *SafeState) Snapshot() Snapshot {
	return Snapshot{Running: true, State: m.state, CapturedOnce: m.capturedOnce}
}

type UnsafeTrust struct {
	state             string
	capturedOnce      bool
	acceptedUntrusted bool
}

func NewUnsafeTrust() Merchant { return &UnsafeTrust{} }
func (m *UnsafeTrust) Deliver(event Event, _ bool) bool {
	if !event.SignatureValid {
		m.acceptedUntrusted = true
	}
	if m.state != "captured" {
		m.state = event.Status
	}
	m.capturedOnce = m.capturedOnce || event.Status == "captured"
	return true
}
func (m *UnsafeTrust) Fulfill(string) {}
func (m *UnsafeTrust) Restart()       {}
func (m *UnsafeTrust) Snapshot() Snapshot {
	return Snapshot{Running: true, State: m.state, CapturedOnce: m.capturedOnce, AcceptedUntrusted: m.acceptedUntrusted}
}

type SafeTrust struct {
	state        string
	capturedOnce bool
}

func NewSafeTrust() Merchant { return &SafeTrust{} }
func (m *SafeTrust) Deliver(event Event, _ bool) bool {
	if !event.SignatureValid {
		return false
	}
	if m.state != "captured" {
		m.state = event.Status
	}
	m.capturedOnce = m.capturedOnce || event.Status == "captured"
	return true
}
func (m *SafeTrust) Fulfill(string) {}
func (m *SafeTrust) Restart()       {}
func (m *SafeTrust) Snapshot() Snapshot {
	return Snapshot{Running: true, State: m.state, CapturedOnce: m.capturedOnce}
}

type UnsafeRetry struct {
	state        string
	capturedOnce bool
	pending      bool
	effects      int
}

func NewUnsafeRetry() Merchant { return &UnsafeRetry{} }
func (m *UnsafeRetry) Deliver(event Event, _ bool) bool {
	if m.state != "captured" {
		m.state = event.Status
	}
	if event.Status == "captured" {
		m.capturedOnce = true
		m.pending = true
	}
	return true
}
func (m *UnsafeRetry) Fulfill(response string) {
	if !m.pending {
		return
	}
	m.effects++
	if response == "ok" {
		m.pending = false
	}
}
func (m *UnsafeRetry) Restart() {}
func (m *UnsafeRetry) Snapshot() Snapshot {
	return Snapshot{Running: true, State: m.state, CapturedOnce: m.capturedOnce, EffectCount: m.effects}
}

type SafeRetry struct {
	state        string
	capturedOnce bool
	pending      bool
	effect       bool
}

func NewSafeRetry() Merchant { return &SafeRetry{} }
func (m *SafeRetry) Deliver(event Event, _ bool) bool {
	if m.state != "captured" {
		m.state = event.Status
	}
	if event.Status == "captured" {
		m.capturedOnce = true
		m.pending = true
	}
	return true
}
func (m *SafeRetry) Fulfill(response string) {
	if !m.pending {
		return
	}
	m.effect = true
	if response == "ok" {
		m.pending = false
	}
}
func (m *SafeRetry) Restart() {}
func (m *SafeRetry) Snapshot() Snapshot {
	effects := 0
	if m.effect {
		effects = 1
	}
	return Snapshot{Running: true, State: m.state, CapturedOnce: m.capturedOnce, EffectCount: effects}
}
