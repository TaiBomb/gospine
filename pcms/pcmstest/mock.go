// Package pcmstest provides test doubles for package pcms.
package pcmstest

import (
	"context"

	"github.com/TaiBomb/gopcms"
	"github.com/TaiBomb/gospine/pcms"
)

// MockClient is a pcms.Client whose Ping returns PingErr and Raw returns RawClient.
type MockClient struct {
	PingErr   error
	RawClient *gopcms.Client
}

var _ pcms.Client = (*MockClient)(nil)

// Ping returns m.PingErr.
func (m *MockClient) Ping(context.Context) error {
	return m.PingErr
}

// Raw returns m.RawClient.
func (m *MockClient) Raw() *gopcms.Client {
	return m.RawClient
}
