package hetzner

import (
	"strings"
	"testing"
)

// TestHostCloudConfig_Legacy asserts the no-TLS path matches what the
// service deploys today: docker only, agent container, no Caddy.
func TestHostCloudConfig_Legacy(t *testing.T) {
	out := hostCloudConfig(8080, "tok-abc", nil)

	for _, s := range []string{
		"-p 8080:8080",
		"-e AGENT_TOKEN=tok-abc",
		"docker.io/andy98725/game-server-host-agent:latest",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("legacy cloud-init missing %q\nout:\n%s", s, out)
		}
	}
	for _, s := range []string{
		"caddy", "Caddyfile", "INTERNAL_PORT_SHIFT", "/etc/caddy/cert.pem",
	} {
		if strings.Contains(out, s) {
			t.Errorf("legacy cloud-init unexpectedly contains TLS artifact %q", s)
		}
	}
}

// TestHostCloudConfig_TLS asserts the TLS path:
// - cert + key are inlined as YAML literal blocks at the right indent
// - Caddy is installed and runs on host network
// - the agent gets INTERNAL_PORT_SHIFT
// - the boot-time loop generates one stanza per port in the configured range
func TestHostCloudConfig_TLS(t *testing.T) {
	cert := []byte("-----BEGIN CERTIFICATE-----\nMIICERT\n-----END CERTIFICATE-----\n")
	key := []byte("-----BEGIN EC PRIVATE KEY-----\nMIIKEY\n-----END EC PRIVATE KEY-----\n")
	out := hostCloudConfig(8080, "tok-abc", &HostTLSOpts{
		CertPEM:        cert,
		KeyPEM:         key,
		PortRangeStart: 7000,
		PortRangeEnd:   9000,
	})

	for _, s := range []string{
		// Cert + key are written via cloud-init write_files.
		"/etc/caddy/cert.pem",
		"/etc/caddy/key.pem",
		"MIICERT",
		"MIIKEY",
		// Cert content indented for YAML literal block (6 spaces).
		"      -----BEGIN CERTIFICATE-----",
		"      -----BEGIN EC PRIVATE KEY-----",
		// Caddy runs as a sibling docker container on host net.
		"docker run -d --name caddy",
		"--network host",
		// Stanza generation loop. We assert the literal pieces — actual
		// stanza generation happens at boot, not in this template.
		"for p in $(seq 7000 9000)",
		`"$((p+10000))"`, // shifted host port passed as printf arg
		`reverse_proxy localhost:%s`,
		// Caddyfile uses newline-separated directives within a site block;
		// the inline ; form Caddy doesn't accept tripped a previous deploy.
		`tls /etc/caddy/cert.pem /etc/caddy/key.pem`,
		// Caddy must skip the agent port (8080 here) — agent already
		// binds it, so a Caddy listener on the same port crashes startup
		// with "address already in use".
		`if [ "$p" -eq 8080 ]`,
		// Agent gets the shift via env so its docker port-bindings line up.
		"-e INTERNAL_PORT_SHIFT=10000",
		"-e AGENT_TOKEN=tok-abc",
		"-p 8080:8080",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("TLS cloud-init missing %q\nout:\n%s", s, out)
		}
	}
}

func TestIndent(t *testing.T) {
	got := indent("line1\nline2\nline3", 4)
	want := "    line1\n    line2\n    line3"
	if got != want {
		t.Errorf("indent mismatch\ngot:  %q\nwant: %q", got, want)
	}
}
