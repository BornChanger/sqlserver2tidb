package redact

import (
	"strings"
	"testing"
)

func TestTextRedactsSensitiveValuesButKeepsHashes(t *testing.T) {
	input := strings.Join([]string{
		"password=raw-password token=raw-token AWS_SECRET_ACCESS_KEY=raw-aws-secret",
		"sqlserver://user:raw-url-password@db.example:1433?password=raw-query-password",
		"https://object-store.example/full/orders.csv?sig=raw-sas-signature&token=raw-url-token",
		"output sha256: sha256:1111111111111111111111111111111111111111111111111111111111111111",
	}, "\n")

	got := Text(input)

	for _, secret := range []string{
		"raw-password",
		"raw-token",
		"raw-aws-secret",
		"raw-url-password",
		"raw-query-password",
		"raw-sas-signature",
		"raw-url-token",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("Text() leaked %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, "<redacted>") {
		t.Fatalf("Text() = %q, want redaction marker", got)
	}
	if !strings.Contains(got, "sha256:1111111111111111111111111111111111111111111111111111111111111111") {
		t.Fatalf("Text() = %q, want SHA256 digest preserved", got)
	}
}

func TestArgsRedactsSensitiveFlagValues(t *testing.T) {
	args := []string{
		"sqlserver2tidb-executor",
		"export",
		"--password",
		"raw-password",
		"--token=raw-token",
		"--output-uri",
		"https://object-store.example/full/orders.csv?sig=raw-sas-signature&token=raw-url-token",
	}

	got := Args(args)

	for _, secret := range []string{"raw-password", "raw-token", "raw-sas-signature", "raw-url-token"} {
		if strings.Contains(strings.Join(got, " "), secret) {
			t.Fatalf("Args() leaked %q in %#v", secret, got)
		}
	}
	if got[3] != "<redacted>" {
		t.Fatalf("Args()[3] = %q, want redacted password flag value", got[3])
	}
	if got[4] != "--token=<redacted>" {
		t.Fatalf("Args()[4] = %q, want redacted token flag value", got[4])
	}
}
