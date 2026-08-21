# Verification artifacts

The Razorpay test flow writes its signed webhook fixture to this directory.

Use only dummy customer data during the test payment.
Review every fixture before you commit it.

Container replay results are in the `lab` directory.
Each result contains its schedule, trace, invariant result, and replay status.
