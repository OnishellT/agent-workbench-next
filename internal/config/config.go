package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultListen = "127.0.0.1:18888"
	stateName     = "agent-workbench-next"
)

// Config contains only this application's paths and browser authority. It never
// imports paths or credentials from an older Workbench installation.
type Config struct {
	StateDir       string
	Listen         string
	PublicURL      string
	SIMURL         string
	AuthToken      string
	Version        string
	TrustedProxies []netip.Prefix
}

// Load resolves command-line configuration and creates or reads a private
// access token in the selected new-state directory. It deliberately ignores
// the legacy WORKBENCH_STATE variable and never derives paths from cwd.
func Load(args []string) (Config, error) {
	flags := flag.NewFlagSet("workbench", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	stateDir := flags.String("state-dir", "", "private state directory")
	listen := flags.String("listen", defaultListen, "loopback listener address")
	publicURL := flags.String("public-url", "", "public origin")
	// SIM keeps its own origin; the flag overrides the environment.
	simURL := flags.String("sim-url", os.Getenv("WORKBENCH_SIM_URL"), "dedicated SIM app origin (optional)")
	tokenFile := flags.String("token-file", "", "private token file under state-dir")
	var trustedProxyFlags stringList
	flags.Var(&trustedProxyFlags, "trusted-proxy", "explicit trusted reverse-proxy IP or CIDR (repeatable)")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if *stateDir == "" {
		defaultState, err := defaultStateDir()
		if err != nil {
			return Config{}, err
		}
		*stateDir = defaultState
	}

	statePath, err := absolutePath(*stateDir)
	if err != nil {
		return Config{}, fmt.Errorf("state-dir: %w", err)
	}
	if statePath == string(os.PathSeparator) {
		return Config{}, errors.New("state-dir cannot be the filesystem root")
	}
	listenAddr, err := normalizeListen(*listen)
	if err != nil {
		return Config{}, err
	}
	origin := *publicURL
	if origin == "" {
		origin = "http://" + listenAddr
	}
	origin, err = NormalizePublicURL(origin)
	if err != nil {
		return Config{}, err
	}
	if err := validatePublicListen(origin, listenAddr); err != nil {
		return Config{}, err
	}
	simOrigin, err := normalizeSIMURL(*simURL, origin)
	if err != nil {
		return Config{}, err
	}
	proxies := make([]netip.Prefix, 0, len(trustedProxyFlags))
	for _, value := range trustedProxyFlags {
		prefix, err := parseTrustedProxy(value)
		if err != nil {
			return Config{}, err
		}
		proxies = append(proxies, prefix)
	}

	secretPath := filepath.Join(statePath, "access.token")
	if *tokenFile != "" {
		secretPath, err = absolutePath(*tokenFile)
		if err != nil {
			return Config{}, fmt.Errorf("token-file: %w", err)
		}
		if !within(statePath, secretPath) || secretPath == statePath {
			return Config{}, errors.New("token-file must be inside state-dir")
		}
	}
	if err := ensurePrivateDir(statePath); err != nil {
		return Config{}, fmt.Errorf("state-dir: %w", err)
	}
	entries, err := os.ReadDir(statePath)
	if err != nil {
		return Config{}, fmt.Errorf("state-dir: %w", err)
	}
	if err := ensurePrivateDir(filepath.Dir(secretPath)); err != nil {
		return Config{}, fmt.Errorf("token-file directory: %w", err)
	}
	token, err := loadOrCreateToken(secretPath, len(entries) == 0)
	if err != nil {
		return Config{}, fmt.Errorf("token-file: %w", err)
	}
	// app.New validates the assembled Config; Load only normalizes its inputs.
	return Config{StateDir: statePath, Listen: listenAddr, PublicURL: origin, SIMURL: simOrigin, AuthToken: token, TrustedProxies: proxies}, nil
}

// Validate checks configurations supplied directly by callers as well as those
// resolved by Load. StateDir must be absolute and must not be cwd-relative.
func (c Config) Validate() error {
	state, err := absolutePath(c.StateDir)
	if err != nil || state != c.StateDir || state == string(os.PathSeparator) {
		return errors.New("state-dir must be an absolute, cleaned non-root path")
	}
	listen, err := normalizeListen(c.Listen)
	if err != nil || listen != c.Listen {
		return errors.New("listen must be a canonical loopback host:port")
	}
	origin, err := NormalizePublicURL(c.PublicURL)
	if err != nil || origin != c.PublicURL {
		return errors.New("public-url must be a canonical HTTP(S) origin")
	}
	if err := validatePublicListen(origin, listen); err != nil {
		return err
	}
	sim, err := normalizeSIMURL(c.SIMURL, origin)
	if err != nil || sim != c.SIMURL {
		return errors.New("sim-url must be an HTTP(S) origin different from public-url")
	}
	if !validToken(c.AuthToken) {
		return errors.New("auth token must contain at least 32 URL-safe bytes")
	}
	for _, prefix := range c.TrustedProxies {
		if !loopbackOnlyPrefix(prefix) {
			return errors.New("trusted-proxy must be a canonical IP or CIDR")
		}
	}
	return nil
}

// NormalizePublicURL accepts an HTTPS public origin, or HTTP only for a
// loopback origin. Paths, credentials, queries, fragments and ambiguous hosts
// are rejected instead of being silently reinterpreted.
func NormalizePublicURL(raw string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "\\\r\n\t ") || strings.ContainsRune(raw, 0) || strings.Contains(raw, "?") || strings.Contains(raw, "#") {
		return "", errors.New("public-url must be an HTTP(S) origin without whitespace, query or fragment")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Host == "" || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("public-url must be an origin without credentials, path, query or fragment")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("public-url scheme must be http or https")
	}
	hostname := u.Hostname()
	if hostname == "" || strings.ContainsAny(hostname, "%\\") || strings.HasSuffix(hostname, ".") || strings.HasSuffix(u.Host, ":") {
		return "", errors.New("public-url contains an invalid host")
	}
	port := u.Port()
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", errors.New("public-url contains an invalid port")
		}
	}
	host, loopback, err := normalizeHostname(hostname)
	if err != nil {
		return "", err
	}
	if scheme == "http" && !loopback {
		return "", errors.New("public-url may use HTTP only on loopback")
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	authority := host
	if port != "" {
		authority = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	return scheme + "://" + authority, nil
}

// normalizeSIMURL validates the optional dedicated SIM origin. Empty means SIM
// is not configured. The Workbench origin itself is refused: embedding it there
// would nest the shell inside itself.
func normalizeSIMURL(raw, publicURL string) (string, error) {
	if raw == "" {
		return "", nil
	}
	origin, err := NormalizePublicURL(raw)
	if err != nil {
		return "", fmt.Errorf("sim-url: %w", err)
	}
	if origin == publicURL {
		return "", errors.New("sim-url must differ from public-url")
	}
	if strings.HasPrefix(publicURL, "https://") {
		// Both origins are canonical here; ports do not isolate host-only cookies.
		parent, _ := url.Parse(publicURL)
		sim, _ := url.Parse(origin)
		if sim.Scheme != "https" || sim.Hostname() == parent.Hostname() {
			return "", errors.New("sim-url for HTTPS Workbench must use HTTPS and a different hostname (cookies are not isolated by port)")
		}
	}
	return origin, nil
}

func normalizeHostname(hostname string) (string, bool, error) {
	if address, err := netip.ParseAddr(hostname); err == nil {
		if address.Zone() != "" {
			return "", false, errors.New("public-url host must not contain an IPv6 zone")
		}
		value := address.String()
		if address.Is6() {
			value = "[" + value + "]"
		}
		return value, address.IsLoopback(), nil
	}
	name := strings.ToLower(hostname)
	if len(name) > 253 {
		return "", false, errors.New("public-url contains an invalid host")
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false, errors.New("public-url contains an invalid host")
		}
		for _, char := range label {
			if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
				return "", false, errors.New("public-url host must use DNS ASCII labels")
			}
		}
	}
	return name, name == "localhost", nil
}

func validatePublicListen(publicURL, listen string) error {
	public, err := url.Parse(publicURL)
	if err != nil {
		return errors.New("public-url is invalid")
	}
	if public.Scheme != "http" {
		return nil
	}
	_, listenPort, err := net.SplitHostPort(listen)
	if err != nil || public.Port() == "" || public.Port() != listenPort {
		return errors.New("an HTTP public-url must use the listen port")
	}
	return nil
}

func normalizeListen(raw string) (string, error) {
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", errors.New("listen must be a loopback host:port")
	}
	address, err := netip.ParseAddr(host)
	if err != nil || address.Zone() != "" || !address.IsLoopback() {
		return "", errors.New("listen must bind to a literal loopback address")
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1024 || value > 65535 {
		return "", errors.New("listen port must be between 1024 and 65535")
	}
	return net.JoinHostPort(address.String(), strconv.Itoa(value)), nil
}

func defaultStateDir() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		absolute, err := absolutePath(xdg)
		if err != nil {
			return "", fmt.Errorf("XDG_STATE_HOME must be absolute: %w", err)
		}
		return filepath.Join(absolute, stateName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("set --state-dir or XDG_STATE_HOME; no home directory is available")
	}
	absolute, err := absolutePath(home)
	if err != nil {
		return "", errors.New("home directory must be absolute")
	}
	return filepath.Join(absolute, ".local", "state", stateName), nil
}

func absolutePath(value string) (string, error) {
	if value == "" || !filepath.IsAbs(value) {
		return "", errors.New("path must be absolute")
	}
	return filepath.Clean(value), nil
}

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("must be a real directory, not a symlink")
	}
	if info.Mode().Perm() != 0700 {
		if err := os.Chmod(path, 0700); err != nil {
			return fmt.Errorf("set directory permissions: %w", err)
		}
	}
	return nil
}

func loadOrCreateToken(path string, allowCreate bool) (string, error) {
	if token, err := readToken(path); err == nil {
		return token, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if !allowCreate {
		return "", errors.New("refusing to generate a token in existing state without a token file")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return readToken(path)
	}
	if err != nil {
		return "", err
	}
	if _, err = io.WriteString(file, token+"\n"); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return token, nil
}

func readToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("must be a regular file, not a symlink")
	}
	if info.Mode().Perm()&0077 != 0 {
		return "", errors.New("permissions must not grant group or other access")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSuffix(string(data), "\n")
	if !validToken(token) {
		return "", errors.New("contains an invalid token")
	}
	return token, nil
}

func validToken(token string) bool {
	if len(token) < 32 {
		return false
	}
	for _, char := range token {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return false
		}
	}
	return true
}

func loopbackOnlyPrefix(prefix netip.Prefix) bool {
	if !prefix.IsValid() || prefix != prefix.Masked() {
		return false
	}
	address := prefix.Addr()
	if address.Zone() != "" || address.Is4In6() || !address.IsLoopback() {
		return false
	}
	if address.Is4() {
		return prefix.Bits() >= 8
	}
	return address.Is6() && prefix.Bits() == 128
}

func parseTrustedProxy(raw string) (netip.Prefix, error) {

	var prefix netip.Prefix
	if address, err := netip.ParseAddr(raw); err == nil && address.Zone() == "" {
		bits := 128
		if address.Is4() {
			bits = 32
		}
		prefix = netip.PrefixFrom(address, bits)
	} else {
		parsed, err := netip.ParsePrefix(raw)
		if err != nil || parsed.Addr().Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("invalid --trusted-proxy %q", raw)
		}
		prefix = parsed.Masked()
	}
	if !loopbackOnlyPrefix(prefix) {
		return netip.Prefix{}, errors.New("--trusted-proxy must name only loopback addresses")
	}
	return prefix, nil
}

func within(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}
