package main

import (
	"testing"
)

func TestSubnetPrefix(t *testing.T) {
	tests := []struct {
		cidr string
		want string
	}{
		{"172.32.0.0/24", "172.32.0"},
		{"10.200.0.0/24", "10.200.0"},
		{"192.168.1.0/24", "192.168.1"},
		{"invalid", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := subnetPrefix(tt.cidr)
		if got != tt.want {
			t.Errorf("subnetPrefix(%q) = %q, want %q", tt.cidr, got, tt.want)
		}
	}
}
