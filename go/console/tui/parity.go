package tui

import "hop.top/kit/contracts/parity"

// parityValues is a package-level alias so tui internals can use parity.Values
// without qualifying the subpackage name everywhere.
var parityValues = &parity.Values
