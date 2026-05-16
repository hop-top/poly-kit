// Package mock implements a unit-test mock driver for the Gitea
// repohost surface. It registers itself under provider name
// "gitea-mock" so adopter tests can blank-import this package and
// switch by setting Config.Provider = "gitea-mock".
//
// Default behavior returns [repohost.Baseline] values for every
// method. Override defaults via the Set*** knobs and call [Reset]
// between tests to clear state. The mock layer is module-scoped and
// guarded by a sync.Mutex so parallel-running test goroutines do
// not corrupt each other's knob state.
package mock
