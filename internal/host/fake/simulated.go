package fake

// Simulated marks the in-memory host, so maintenance features (the doctor's
// probes, backup bundles, the upgrade check) never touch the real machine
// or the network on its behalf.
func (h *Host) Simulated() bool { return true }
