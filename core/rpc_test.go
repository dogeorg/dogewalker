package core

import (
	"testing"
)

func TestNewCoreRPCClient(t *testing.T) {
	rpc := NewCoreRPCClient("http://127.0.0.1:22555", 0, "dogecoin", "dogecoin")
	if rpc == nil {
		t.Fatal("NewCoreRPCClient returned nil")
	}

	if rpc.(*CoreRPCClient).URL != "http://127.0.0.1:22555" {
		t.Fatal("GetURL returned wrong URL")
	}
}

func TestNewCoreRPCClientWithHostname(t *testing.T) {
	rpc := NewCoreRPCClient("hostname", 22555, "dogecoin", "dogecoin")
	if rpc == nil {
		t.Fatal("NewCoreRPCClient returned nil")
	}

	if rpc.(*CoreRPCClient).URL != "http://hostname:22555" {
		t.Fatal("GetURL returned wrong URL")
	}
}
