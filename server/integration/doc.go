// Package integration holds end-to-end tests that drive the real compiled
// goldencloud binary over HTTP.
//
// The tests are behind the "integration" build tag because they start
// processes, bind ports and move a gigabyte of data:
//
//	go test -tags=integration ./integration/...
//
// The binary under test comes from the GOLDENCLOUD_BINARY environment
// variable; if it is unset the suite builds one.
package integration
