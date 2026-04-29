package server

import (
	"context"
	"io"

	"github.com/andy98725/elo-service/src/external/aws"
	"github.com/andy98725/elo-service/src/external/hetzner"
)

// MachineService manages long-lived host VMs that run multiple game
// containers each. The matchmaker provisions hosts on demand (or warmly
// via the warm pool) and tears them down through this interface.
type MachineService interface {
	// ValidateServerType is called at startup so a misconfigured
	// HCLOUD_HOST_TYPE fails loudly instead of silently at first match.
	ValidateServerType(ctx context.Context, serverType string) error
	// CreateHost provisions a new host VM. When `tls` is non-nil, the
	// host is brought up with Caddy + the wildcard cert pre-installed.
	CreateHost(ctx context.Context, serverType string, agentPort int64, tls *hetzner.HostTLSOpts) (*hetzner.HostConnectionInfo, error)
	DeleteHost(ctx context.Context, providerID string) error
	// ListHosts returns the provider IDs of every game-host VM currently
	// alive at the provider. Used by ReconcileLiveHosts to detect DB rows
	// whose underlying VM was destroyed out-of-band.
	ListHosts(ctx context.Context) ([]string, error)
}

type StorageService interface {
	UploadLogs(ctx context.Context, body []byte) (string, error)
	GetLogs(ctx context.Context, key string) (io.ReadCloser, error)

	// Generic blob get/put used by the cert manager. Implementations
	// signal "key does not exist" via an error that the cert manager
	// detects through the AWSClient.ErrNotFound sentinel.
	PutObject(ctx context.Context, key string, body []byte) error
	GetObject(ctx context.Context, key string) ([]byte, error)

	// Spectator stream: chunked per-match objects under live/<matchID>/
	// during the match. Manifest is rewritten after each chunk so a
	// spectator polling the route can find the latest seq cheaply
	// (one GET) without listing the prefix. Slice 4 will move objects
	// from live/ to replay/ on EndMatch and apply a 7-day lifecycle to
	// replay/.
	PutSpectateChunk(ctx context.Context, matchID string, seq int, data []byte) error
	PutSpectateManifest(ctx context.Context, matchID string, manifest []byte) error

	// GetSpectateManifest returns the manifest bytes — preferring
	// replay/<matchID>/manifest.json (post-EndMatch) and falling back
	// to live/<matchID>/manifest.json for in-progress matches. Returns
	// ErrNotFound when neither prefix has the match.
	GetSpectateManifest(ctx context.Context, matchID string) ([]byte, error)
	// GetSpectateChunk reads one sequenced chunk, applying the same
	// replay-then-live preference as GetSpectateManifest. Slice 4
	// guarantees a finalized replay/ manifest is only written after all
	// replay/ chunks land, so a spectator that sees replay's manifest
	// will find every chunk it points at.
	GetSpectateChunk(ctx context.Context, matchID string, seq int) ([]byte, error)
	// MoveSpectateLiveToReplay copies every live/<matchID>/* object to
	// the replay/ prefix, writes a finalized manifest there, and deletes
	// the live/ versions. Order: copy chunks → put finalized replay
	// manifest → delete live chunks → delete live manifest. Spectators
	// only ever see one consistent prefix per request.
	MoveSpectateLiveToReplay(ctx context.Context, matchID string) error

	// PutMatchArtifact stores one named artifact at
	// artifacts/<matchID>/<name> and updates the per-match index.json
	// with the artifact's metadata. The implementation is responsible
	// for the read-modify-write of the index — concurrent uploads to
	// the same match can race, but game servers typically don't.
	PutMatchArtifact(ctx context.Context, matchID, name, contentType string, body []byte) error
	// GetMatchArtifact returns the bytes and stored content-type for
	// one artifact. ErrNotFound when the artifact doesn't exist.
	GetMatchArtifact(ctx context.Context, matchID, name string) (body []byte, contentType string, err error)
	// GetMatchArtifactIndex returns the parsed metadata for every
	// artifact in this match. Empty map (no error) when the match has
	// no artifacts. The metadata type lives in the aws package — server
	// already imports aws to construct AWSClient, so referencing it here
	// avoids defining the same shape twice.
	GetMatchArtifactIndex(ctx context.Context, matchID string) (map[string]aws.MatchArtifactMeta, error)
}

// DNSService is the per-host DNS-record CRUD surface. Production is satisfied
// by cloudflare.Client; tests pass a no-op or in-memory mock.
type DNSService interface {
	CreateARecord(ctx context.Context, name, ip string) (recordID string, err error)
	DeleteARecord(ctx context.Context, recordID string) error
	FindARecordByName(ctx context.Context, name string) (recordID string, err error)
}

// CertService exposes the current wildcard cert+key. Cloud-init reads this
// when building a host's user-data. Returns an error if no cert is loaded
// yet (caller must wait or skip).
type CertService interface {
	CurrentPEM() (cert, key []byte, err error)
	// EnsureFresh is invoked periodically by the worker to renew the cert
	// before it expires. Implementations must be idempotent — a no-op when
	// the current cert has plenty of life left.
	EnsureFresh(ctx context.Context) error
}
