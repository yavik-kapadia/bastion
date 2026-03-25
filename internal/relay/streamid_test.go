package relay

import (
	"testing"
)

func TestParseStreamID(t *testing.T) {
	tests := []struct {
		input      string
		wantMode   Mode
		wantName   string
		wantPass   string
		wantErrStr string
	}{
		// Modern format
		{"#!::m=publish,r=mystream", ModePublish, "mystream", "", ""},
		{"#!::m=request,r=mystream", ModeRequest, "mystream", "", ""},
		{"#!::m=play,r=mystream", ModeRequest, "mystream", "", ""},
		{"#!::m=publish,r=mystream,s=secret", ModePublish, "mystream", "secret", ""},
		{"#!::r=mystream,m=publish", ModePublish, "mystream", "", ""},
		// Legacy format
		{"publish/mystream", ModePublish, "mystream", "", ""},
		{"request/mystream", ModeRequest, "mystream", "", ""},
		{"play/mystream", ModeRequest, "mystream", "", ""},
		{"publish/mystream/secret", ModePublish, "mystream", "secret", ""},
		// Errors
		{"", ModePublish, "", "", "empty"},
		{"#!::m=publish", ModePublish, "", "", "missing resource"},
		{"#!::m=bad,r=foo", ModePublish, "", "", "unknown mode"},
		{"badformat", ModePublish, "", "", "expected mode/name"},
		{"unknown/foo", ModePublish, "", "", "unknown mode"},
		{"publish/", ModePublish, "", "", "must not be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			sid, err := ParseStreamID(tt.input)
			if tt.wantErrStr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrStr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sid.Mode != tt.wantMode {
				t.Errorf("mode: got %v, want %v", sid.Mode, tt.wantMode)
			}
			if sid.Name != tt.wantName {
				t.Errorf("name: got %q, want %q", sid.Name, tt.wantName)
			}
			if sid.Passphrase != tt.wantPass {
				t.Errorf("passphrase: got %q, want %q", sid.Passphrase, tt.wantPass)
			}
		})
	}
}
