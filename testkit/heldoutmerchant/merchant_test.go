package heldoutmerchant

import "testing"

func TestHeldOutMerchantPairsDisagreeOnTheirTargetFailure(t *testing.T) {
	captured := Event{ID: "captured", Status: "captured", SignatureValid: true}
	failed := Event{ID: "failed", Status: "failed", SignatureValid: true}
	invalid := Event{ID: "invalid", Status: "captured"}

	unsafeDedup, safeDedup := NewUnsafeDedup(), NewSafeDedup()
	unsafeDedup.Deliver(captured, true)
	safeDedup.Deliver(captured, true)
	unsafeDedup.Restart()
	safeDedup.Restart()
	unsafeDedup.Deliver(captured, false)
	safeDedup.Deliver(captured, false)

	unsafeState, safeState := NewUnsafeState(), NewSafeState()
	unsafeState.Deliver(captured, false)
	safeState.Deliver(captured, false)
	unsafeState.Deliver(failed, false)
	safeState.Deliver(failed, false)

	unsafeTrust, safeTrust := NewUnsafeTrust(), NewSafeTrust()
	unsafeTrust.Deliver(invalid, false)
	safeTrust.Deliver(invalid, false)

	unsafeRetry, safeRetry := NewUnsafeRetry(), NewSafeRetry()
	unsafeRetry.Deliver(captured, false)
	safeRetry.Deliver(captured, false)
	unsafeRetry.Fulfill("timeout")
	safeRetry.Fulfill("timeout")
	unsafeRetry.Fulfill("ok")
	safeRetry.Fulfill("ok")

	if unsafeDedup.Snapshot().EffectCount != 2 || safeDedup.Snapshot().EffectCount != 1 ||
		unsafeState.Snapshot().State != "failed" || safeState.Snapshot().State != "captured" ||
		!unsafeTrust.Snapshot().AcceptedUntrusted || safeTrust.Snapshot().AcceptedUntrusted ||
		unsafeRetry.Snapshot().EffectCount != 2 || safeRetry.Snapshot().EffectCount != 1 {
		t.Fatal("a held-out merchant pair does not isolate its target failure")
	}
}
