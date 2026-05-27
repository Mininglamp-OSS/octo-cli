package authstore

import "errors"

// errMachineIDUnsupported is returned by machineID on platforms that have no
// portable machine identifier. deriveKey treats it as a stable "salt-only key"
// signal — distinct from a transient read failure on a supported platform,
// which must surface rather than silently change the derived key.
var errMachineIDUnsupported = errors.New("machine id unsupported on this platform")
