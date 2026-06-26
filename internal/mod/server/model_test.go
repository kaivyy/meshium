package server

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTagsJSONHelpers(t *testing.T) {
	tags := []string{"web", "prod"}
	got := tagsToJSON(tags)
	if got != `["web","prod"]` {
		t.Fatalf("expected JSON array, got %q", got)
	}

	if !reflect.DeepEqual(tagsFromJSON(got), tags) {
		t.Fatalf("expected %v, got %v", tags, tagsFromJSON(got))
	}

	if !reflect.DeepEqual(tagsFromJSON(``), []string{}) {
		t.Fatalf("expected empty slice from empty json string")
	}
}

func TestServerResponseOmitsCredentials(t *testing.T) {
	resp := Server{
		ID:       1,
		Name:     "test",
		Password: "secret",
		SSHKey:   "private-key",
	}

	public := resp.Response()
	data, err := json.Marshal(public)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if string(data) == "" {
		t.Fatal("expected marshaled data")
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty JSON output")
	}
	if contains := string(data); contains == "password" || contains == "sshKey" || contains == "passphrase" {
		t.Fatalf("response leaked credential fields: %s", contains)
	}
}
