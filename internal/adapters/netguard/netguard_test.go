package netguard

import (
	"errors"
	"testing"
)

func TestRejectPrivateAddressBlocksReservedRanges(t *testing.T) {
	addresses := []string{
		"100.64.0.1:80", "100.127.255.254:80", "0.1.2.3:80", "[64:ff9b::808:808]:80",
		"127.0.0.1:80", "[::1]:80", "10.0.0.5:6379", "172.16.0.1:80", "192.168.1.1:80", "169.254.169.254:80",
	}
	for _, address := range addresses {
		if err := RejectPrivateAddress("tcp", address, nil); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s must be blocked, got %v", address, err)
		}
	}
}

func TestRejectPrivateAddressAllowsPublicAddress(t *testing.T) {
	if err := RejectPrivateAddress("tcp", "8.8.8.8:80", nil); err != nil {
		t.Fatalf("public address must pass, got %v", err)
	}
}

func TestRejectPrivateAddressBlocksUnparseableAddress(t *testing.T) {
	if err := RejectPrivateAddress("tcp", "not-an-address", nil); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected blocked, got %v", err)
	}
}
