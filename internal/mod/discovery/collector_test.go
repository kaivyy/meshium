package discovery

import (
	"strings"
	"testing"
)

// mockSSHClient implements the SSHExecuter interface for testing.
type mockSSHClient struct {
	responses map[string]string
}

func (m *mockSSHClient) Exec(cmd string) (string, string, int, error) {
	for pattern, response := range m.responses {
		if strings.Contains(cmd, pattern) {
			return response, "", 0, nil
		}
	}
	return "", "", 0, nil
}

func TestCollectHostname(t *testing.T) {
	client := &mockSSHClient{
		responses: map[string]string{
			"hostname": "web-01\n",
		},
	}

	collector := NewCollector(client)
	result := collector.CollectHostname()

	if result.Value != "web-01" {
		t.Errorf("expected 'web-01', got %q", result.Value)
	}
}

func TestCollectOS(t *testing.T) {
	client := &mockSSHClient{
		responses: map[string]string{
			"os-release": `PRETTY_NAME="Ubuntu 22.04 LTS"`,
		},
	}

	collector := NewCollector(client)
	result := collector.CollectOS()

	if !strings.Contains(result.Value, "Ubuntu") {
		t.Errorf("expected to contain 'Ubuntu', got %q", result.Value)
	}
}

func TestCollectRAM(t *testing.T) {
	client := &mockSSHClient{
		responses: map[string]string{
			"free -m": "              total        used        free\nMem:           8192        2048        6144\n",
		},
	}

	collector := NewCollector(client)
	result := collector.CollectRAM()

	if result.IntValue != 8192 {
		t.Errorf("expected 8192, got %d", result.IntValue)
	}
}
