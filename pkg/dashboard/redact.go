package dashboard

// MaskedValue replaces every captured secret in API responses unless the
// server was started with --reveal-secrets. Fixed width on purpose: the
// placeholder must not leak the length of the captured value.
const MaskedValue = "••••••"

// secretKeys enumerates every parameter key that carries captured secret
// material, taken from the Record(...) call sites in pkg/flamingo:
//
//	ssh.go   password, pubkey
//	http.go  password (basic auth), authorization (access records), hashcat (NTLM)
//	ldap.go  password
//	ftp.go   password
//	snmp.go  community
//	dns.go   (none — DNS questions are hostnames, not secrets)
//
// pubkey-sha256 deliberately stays readable: it is a non-reversible
// fingerprint, and it is what an analyst correlates repeat attackers by.
var secretKeys = map[string]struct{}{
	"authorization": {},
	"community":     {},
	"hashcat":       {},
	"password":      {},
	"pubkey":        {},
}

// IsSecretKey reports whether the record field carries captured secret
// material.
func IsSecretKey(key string) bool {
	_, ok := secretKeys[key]
	return ok
}

// RedactRecord returns a copy of the record fields with every secret key
// replaced by MaskedValue unless reveal is set. The copy also applies the
// "credential" default for a missing _type, so consumers of the API never
// see a record without a type. The input map is never modified.
//
// There is deliberately no query parameter that can unmask: redaction is
// decided here, from the server-side flag, before serialization.
func RedactRecord(fields map[string]string, reveal bool) map[string]string {
	out := make(map[string]string, len(fields))
	for k, v := range fields {
		if _, secret := secretKeys[k]; secret && !reveal {
			out[k] = MaskedValue
			continue
		}
		out[k] = v
	}
	if out["_type"] == "" {
		out["_type"] = defaultRecordType
	}
	return out
}
