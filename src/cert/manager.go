// Package cert manages a single wildcard TLS certificate for the
// game-server fleet.
//
// Why a single wildcard instead of per-VM certs:
// Let's Encrypt limits issuance to 50 certificates per registered domain
// per rolling 7-day window. Per-VM certs trip that limit at any non-trivial
// host churn. A single *.<GameServerDomain> cert renewed every ~60 days
// uses 1 cert/quarter regardless of how many hosts come and go.
//
// The cert+key are persisted in S3 (same bucket the matchmaker already uses
// for match logs, under a separate prefix) and inlined into the cloud-init
// of each new Hetzner host VM. Caddy on the host serves traffic with the
// cert directly — no per-host ACME, no DNS-01 from inside Hetzner.
package cert

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"
)

// renewalThreshold is the remaining-validity window below which EnsureFresh
// will renew the cert. Let's Encrypt issues 90-day certs; renewing at 30
// days remaining leaves room for retries if the renewal fails.
const renewalThreshold = 30 * 24 * time.Hour

// Storage is the persistence interface the manager depends on. The
// production implementation is backed by the existing AWSClient; tests can
// supply an in-memory substitute.
type Storage interface {
	PutObject(ctx context.Context, key string, body []byte) error
	GetObject(ctx context.Context, key string) ([]byte, error)
}

// IsNotFoundFunc lets the manager distinguish "no cert yet" from real
// storage errors without coupling to a specific backend's error type.
type IsNotFoundFunc func(error) bool

const (
	keyAccount = "wildcard-tls/account.json"
	keyCert    = "wildcard-tls/cert.pem"
	keyCertKey = "wildcard-tls/key.pem"
)

// Config wires the manager to its dependencies.
type Config struct {
	// Domain is the bare zone fragment under which the wildcard is issued,
	// e.g. "gs.elomm.net". The cert subject becomes "*.<Domain>".
	Domain string
	// CloudflareToken authorizes both the runtime DNS-record CRUD (in the
	// cloudflare package) AND lego's DNS-01 challenges; both share the
	// same Zone:DNS:Edit scope.
	CloudflareToken string
	// Email is the ACME contact address (Let's Encrypt requires one).
	Email string
	// Storage is where the cert, key, and ACME account are persisted across
	// matchmaker restarts.
	Storage    Storage
	IsNotFound IsNotFoundFunc
}

// Manager issues + renews the wildcard cert. The current cert is held in
// memory; consumers call CurrentPEM to read it.
type Manager struct {
	cfg Config

	mu         sync.RWMutex
	certPEM    []byte
	keyPEM     []byte
	expiresAt  time.Time
	user       *acmeUser
	legoClient *lego.Client
}

// acmeUser is what lego requires for ACME account state. The key is
// generated once and kept in S3 so we don't re-register on every restart
// (each registration counts against rate limits, plus we'd lose history).
type acmeUser struct {
	Email        string                 `json:"email"`
	Registration *registration.Resource `json:"registration"`
	KeyPEM       []byte                 `json:"key_pem"`
	key          crypto.PrivateKey
}

func (u *acmeUser) GetEmail() string                        { return u.Email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.Registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.key }

func New(cfg Config) (*Manager, error) {
	if cfg.Domain == "" || cfg.CloudflareToken == "" || cfg.Email == "" {
		return nil, errors.New("cert: Domain, CloudflareToken, and Email are required")
	}
	if cfg.Storage == nil {
		return nil, errors.New("cert: Storage is required")
	}
	if cfg.IsNotFound == nil {
		return nil, errors.New("cert: IsNotFound is required")
	}
	return &Manager{cfg: cfg}, nil
}

// EnsureFresh loads the cert+account from storage, requests a new cert if
// none exists, and renews if the current one is within the renewal window.
// Idempotent and safe to call repeatedly.
func (m *Manager) EnsureFresh(ctx context.Context) error {
	if err := m.ensureUser(ctx); err != nil {
		return fmt.Errorf("acme account: %w", err)
	}
	if err := m.loadExistingCert(ctx); err != nil {
		return fmt.Errorf("load cert: %w", err)
	}

	m.mu.RLock()
	hasCert := len(m.certPEM) > 0
	expiresIn := time.Until(m.expiresAt)
	m.mu.RUnlock()

	switch {
	case !hasCert:
		slog.Info("cert: no cert in storage, issuing new wildcard", "domain", "*."+m.cfg.Domain)
		return m.obtain(ctx)
	case expiresIn < renewalThreshold:
		slog.Info("cert: renewing wildcard", "domain", "*."+m.cfg.Domain, "expires_in", expiresIn.Round(time.Hour))
		return m.renew(ctx)
	default:
		slog.Debug("cert: existing cert is fresh", "expires_in", expiresIn.Round(time.Hour))
		return nil
	}
}

// CurrentPEM returns the cert chain and key as PEM bytes, suitable for
// inlining into a cloud-init document. Returns an error if no cert has been
// loaded yet (caller must invoke EnsureFresh first).
func (m *Manager) CurrentPEM() (cert, key []byte, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.certPEM) == 0 || len(m.keyPEM) == 0 {
		return nil, nil, errors.New("cert: no cert loaded; call EnsureFresh first")
	}
	return append([]byte(nil), m.certPEM...), append([]byte(nil), m.keyPEM...), nil
}

func (m *Manager) ensureUser(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.legoClient != nil {
		return nil
	}

	user := &acmeUser{Email: m.cfg.Email}

	if raw, err := m.cfg.Storage.GetObject(ctx, keyAccount); err == nil {
		if err := json.Unmarshal(raw, user); err != nil {
			return fmt.Errorf("decode acme account: %w", err)
		}
		k, err := decodeECPrivateKey(user.KeyPEM)
		if err != nil {
			return fmt.Errorf("decode acme account key: %w", err)
		}
		user.key = k
	} else if m.cfg.IsNotFound(err) {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return fmt.Errorf("generate acme key: %w", err)
		}
		pemBytes, err := encodeECPrivateKey(k)
		if err != nil {
			return fmt.Errorf("encode acme key: %w", err)
		}
		user.key = k
		user.KeyPEM = pemBytes
	} else {
		return fmt.Errorf("get account: %w", err)
	}

	config := lego.NewConfig(user)
	config.Certificate.KeyType = certcrypto.RSA2048

	client, err := lego.NewClient(config)
	if err != nil {
		return fmt.Errorf("lego client: %w", err)
	}

	provider, err := cloudflare.NewDNSProviderConfig(&cloudflare.Config{
		AuthToken:          m.cfg.CloudflareToken,
		PropagationTimeout: 2 * time.Minute,
		PollingInterval:    2 * time.Second,
		// 120 is lego's minimum TTL for the temporary _acme-challenge TXT
		// records it creates during DNS-01 verification. The cert manager
		// only writes these for ~30s during issuance/renewal, so the
		// floor doesn't matter operationally — it just has to satisfy
		// lego's validation. Per-host A records (in
		// src/external/cloudflare/cloudflare.go) still use TTL=60.
		TTL: 120,
	})
	if err != nil {
		return fmt.Errorf("cloudflare dns provider: %w", err)
	}
	if err := client.Challenge.SetDNS01Provider(provider); err != nil {
		return fmt.Errorf("set dns01 provider: %w", err)
	}

	if user.Registration == nil {
		reg, err := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return fmt.Errorf("acme register: %w", err)
		}
		user.Registration = reg
		raw, err := json.Marshal(user)
		if err != nil {
			return fmt.Errorf("encode acme account: %w", err)
		}
		if err := m.cfg.Storage.PutObject(ctx, keyAccount, raw); err != nil {
			return fmt.Errorf("save acme account: %w", err)
		}
		slog.Info("cert: registered new ACME account", "email", m.cfg.Email)
	}

	m.user = user
	m.legoClient = client
	return nil
}

func (m *Manager) loadExistingCert(ctx context.Context) error {
	cert, err := m.cfg.Storage.GetObject(ctx, keyCert)
	if err != nil {
		if m.cfg.IsNotFound(err) {
			return nil
		}
		return err
	}
	key, err := m.cfg.Storage.GetObject(ctx, keyCertKey)
	if err != nil {
		if m.cfg.IsNotFound(err) {
			return nil
		}
		return err
	}

	leaf, err := parseLeafCert(cert)
	if err != nil {
		return fmt.Errorf("parse cert: %w", err)
	}

	m.mu.Lock()
	m.certPEM = cert
	m.keyPEM = key
	m.expiresAt = leaf.NotAfter
	m.mu.Unlock()
	return nil
}

func (m *Manager) obtain(ctx context.Context) error {
	res, err := m.legoClient.Certificate.Obtain(certificate.ObtainRequest{
		Domains: []string{"*." + m.cfg.Domain},
		Bundle:  true,
	})
	if err != nil {
		return fmt.Errorf("acme obtain: %w", err)
	}
	return m.persist(ctx, res)
}

func (m *Manager) renew(ctx context.Context) error {
	m.mu.RLock()
	cert, key := m.certPEM, m.keyPEM
	m.mu.RUnlock()

	res, err := m.legoClient.Certificate.RenewWithOptions(certificate.Resource{
		Domain:      "*." + m.cfg.Domain,
		Certificate: cert,
		PrivateKey:  key,
	}, &certificate.RenewOptions{Bundle: true})
	if err != nil {
		return fmt.Errorf("acme renew: %w", err)
	}
	return m.persist(ctx, res)
}

func (m *Manager) persist(ctx context.Context, res *certificate.Resource) error {
	if err := m.cfg.Storage.PutObject(ctx, keyCert, res.Certificate); err != nil {
		return fmt.Errorf("save cert: %w", err)
	}
	if err := m.cfg.Storage.PutObject(ctx, keyCertKey, res.PrivateKey); err != nil {
		return fmt.Errorf("save key: %w", err)
	}

	leaf, err := parseLeafCert(res.Certificate)
	if err != nil {
		return fmt.Errorf("parse new cert: %w", err)
	}

	m.mu.Lock()
	m.certPEM = res.Certificate
	m.keyPEM = res.PrivateKey
	m.expiresAt = leaf.NotAfter
	m.mu.Unlock()

	slog.Info("cert: persisted wildcard cert", "domain", "*."+m.cfg.Domain, "not_after", leaf.NotAfter)
	return nil
}

// parseLeafCert pulls the first PEM block from a bundle. Lego always orders
// bundles leaf-first, so the first block is the one whose NotAfter we care
// about.
func parseLeafCert(pemBundle []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBundle)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	return x509.ParseCertificate(block.Bytes)
}

func encodeECPrivateKey(k *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

func decodeECPrivateKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block in account key")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}
