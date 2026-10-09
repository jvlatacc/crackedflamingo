package dashboard

import "testing"

// The secret-key set below is enumerated from the Record(...) call sites
// actually present in pkg/flamingo (verified this session):
//
//   ssh.go   password (password auth), pubkey (pubkey auth)
//   http.go  password (basic), hashcat (NTLMSSP), authorization (access)
//   ldap.go  password
//   ftp.go   password
//   snmp.go  community
//   dns.go   (no secret material — questions stay readable)
//
// pubkey-sha256 is a non-reversible fingerprint and deliberately readable.

func TestRedactSSHPasswordAuth(t *testing.T) {
	fields := RedactRecord(map[string]string{
		"username": "root", "password": "hunter2", "method": "password",
		"version": "SSH-2.0-OpenSSH_9.6",
	}, false)
	if fields["password"] != MaskedValue {
		t.Errorf("ssh password not masked: %q", fields["password"])
	}
	for _, readable := range []string{"username", "method", "version"} {
		if fields[readable] == MaskedValue {
			t.Errorf("ssh %s should stay readable", readable)
		}
	}
}

func TestRedactSSHPubkeyAuth(t *testing.T) {
	fields := RedactRecord(map[string]string{
		"username":      "deploy",
		"pubkey":        "AAAA ssh-ed25519-key-material",
		"pubkey-sha256": "SHA256:ABCD1234",
		"method":        "pubkey",
	}, false)
	if fields["pubkey"] != MaskedValue {
		t.Errorf("ssh pubkey not masked: %q", fields["pubkey"])
	}
	if fields["pubkey-sha256"] != "SHA256:ABCD1234" {
		t.Errorf("pubkey-sha256 fingerprint should stay readable: %q", fields["pubkey-sha256"])
	}
	if fields["username"] != "deploy" {
		t.Errorf("ssh username should stay readable: %q", fields["username"])
	}
}

func TestRedactHTTPBasic(t *testing.T) {
	fields := RedactRecord(map[string]string{
		"username": "admin", "password": "letmein", "method": "basic",
		"agent": "curl/8.5.0", "path": "/admin",
	}, false)
	if fields["password"] != MaskedValue {
		t.Errorf("http basic password not masked: %q", fields["password"])
	}
	if fields["username"] != "admin" || fields["method"] != "basic" {
		t.Errorf("http basic readable fields disturbed: %v", fields)
	}
}

func TestRedactHTTPNTLMSSP(t *testing.T) {
	fields := RedactRecord(map[string]string{
		"username": "corp\\jdoe", "hashcat": "NtOw.5c082ee512dbd123", "method": "NTLMSSP",
	}, false)
	if fields["hashcat"] != MaskedValue {
		t.Errorf("ntlmssp hashcat not masked: %q", fields["hashcat"])
	}
	if fields["username"] != "corp\\jdoe" {
		t.Errorf("ntlmssp username should stay readable: %q", fields["username"])
	}
}

func TestRedactHTTPAccessAuthorization(t *testing.T) {
	fields := RedactRecord(map[string]string{
		"authorization": "Basic YWRtaW46YWRtaW4=", "agent": "masscan/1.3", "path": "/",
	}, false)
	if fields["authorization"] != MaskedValue {
		t.Errorf("http authorization header not masked: %q", fields["authorization"])
	}
	if fields["agent"] != "masscan/1.3" || fields["path"] != "/" {
		t.Errorf("http access readable fields disturbed: %v", fields)
	}
}

func TestRedactLDAP(t *testing.T) {
	fields := RedactRecord(map[string]string{
		"username": "cn=admin,dc=test", "password": "ldapsecret",
	}, false)
	if fields["password"] != MaskedValue {
		t.Errorf("ldap password not masked: %q", fields["password"])
	}
	if fields["username"] != "cn=admin,dc=test" {
		t.Errorf("ldap username should stay readable: %q", fields["username"])
	}
}

func TestRedactFTP(t *testing.T) {
	fields := RedactRecord(map[string]string{
		"username": "backup", "password": "letmein123",
	}, false)
	if fields["password"] != MaskedValue {
		t.Errorf("ftp password not masked: %q", fields["password"])
	}
	if fields["username"] != "backup" {
		t.Errorf("ftp username should stay readable: %q", fields["username"])
	}
}

func TestRedactSNMPCommunity(t *testing.T) {
	fields := RedactRecord(map[string]string{
		"community": "public", "version": "v2c",
	}, false)
	if fields["community"] != MaskedValue {
		t.Errorf("snmp community not masked: %q", fields["community"])
	}
	if fields["version"] != "v2c" {
		t.Errorf("snmp version should stay readable: %q", fields["version"])
	}
}

func TestRedactDNSNothingSecret(t *testing.T) {
	fields := RedactRecord(map[string]string{
		"questions": "example.com/A db.test/A",
	}, false)
	if fields["questions"] != "example.com/A db.test/A" {
		t.Errorf("dns questions should stay readable: %q", fields["questions"])
	}
}

func TestRedactRevealReturnsOriginals(t *testing.T) {
	original := map[string]string{
		"username": "root", "password": "hunter2", "pubkey": "AAAA",
		"authorization": "Basic x", "hashcat": "NtOw.x", "community": "public",
	}
	fields := RedactRecord(original, true)
	for _, key := range []string{"password", "pubkey", "authorization", "hashcat", "community"} {
		if fields[key] != original[key] {
			t.Errorf("reveal: %s = %q, want original %q", key, fields[key], original[key])
		}
	}
}

func TestRedactMaskIsFixedWidth(t *testing.T) {
	// The placeholder must not leak the secret's length.
	short := RedactRecord(map[string]string{"password": "x"}, false)["password"]
	long := RedactRecord(map[string]string{"password": "a-very-long-secret-value-indeed"}, false)["password"]
	if short != long || len(short) != len(MaskedValue) {
		t.Errorf("mask is not fixed width: %q vs %q (const %q)", short, long, MaskedValue)
	}
}

func TestRedactUnknownKeysPassThrough(t *testing.T) {
	fields := RedactRecord(map[string]string{"custom_field": "keepme"}, false)
	if fields["custom_field"] != "keepme" {
		t.Errorf("non-secret passthrough broken: %v", fields)
	}
}
