package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config is the server configuration, read from environment variables.
type Config struct {
	Listen            string
	StoreDSN          string
	MasterKey         string
	AdminUser         string
	AdminPassword     string
	AdminPasswordHash string
	CookieSecure      bool
	SessionIdle       time.Duration
	HistoryRetention  time.Duration
	IncidentRetention time.Duration
	FindingRetention  time.Duration
	MaxConcurrent     int
	WriteQueue        int
	MetricsEnabled    bool
	BootstrapTargets  string
	DemoMode          bool
	// LoadSampling turns active session sampling on; LoadSampleInterval is
	// the time between two samples of pg_stat_activity per target.
	LoadSampling       bool
	LoadSampleInterval time.Duration
	// PublicURL is LockLens' address as users reach it, for links in
	// notifications (for example https://locklens.example.com).
	PublicURL string
	// StoreTimescale says whether the monitoring store uses TimescaleDB.
	StoreTimescale StoreTimescale
	// TrustedProxies are the networks of reverse proxies (the frontend) whose
	// X-Forwarded-For header names the real client; TrustedProxyHosts are
	// host names of such proxies, resolved periodically (for example the
	// frontend's Compose service name).
	TrustedProxies    []netip.Prefix
	TrustedProxyHosts []string
	LogLevel          string
	LogFormat         string
	ShutdownTimeout   time.Duration
}

// LoadConfig reads the configuration. getenv is os.Getenv in production.
func LoadConfig(getenv func(string) string) (Config, error) {
	c := Config{
		Listen:            def(getenv("LOCKLENS_LISTEN"), "127.0.0.1:8080"),
		StoreDSN:          getenv("LOCKLENS_STORE_DSN"),
		MasterKey:         getenv("LOCKLENS_MASTER_KEY"),
		AdminUser:         def(getenv("LOCKLENS_ADMIN_USER"), "admin"),
		AdminPassword:     getenv("LOCKLENS_ADMIN_PASSWORD"),
		AdminPasswordHash: getenv("LOCKLENS_ADMIN_PASSWORD_HASH"),
		BootstrapTargets:  getenv("LOCKLENS_BOOTSTRAP_TARGETS_FILE"),
		LogLevel:          def(getenv("LOCKLENS_LOG_LEVEL"), "info"),
		LogFormat:         def(getenv("LOCKLENS_LOG_FORMAT"), "text"),
	}
	var errs []error
	dur := func(name string, d time.Duration, min time.Duration) time.Duration {
		v := getenv(name)
		if v == "" {
			return d
		}
		x, err := time.ParseDuration(v)
		if err != nil || x < min {
			errs = append(errs, fmt.Errorf("%s must be a duration of at least %s (for example 168h)", name, min))
			return d
		}
		return x
	}
	num := func(name string, d, lo, hi int) int {
		v := getenv(name)
		if v == "" {
			return d
		}
		x, err := strconv.Atoi(v)
		if err != nil || x < lo || x > hi {
			errs = append(errs, fmt.Errorf("%s must be an integer between %d and %d", name, lo, hi))
			return d
		}
		return x
	}
	boolean := func(name string, d bool) bool {
		switch strings.ToLower(getenv(name)) {
		case "":
			return d
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
		errs = append(errs, fmt.Errorf("%s must be true or false", name))
		return d
	}
	c.SessionIdle = dur("LOCKLENS_SESSION_IDLE", 12*time.Hour, 5*time.Minute)
	c.HistoryRetention = dur("LOCKLENS_RETENTION", 7*24*time.Hour, time.Hour)
	c.IncidentRetention = dur("LOCKLENS_INCIDENT_RETENTION", 90*24*time.Hour, 24*time.Hour)
	c.FindingRetention = dur("LOCKLENS_FINDING_RETENTION", 30*24*time.Hour, time.Hour)
	c.ShutdownTimeout = dur("LOCKLENS_SHUTDOWN_TIMEOUT", 10*time.Second, time.Second)
	c.MaxConcurrent = num("LOCKLENS_MAX_CONCURRENT_COLLECTIONS", 4, 1, 64)
	c.WriteQueue = num("LOCKLENS_WRITE_QUEUE", 2048, 16, 100000)
	c.MetricsEnabled = boolean("LOCKLENS_METRICS_ENABLED", true)
	c.DemoMode = boolean("LOCKLENS_DEMO_MODE", false)
	c.LoadSampling = boolean("LOCKLENS_LOAD_SAMPLING", true)
	if st, err := LoadStoreTimescale(getenv); err != nil {
		errs = append(errs, err)
	} else {
		c.StoreTimescale = st
	}
	c.LoadSampleInterval = dur("LOCKLENS_LOAD_SAMPLE_INTERVAL", 2*time.Second, time.Second)
	if v := strings.TrimRight(strings.TrimSpace(getenv("LOCKLENS_PUBLIC_URL")), "/"); v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" {
			errs = append(errs, errors.New("LOCKLENS_PUBLIC_URL must be an http or https URL such as https://locklens.example.com"))
		} else {
			c.PublicURL = v
		}
	}
	if c.LoadSampleInterval > time.Minute {
		errs = append(errs, errors.New("LOCKLENS_LOAD_SAMPLE_INTERVAL must be at most 1m"))
	}
	for _, part := range strings.Split(getenv("LOCKLENS_TRUSTED_PROXIES"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			if a, aerr := netip.ParseAddr(part); aerr == nil {
				p = netip.PrefixFrom(a, a.BitLen())
			} else if hostPattern.MatchString(part) {
				c.TrustedProxyHosts = append(c.TrustedProxyHosts, strings.ToLower(part))
				continue
			} else {
				errs = append(errs, fmt.Errorf("LOCKLENS_TRUSTED_PROXIES: %q is not an IP address, CIDR, or host name", part))
				continue
			}
		}
		c.TrustedProxies = append(c.TrustedProxies, p.Masked())
	}

	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		errs = append(errs, errors.New("LOCKLENS_LISTEN must be host:port, for example 127.0.0.1:8080"))
	}
	loopback := host == "localhost"
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		loopback = true
	}
	switch strings.ToLower(def(getenv("LOCKLENS_COOKIE_SECURE"), "auto")) {
	case "auto":
		// Over plain HTTP on loopback a Secure cookie would never be sent.
		c.CookieSecure = !loopback
	case "true", "1":
		c.CookieSecure = true
	case "false", "0":
		c.CookieSecure = false
	default:
		errs = append(errs, errors.New("LOCKLENS_COOKIE_SECURE must be auto, true, or false"))
	}
	if c.StoreDSN == "" {
		errs = append(errs, errors.New("LOCKLENS_STORE_DSN is required (connection string of LockLens' own monitoring database)"))
	}
	if c.MasterKey == "" {
		errs = append(errs, errors.New("LOCKLENS_MASTER_KEY is required (32 random bytes, base64; generate with: openssl rand -base64 32)"))
	}
	if c.AdminPassword != "" && c.AdminPasswordHash != "" {
		errs = append(errs, errors.New("set either LOCKLENS_ADMIN_PASSWORD or LOCKLENS_ADMIN_PASSWORD_HASH, not both"))
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		errs = append(errs, errors.New("LOCKLENS_LOG_FORMAT must be text or json"))
	}
	return c, errors.Join(errs...)
}

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62})(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}))*$`)

func def(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

// Getenv is the production environment lookup.
var Getenv = os.Getenv

// StoreTimescale configures TimescaleDB in the monitoring store.
type StoreTimescale struct {
	// Mode is auto (use it when the server has it), on (require it), or off.
	Mode string
	// CompressAfter is the age at which chunks are compressed; 0 disables
	// compression.
	CompressAfter time.Duration
}

// LoadStoreTimescale reads LOCKLENS_STORE_TIMESCALE and
// LOCKLENS_STORE_COMPRESS_AFTER.
func LoadStoreTimescale(getenv func(string) string) (StoreTimescale, error) {
	st := StoreTimescale{Mode: strings.ToLower(strings.TrimSpace(getenv("LOCKLENS_STORE_TIMESCALE"))), CompressAfter: 24 * time.Hour}
	if st.Mode == "" {
		st.Mode = "auto"
	}
	var errs []error
	if st.Mode != "auto" && st.Mode != "on" && st.Mode != "off" {
		errs = append(errs, errors.New("LOCKLENS_STORE_TIMESCALE must be auto, on, or off"))
	}
	if v := strings.TrimSpace(getenv("LOCKLENS_STORE_COMPRESS_AFTER")); v != "" {
		d, err := time.ParseDuration(v)
		switch {
		case v == "0" || (err == nil && d == 0):
			st.CompressAfter = 0
		case err != nil || d < time.Hour:
			errs = append(errs, errors.New("LOCKLENS_STORE_COMPRESS_AFTER must be 0 (no compression) or a duration of at least 1h, for example 24h"))
		default:
			st.CompressAfter = d
		}
	}
	return st, errors.Join(errs...)
}
