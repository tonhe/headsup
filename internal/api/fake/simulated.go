package fake

// Simulated marks the in-memory backend, so features that would reach the
// network on the server's behalf (the upgrade check) stay offline in demos.
func (b *Backend) Simulated() bool { return true }
