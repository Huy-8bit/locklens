package targetdb

import "testing"

func TestSecretSettings(t *testing.T) {
	for _, name := range []string{"primary_conninfo", "archive_command", "restore_command", "ssl_passphrase_command", "ssl_key_file"} {
		if !SecretSetting(name) {
			t.Errorf("%s can hold secrets", name)
		}
	}
	for _, name := range []string{"work_mem", "shared_buffers", "archive_mode", "primary_slot_name"} {
		if SecretSetting(name) {
			t.Errorf("%s is not secret", name)
		}
	}
	if a, b := Fingerprint("host=x password=y"), Fingerprint("host=x password=y"); a != b || a == "host=x password=y" {
		t.Fatalf("fingerprints %q %q", a, b)
	}
}
