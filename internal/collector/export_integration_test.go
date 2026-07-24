//go:build integration

package collector

// PersistResilientForTest exposes persistResilient to the external
// collector_test package for the transient-vs-poison boundary tests.
var PersistResilientForTest = persistResilient
