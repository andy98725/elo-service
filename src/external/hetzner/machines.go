package hetzner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type HetznerConnection struct {
	client *hcloud.Client
}

func InitHetznerConnection(token string) (*HetznerConnection, error) {
	client := hcloud.NewClient(hcloud.WithToken(token))
	return &HetznerConnection{client: client}, nil
}

// ValidateServerType checks that the given server type name is available and not deprecated.
// Should be called at startup to catch misconfigured HCLOUD_HOST_TYPE early.
func (h *HetznerConnection) ValidateServerType(ctx context.Context, serverType string) error {
	types, err := h.client.ServerType.All(ctx)
	if err != nil {
		return fmt.Errorf("could not fetch Hetzner server types: %w", err)
	}
	for _, t := range types {
		if t.Name == serverType {
			return nil
		}
	}
	return fmt.Errorf("HCLOUD_HOST_TYPE %q is not available or has been deprecated; available types: run list_server_types to see current options", serverType)
}

type HostConnectionInfo struct {
	ProviderID string
	PublicIP   string
	AgentPort  int64
	AgentToken string
}

// CreateHost provisions a new Hetzner VM that runs the game-server-host-agent.
// Blocks until the agent is reachable (VM fully booted and agent running).
//
// When `tls` is non-nil, Caddy is co-installed and the agent is told to
// shift its docker host-port bindings by internalPortShift; clients then
// connect over TLS to the public port range while the actual game container
// is bound on port+shift internally.
func (h *HetznerConnection) CreateHost(ctx context.Context, serverType string, agentPort int64, tls *HostTLSOpts) (*HostConnectionInfo, error) {
	agentToken, err := GenerateToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate agent token: %w", err)
	}

	userData := hostCloudConfig(agentPort, agentToken, tls)
	serverName := fmt.Sprintf("game-host-%d", time.Now().Unix())

	slog.Info("Creating host VM", "serverName", serverName, "serverType", serverType)
	createOpts := hcloud.ServerCreateOpts{
		Name:      serverName,
		ServerType: &hcloud.ServerType{Name: serverType, Architecture: hcloud.ArchitectureX86, CPUType: hcloud.CPUTypeShared},
		Image:     &hcloud.Image{Name: "ubuntu-24.04"},
		Location:  &hcloud.Location{Name: "nbg1"},
		UserData:  userData,
		PublicNet: &hcloud.ServerCreatePublicNet{EnableIPv4: true, EnableIPv6: false},
		Labels:    map[string]string{"role": "game-host"},
	}

	result, _, err := h.client.Server.Create(ctx, createOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create host VM: %w", err)
	}

	slog.Info("Host VM creating, waiting for IP", "serverName", serverName)
	var publicIP string
	for {
		srv, _, err := h.client.Server.GetByID(ctx, result.Server.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to poll host VM: %w", err)
		}
		if srv.PublicNet.IPv4.IP != nil {
			publicIP = srv.PublicNet.IPv4.IP.String()
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	slog.Info("Host VM has IP, waiting for agent", "ip", publicIP, "agentPort", agentPort)
	if err := waitForAgent(ctx, publicIP, agentPort); err != nil {
		return nil, fmt.Errorf("agent did not become ready: %w", err)
	}

	slog.Info("Host VM ready", "serverName", serverName, "ip", publicIP)
	return &HostConnectionInfo{
		ProviderID: strconv.FormatInt(result.Server.ID, 10),
		PublicIP:   publicIP,
		AgentPort:  agentPort,
		AgentToken: agentToken,
	}, nil
}

// ListHosts returns the provider IDs of every VM in the account labeled
// role=game-host (the label CreateHost stamps on every VM it creates).
// Used by the reconciliation pass to detect DB rows whose underlying VM
// no longer exists at Hetzner — a host destroyed manually via the
// console, or by an earlier matchmaker bug, will linger as status='ready'
// otherwise and the matchmaker keeps using its stale agent token against
// whatever new VM took over its IP, yielding indefinite 401s.
func (h *HetznerConnection) ListHosts(ctx context.Context) ([]string, error) {
	servers, err := h.client.Server.AllWithOpts(ctx, hcloud.ServerListOpts{
		ListOpts: hcloud.ListOpts{LabelSelector: "role=game-host"},
	})
	if err != nil {
		return nil, fmt.Errorf("listing hetzner servers: %w", err)
	}
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		out = append(out, strconv.FormatInt(s.ID, 10))
	}
	return out, nil
}

// DeleteHost shuts down and permanently deletes a host VM.
func (h *HetznerConnection) DeleteHost(ctx context.Context, providerID string) error {
	id, err := strconv.ParseInt(providerID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid providerID %q: %w", providerID, err)
	}

	server := &hcloud.Server{ID: id}

	shutdown, _, err := h.client.Server.Shutdown(ctx, server)
	if err != nil {
		return fmt.Errorf("failed to shutdown host VM: %w", err)
	}
	h.client.Action.WaitFor(ctx, shutdown)

	del, _, err := h.client.Server.DeleteWithResult(ctx, server)
	if err != nil {
		return fmt.Errorf("failed to delete host VM: %w", err)
	}
	if del.Action.Status != "success" {
		return fmt.Errorf("delete action did not succeed: %s", del.Action.Status)
	}

	slog.Info("Host VM deleted", "providerID", providerID)
	return nil
}

// GenerateToken produces a random 32-byte hex token suitable for agent and game-server auth.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	token = strings.ReplaceAll(token, " ", "-")
	token = strings.ReplaceAll(token, "/", "-")
	return token, nil
}

// waitForAgent polls GET http://{ip}:{port}/health until it receives 200 or the context is cancelled.
func waitForAgent(ctx context.Context, ip string, port int64) error {
	url := fmt.Sprintf("http://%s:%d/health", ip, port)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(5 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timed out waiting for agent at %s", url)
			}
			resp, err := http.Get(url)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
	}
}

// internalPortShift is added to every public game port to derive the
// docker-bound internal host port. Caddy listens on the original port
// range and reverse-proxies to localhost:port+shift. The agent sees
// this as the INTERNAL_PORT_SHIFT env on TLS-enabled hosts.
const internalPortShift = 10000

// HostTLSOpts carries the wildcard-TLS configuration for a host's
// cloud-init. When set on CreateHost, Caddy is installed alongside the
// agent: Caddy holds the wildcard cert and listens on every port in
// [PortRangeStart, PortRangeEnd], reverse-proxying to that port shifted
// by internalPortShift. The agent then binds docker host ports as
// publicPort+shift instead of publicPort.
type HostTLSOpts struct {
	CertPEM        []byte
	KeyPEM         []byte
	PortRangeStart int64
	PortRangeEnd   int64
}

// hostCloudConfig returns the cloud-init user-data string for a host VM.
// Legacy variant just installs Docker and starts the agent. TLS variant
// also drops the wildcard cert + a Caddyfile and runs Caddy as a sibling
// container on the host network.
const hostCloudConfigLegacyTemplate = `#cloud-config
package_update: true
packages:
  - docker.io

runcmd:
  - systemctl start docker
  - docker pull docker.io/andy98725/game-server-host-agent:latest
  - docker run -d --name game-server-agent
      --restart always
      -p %d:8080
      -v /var/run/docker.sock:/var/run/docker.sock
      -e AGENT_TOKEN=%s
      docker.io/andy98725/game-server-host-agent:latest
`

// hostCloudConfigTLSTemplate placeholders (in order):
//   1: cert PEM, indented by 6 spaces for the YAML literal block
//   2: key PEM, indented by 6 spaces
//   3: agent port (host-side, where matchmaker calls)
//   4: agent token
//   5: internal port shift (passed as env to agent for docker bind)
//   6: port range start
//   7: port range end
//   8: cert path on host (string-literal "/etc/caddy/cert.pem")
//   9: key path on host
//
// The "generate Caddyfile" loop produces one stanza per port:
//
//	:P { tls /etc/caddy/cert.pem /etc/caddy/key.pem; reverse_proxy localhost:(P+shift) }
//
// We do this in shell rather than emitting 2000+ stanzas inline to keep
// the cloud-init document compact. cloud-init's user-data is capped at
// 32 KB and an inlined Caddyfile would eat most of that.
const hostCloudConfigTLSTemplate = `#cloud-config
package_update: true
packages:
  - docker.io

write_files:
  - path: /etc/caddy/cert.pem
    permissions: '0640'
    content: |
%s
  - path: /etc/caddy/key.pem
    permissions: '0640'
    content: |
%s
  - path: /etc/caddy/Caddyfile.head
    permissions: '0644'
    content: |
      {
        auto_https off
        servers {
          protocols h1 h2
        }
      }

runcmd:
  - systemctl start docker
  # Generate per-port reverse_proxy stanzas for the entire game-port range.
  - bash -c 'cp /etc/caddy/Caddyfile.head /etc/caddy/Caddyfile && for p in $(seq %d %d); do if [ "$p" -eq %d ]; then continue; fi; printf ":%%s {\n  tls /etc/caddy/cert.pem /etc/caddy/key.pem\n  reverse_proxy localhost:%%s\n}\n" "$p" "$((p+%d))" >> /etc/caddy/Caddyfile; done'
  - docker pull caddy:2
  - docker run -d --name caddy
      --restart always
      --network host
      -v /etc/caddy:/etc/caddy:ro
      caddy:2 caddy run --config /etc/caddy/Caddyfile
  - docker pull docker.io/andy98725/game-server-host-agent:latest
  - docker run -d --name game-server-agent
      --restart always
      -p %d:8080
      -v /var/run/docker.sock:/var/run/docker.sock
      -e AGENT_TOKEN=%s
      -e INTERNAL_PORT_SHIFT=%d
      docker.io/andy98725/game-server-host-agent:latest
`

func hostCloudConfig(agentPort int64, agentToken string, tls *HostTLSOpts) string {
	if tls == nil {
		return fmt.Sprintf(hostCloudConfigLegacyTemplate, agentPort, agentToken)
	}
	// Caddy listens on every port in the range EXCEPT the agent port, which
	// is bound by the agent container. Agent port is normally inside
	// [PortRangeStart, PortRangeEnd] (default 8080 in 7000-9000), so the
	// shell loop skips it explicitly.
	return fmt.Sprintf(hostCloudConfigTLSTemplate,
		indent(string(tls.CertPEM), 6),
		indent(string(tls.KeyPEM), 6),
		tls.PortRangeStart, tls.PortRangeEnd, agentPort, internalPortShift,
		agentPort, agentToken, internalPortShift,
	)
}

// indent prefixes every line with n spaces. Used to embed multi-line PEM
// blocks inside YAML literal blocks where the continuation indent must
// exceed the key's indent.
func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = pad + line
	}
	return strings.Join(lines, "\n")
}
