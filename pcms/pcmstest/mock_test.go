package pcmstest

import (
	"context"
	"errors"
	"testing"
)

func TestMockClient(t *testing.T) {
	var zero MockClient
	if err := zero.Ping(context.Background()); err != nil || zero.Raw() != nil {
		t.Fatal("expected the zero value to ping successfully with no raw client")
	}

	down := errors.New("down")
	if err := (&MockClient{PingErr: down}).Ping(context.Background()); !errors.Is(err, down) {
		t.Fatalf("expected PingErr, got %v", err)
	}
}
