package nixdrv

import "testing"

func TestParseStorePath(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		hash    string
		spName  string
		wantErr bool
	}{
		{
			name:   "regular store path",
			input:  "/nix/store/ac8da0sqpg4pyhzyr0qgl26d5dnpn7qp-hello-2.10.tar.gz",
			hash:   "ac8da0sqpg4pyhzyr0qgl26d5dnpn7qp",
			spName: "hello-2.10.tar.gz",
		},
		{
			name:   "derivation path",
			input:  "/nix/store/q3lv9bi7r4di3kxdjhy7kvwgvpmanfza-hello-2.10.drv",
			hash:   "q3lv9bi7r4di3kxdjhy7kvwgvpmanfza",
			spName: "hello-2.10.drv",
		},
		{
			name:    "too short",
			input:   "/nix/store/abc-x",
			wantErr: true,
		},
		{
			name:    "wrong prefix",
			input:   "/tmp/ac8da0sqpg4pyhzyr0qgl26d5dnpn7qp-hello",
			wantErr: true,
		},
		{
			name:    "no dash at position 32",
			input:   "/nix/store/ac8da0sqpg4pyhzyr0qgl26d5dnpn7qphello",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp, err := ParseStorePath(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := sp.HashPart(); got != tt.hash {
				t.Errorf("HashPart() = %q, want %q", got, tt.hash)
			}
			if got := sp.Name(); got != tt.spName {
				t.Errorf("Name() = %q, want %q", got, tt.spName)
			}
			if got := sp.String(); got != tt.input {
				t.Errorf("String() = %q, want %q", got, tt.input)
			}
		})
	}
}

func TestIsDerivation(t *testing.T) {
	drv := MustParseStorePath("/nix/store/q3lv9bi7r4di3kxdjhy7kvwgvpmanfza-hello-2.10.drv")
	if !drv.IsDerivation() {
		t.Error("expected IsDerivation() = true")
	}
	if drv.DrvName() != "hello-2.10" {
		t.Errorf("DrvName() = %q, want %q", drv.DrvName(), "hello-2.10")
	}

	regular := MustParseStorePath("/nix/store/ac8da0sqpg4pyhzyr0qgl26d5dnpn7qp-hello-2.10.tar.gz")
	if regular.IsDerivation() {
		t.Error("expected IsDerivation() = false")
	}
}
