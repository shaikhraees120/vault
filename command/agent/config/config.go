package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	ctconfig "github.com/hashicorp/consul-template/config"
	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/go-secure-stdlib/parseutil"
	"github.com/hashicorp/hcl"
	"github.com/hashicorp/hcl/hcl/ast"
	"github.com/hashicorp/vault/helper/namespace"
	"github.com/hashicorp/vault/internalshared/configutil"
	"github.com/mitchellh/mapstructure"
)

// Config is the configuration for Vault Agent.
type Config struct {
	*configutil.SharedConfig `hcl:"-"`

	AutoAuth                    *AutoAuth                  `hcl:"auto_auth"`
	ExitAfterAuth               bool                       `hcl:"exit_after_auth"`
	Cache                       *Cache                     `hcl:"cache"`
	APIProxy                    *APIProxy                  `hcl:"api_proxy""`
	Vault                       *Vault                     `hcl:"vault"`
	TemplateConfig              *TemplateConfig            `hcl:"template_config"`
	Templates                   []*ctconfig.TemplateConfig `hcl:"templates"`
	DisableIdleConns            []string                   `hcl:"disable_idle_connections"`
	DisableIdleConnsAPIProxy    bool                       `hcl:"-"`
	DisableIdleConnsTemplating  bool                       `hcl:"-"`
	DisableIdleConnsAutoAuth    bool                       `hcl:"-"`
	DisableKeepAlives           []string                   `hcl:"disable_keep_alives"`
	DisableKeepAlivesAPIProxy   bool                       `hcl:"-"`
	DisableKeepAlivesTemplating bool                       `hcl:"-"`
	DisableKeepAlivesAutoAuth   bool                       `hcl:"-"`
}

const (
	DisableIdleConnsEnv  = "VAULT_AGENT_DISABLE_IDLE_CONNECTIONS"
	DisableKeepAlivesEnv = "VAULT_AGENT_DISABLE_KEEP_ALIVES"
)

func (c *Config) Prune() {
	for _, l := range c.Listeners {
		l.RawConfig = nil
		l.Profiling.UnusedKeys = nil
		l.Telemetry.UnusedKeys = nil
		l.CustomResponseHeaders = nil
	}
	c.FoundKeys = nil
	c.UnusedKeys = nil
	c.SharedConfig.FoundKeys = nil
	c.SharedConfig.UnusedKeys = nil
	if c.Telemetry != nil {
		c.Telemetry.FoundKeys = nil
		c.Telemetry.UnusedKeys = nil
	}
}

type Retry struct {
	NumRetries int `hcl:"num_retries"`
}

// Vault contains configuration for connecting to Vault servers
type Vault struct {
	Address          string      `hcl:"address"`
	CACert           string      `hcl:"ca_cert"`
	CAPath           string      `hcl:"ca_path"`
	TLSSkipVerify    bool        `hcl:"-"`
	TLSSkipVerifyRaw interface{} `hcl:"tls_skip_verify"`
	ClientCert       string      `hcl:"client_cert"`
	ClientKey        string      `hcl:"client_key"`
	TLSServerName    string      `hcl:"tls_server_name"`
	Retry            *Retry      `hcl:"retry"`
}

// transportDialer is an interface that allows passing a custom dialer function
// to an HTTP client's transport config
type transportDialer interface {
	// Dial is intended to match https://pkg.go.dev/net#Dialer.Dial
	Dial(network, address string) (net.Conn, error)

	// DialContext is intended to match https://pkg.go.dev/net#Dialer.DialContext
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// APIProxy contains any configuration needed for proxy mode
type APIProxy struct {
	UseAutoAuthTokenRaw interface{} `hcl:"use_auto_auth_token"`
	UseAutoAuthToken    bool        `hcl:"-"`
	ForceAutoAuthToken  bool        `hcl:"-"`
	EnforceConsistency  string      `hcl:"enforce_consistency"`
	WhenInconsistent    string      `hcl:"when_inconsistent"`
}

// Cache contains any configuration needed for Cache mode
type Cache struct {
	UseAutoAuthTokenRaw interface{}     `hcl:"use_auto_auth_token"`
	UseAutoAuthToken    bool            `hcl:"-"`
	ForceAutoAuthToken  bool            `hcl:"-"`
	EnforceConsistency  string          `hcl:"enforce_consistency"`
	WhenInconsistent    string          `hcl:"when_inconsistent"`
	Persist             *Persist        `hcl:"persist"`
	InProcDialer        transportDialer `hcl:"-"`
}

// Persist contains configuration needed for persistent caching
type Persist struct {
	Type                    string
	Path                    string `hcl:"path"`
	KeepAfterImport         bool   `hcl:"keep_after_import"`
	ExitOnErr               bool   `hcl:"exit_on_err"`
	ServiceAccountTokenFile string `hcl:"service_account_token_file"`
}

// AutoAuth is the configured authentication method and sinks
type AutoAuth struct {
	Method *Method `hcl:"-"`
	Sinks  []*Sink `hcl:"sinks"`

	// NOTE: This is unsupported outside of testing and may disappear at any
	// time.
	EnableReauthOnNewCredentials bool `hcl:"enable_reauth_on_new_credentials"`
}

// Method represents the configuration for the authentication backend
type Method struct {
	Type          string
	MountPath     string        `hcl:"mount_path"`
	WrapTTLRaw    interface{}   `hcl:"wrap_ttl"`
	WrapTTL       time.Duration `hcl:"-"`
	MinBackoffRaw interface{}   `hcl:"min_backoff"`
	MinBackoff    time.Duration `hcl:"-"`
	MaxBackoffRaw interface{}   `hcl:"max_backoff"`
	MaxBackoff    time.Duration `hcl:"-"`
	Namespace     string        `hcl:"namespace"`
	ExitOnError   bool          `hcl:"exit_on_err"`
	Config        map[string]interface{}
}

// Sink defines a location to write the authenticated token
type Sink struct {
	Type       string
	WrapTTLRaw interface{}   `hcl:"wrap_ttl"`
	WrapTTL    time.Duration `hcl:"-"`
	DHType     string        `hcl:"dh_type"`
	DeriveKey  bool          `hcl:"derive_key"`
	DHPath     string        `hcl:"dh_path"`
	AAD        string        `hcl:"aad"`
	AADEnvVar  string        `hcl:"aad_env_var"`
	Config     map[string]interface{}
}

// TemplateConfig defines global behaviors around template
type TemplateConfig struct {
	ExitOnRetryFailure       bool          `hcl:"exit_on_retry_failure"`
	StaticSecretRenderIntRaw interface{}   `hcl:"static_secret_render_interval"`
	StaticSecretRenderInt    time.Duration `hcl:"-"`
}

func NewConfig() *Config {
	return &Config{
		SharedConfig: new(configutil.SharedConfig),
	}
}

// Merge merges two Agent configurations.
func (c *Config) Merge(c2 *Config) *Config {
	if c2 == nil {
		return c
	}

	result := NewConfig()

	result.SharedConfig = c.SharedConfig
	if c2.SharedConfig != nil {
		result.SharedConfig = c.SharedConfig.Merge(c2.SharedConfig)
	}

	result.AutoAuth = c.AutoAuth
	if c2.AutoAuth != nil {
		result.AutoAuth = c2.AutoAuth
	}

	result.Cache = c.Cache
	if c2.Cache != nil {
		result.Cache = c2.Cache
	}

	result.APIProxy = c.APIProxy
	if c2.APIProxy != nil {
		result.APIProxy = c2.APIProxy
	}

	result.DisableMlock = c.DisableMlock
	if c2.DisableMlock {
		result.DisableMlock = c2.DisableMlock
	}

	// For these, ignore the non-specific one and overwrite them all
	result.DisableIdleConnsAutoAuth = c.DisableIdleConnsAutoAuth
	if c2.DisableIdleConnsAutoAuth {
		result.DisableIdleConnsAutoAuth = c2.DisableIdleConnsAutoAuth
	}

	result.DisableIdleConnsAPIProxy = c.DisableIdleConnsAPIProxy
	if c2.DisableIdleConnsAPIProxy {
		result.DisableIdleConnsAPIProxy = c2.DisableIdleConnsAPIProxy
	}

	result.DisableIdleConnsTemplating = c.DisableIdleConnsTemplating
	if c2.DisableIdleConnsTemplating {
		result.DisableIdleConnsTemplating = c2.DisableIdleConnsTemplating
	}

	result.DisableKeepAlivesAutoAuth = c.DisableKeepAlivesAutoAuth
	if c2.DisableKeepAlivesAutoAuth {
		result.DisableKeepAlivesAutoAuth = c2.DisableKeepAlivesAutoAuth
	}

	result.DisableKeepAlivesAPIProxy = c.DisableKeepAlivesAPIProxy
	if c2.DisableKeepAlivesAPIProxy {
		result.DisableKeepAlivesAPIProxy = c2.DisableKeepAlivesAPIProxy
	}

	result.DisableKeepAlivesTemplating = c.DisableKeepAlivesTemplating
	if c2.DisableKeepAlivesTemplating {
		result.DisableKeepAlivesTemplating = c2.DisableKeepAlivesTemplating
	}

	result.TemplateConfig = c.TemplateConfig
	if c2.TemplateConfig != nil {
		result.TemplateConfig = c2.TemplateConfig
	}

	for _, l := range c.Templates {
		result.Templates = append(result.Templates, l)
	}
	for _, l := range c2.Templates {
		result.Templates = append(result.Templates, l)
	}

	result.ExitAfterAuth = c.ExitAfterAuth
	if c2.ExitAfterAuth {
		result.ExitAfterAuth = c2.ExitAfterAuth
	}

	result.Vault = c.Vault
	if c2.Vault != nil {
		result.Vault = c2.Vault
	}

	result.PidFile = c.PidFile
	if c2.PidFile != "" {
		result.PidFile = c2.PidFile
	}

	return result
}

// ValidateConfig validates an Agent configuration after it has been fully merged together, to
// ensure that required combinations of configs are there
func (c *Config) ValidateConfig() error {
	if c.APIProxy != nil && c.Cache != nil {
		if c.Cache.UseAutoAuthTokenRaw != nil {
			if c.APIProxy.UseAutoAuthTokenRaw != nil {
				return fmt.Errorf("use_auto_auth_token defined in both api_proxy and cache config. Please remove this configuration from the cache block")
			} else {
				c.APIProxy.ForceAutoAuthToken = c.Cache.ForceAutoAuthToken
			}
		}
	}

	if c.Cache != nil {
		if len(c.Listeners) < 1 && len(c.Templates) < 1 {
			return fmt.Errorf("enabling the cache requires at least 1 template or 1 listener to be defined")
		}

		if c.Cache.UseAutoAuthToken {
			if c.AutoAuth == nil {
				return fmt.Errorf("cache.use_auto_auth_token is true but auto_auth not configured")
			}
			if c.AutoAuth != nil && c.AutoAuth.Method != nil && c.AutoAuth.Method.WrapTTL > 0 {
				return fmt.Errorf("cache.use_auto_auth_token is true and auto_auth uses wrapping")
			}
		}
	}

	if c.APIProxy != nil {
		if len(c.Listeners) < 1 {
			return fmt.Errorf("configuring the api_proxy requires at least 1 listener to be defined")
		}

		if c.APIProxy.UseAutoAuthToken {
			if c.AutoAuth == nil {
				return fmt.Errorf("api_proxy.use_auto_auth_token is true but auto_auth not configured")
			}
			if c.AutoAuth != nil && c.AutoAuth.Method != nil && c.AutoAuth.Method.WrapTTL > 0 {
				return fmt.Errorf("api_proxy.use_auto_auth_token is true and auto_auth uses wrapping")
			}
		}
	}

	if c.AutoAuth != nil {
		if len(c.AutoAuth.Sinks) == 0 &&
			(c.APIProxy == nil || !c.APIProxy.UseAutoAuthToken) &&
			len(c.Templates) == 0 {
			return fmt.Errorf("auto_auth requires at least one sink or at least one template or api_proxy.use_auto_auth_token=true")
		}
	}

	if c.AutoAuth == nil && c.Cache == nil && len(c.Listeners) == 0 {
		return fmt.Errorf("no auto_auth, cache, or listener block found in config")
	}

	return nil
}

// LoadConfig loads the configuration at the given path, regardless if
// it's a file or directory.
func LoadConfig(path string) (*Config, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if fi.IsDir() {
		return LoadConfigDir(path)
	}
	return LoadConfigFile(path)
}

// LoadConfigDir loads the configuration at the given path if it's a directory
func LoadConfigDir(dir string) (*Config, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("configuration path must be a directory: %q", dir)
	}

	var files []string
	err = nil
	for err != io.EOF {
		var fis []os.FileInfo
		fis, err = f.Readdir(128)
		if err != nil && err != io.EOF {
			return nil, err
		}

		for _, fi := range fis {
			// Ignore directories
			if fi.IsDir() {
				continue
			}

			// Only care about files that are valid to load.
			name := fi.Name()
			skip := true
			if strings.HasSuffix(name, ".hcl") {
				skip = false
			} else if strings.HasSuffix(name, ".json") {
				skip = false
			}
			if skip || isTemporaryFile(name) {
				continue
			}

			path := filepath.Join(dir, name)
			files = append(files, path)
		}
	}

	result := NewConfig()
	for _, f := range files {
		config, err := LoadConfigFile(f)
		if err != nil {
			return nil, fmt.Errorf("error loading %q: %w", f, err)
		}

		if result == nil {
			result = config
		} else {
			result = result.Merge(config)
		}
	}

	return result, nil
}

// isTemporaryFile returns true or false depending on whether the
// provided file name is a temporary file for the following editors:
// emacs or vim.
func isTemporaryFile(name string) bool {
	return strings.HasSuffix(name, "~") || // vim
		strings.HasPrefix(name, ".#") || // emacs
		(strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#")) // emacs
}

// LoadConfigFile loads the configuration at the given path if it's a file
func LoadConfigFile(path string) (*Config, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	if fi.IsDir() {
		return nil, fmt.Errorf("location is a directory, not a file")
	}

	// Read the file
	d, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Parse!
	obj, err := hcl.Parse(string(d))
	if err != nil {
		return nil, err
	}

	// Attribute
	ast.Walk(obj, func(n ast.Node) (ast.Node, bool) {
		if k, ok := n.(*ast.ObjectKey); ok {
			k.Token.Pos.Filename = path
		}
		return n, true
	})

	// Start building the result
	result := NewConfig()
	if err := hcl.DecodeObject(result, obj); err != nil {
		return nil, err
	}

	sharedConfig, err := configutil.ParseConfig(string(d))
	if err != nil {
		return nil, err
	}

	// Pruning custom headers for Agent for now
	for _, ln := range sharedConfig.Listeners {
		ln.CustomResponseHeaders = nil
	}

	result.SharedConfig = sharedConfig

	list, ok := obj.Node.(*ast.ObjectList)
	if !ok {
		return nil, fmt.Errorf("error parsing: file doesn't contain a root object")
	}

	if err := parseAutoAuth(result, list); err != nil {
		return nil, fmt.Errorf("error parsing 'auto_auth': %w", err)
	}

	if err := parseCache(result, list); err != nil {
		return nil, fmt.Errorf("error parsing 'cache':%w", err)
	}

	if err := parseAPIProxy(result, list); err != nil {
		return nil, fmt.Errorf("error parsing 'api_proxy':%w", err)
	}

	if err := parseTemplateConfig(result, list); err != nil {
		return nil, fmt.Errorf("error parsing 'template_config': %w", err)
	}

	if err := parseTemplates(result, list); err != nil {
		return nil, fmt.Errorf("error parsing 'template': %w", err)
	}

	if result.Cache != nil && result.APIProxy == nil {
		result.APIProxy = &APIProxy{
			UseAutoAuthToken:   result.Cache.UseAutoAuthToken,
			ForceAutoAuthToken: result.Cache.ForceAutoAuthToken,
		}
	}

	err = parseVault(result, list)
	if err != nil {
		return nil, fmt.Errorf("error parsing 'vault':%w", err)
	}

	if result.Vault != nil {
		// Set defaults
		if result.Vault.Retry == nil {
			result.Vault.Retry = &Retry{}
		}
		switch result.Vault.Retry.NumRetries {
		case 0:
			result.Vault.Retry.NumRetries = ctconfig.DefaultRetryAttempts
		case -1:
			result.Vault.Retry.NumRetries = 0
		}
	}

	if disableIdleConnsEnv := os.Getenv(DisableIdleConnsEnv); disableIdleConnsEnv != "" {
		result.DisableIdleConns, err = parseutil.ParseCommaStringSlice(strings.ToLower(disableIdleConnsEnv))
		if err != nil {
			return nil, fmt.Errorf("error parsing environment variable %s: %v", DisableIdleConnsEnv, err)
		}
	}

	for _, subsystem := range result.DisableIdleConns {
		switch subsystem {
		case "auto-auth":
			result.DisableIdleConnsAutoAuth = true
		case "caching", "proxying":
			result.DisableIdleConnsAPIProxy = true
		case "templating":
			result.DisableIdleConnsTemplating = true
		case "":
			continue
		default:
			return nil, fmt.Errorf("unknown disable_idle_connections value: %s", subsystem)
		}
	}

	if disableKeepAlivesEnv := os.Getenv(DisableKeepAlivesEnv); disableKeepAlivesEnv != "" {
		result.DisableKeepAlives, err = parseutil.ParseCommaStringSlice(strings.ToLower(disableKeepAlivesEnv))
		if err != nil {
			return nil, fmt.Errorf("error parsing environment variable %s: %v", DisableKeepAlivesEnv, err)
		}
	}

	for _, subsystem := range result.DisableKeepAlives {
		switch subsystem {
		case "auto-auth":
			result.DisableKeepAlivesAutoAuth = true
		case "caching", "proxying":
			result.DisableKeepAlivesAPIProxy = true
		case "templating":
			result.DisableKeepAlivesTemplating = true
		case "":
			continue
		default:
			return nil, fmt.Errorf("unknown disable_keep_alives value: %s", subsystem)
		}
	}

	return result, nil
}

func parseVault(result *Config, list *ast.ObjectList) error {
	name := "vault"

	vaultList := list.Filter(name)
	if len(vaultList.Items) == 0 {
		return nil
	}

	if len(vaultList.Items) > 1 {
		return fmt.Errorf("one and only one %q block is required", name)
	}

	item := vaultList.Items[0]

	var v Vault
	err := hcl.DecodeObject(&v, item.Val)
	if err != nil {
		return err
	}

	if v.TLSSkipVerifyRaw != nil {
		v.TLSSkipVerify, err = parseutil.ParseBool(v.TLSSkipVerifyRaw)
		if err != nil {
			return err
		}
	}

	result.Vault = &v

	subs, ok := item.Val.(*ast.ObjectType)
	if !ok {
		return fmt.Errorf("could not parse %q as an object", name)
	}

	if err := parseRetry(result, subs.List); err != nil {
		return fmt.Errorf("error parsing 'retry': %w", err)
	}

	return nil
}

func parseRetry(result *Config, list *ast.ObjectList) error {
	name := "retry"

	retryList := list.Filter(name)
	if len(retryList.Items) == 0 {
		return nil
	}

	if len(retryList.Items) > 1 {
		return fmt.Errorf("one and only one %q block is required", name)
	}

	item := retryList.Items[0]

	var r Retry
	err := hcl.DecodeObject(&r, item.Val)
	if err != nil {
		return err
	}

	result.Vault.Retry = &r

	return nil
}

func parseAPIProxy(result *Config, list *ast.ObjectList) error {
	name := "api_proxy"

	apiProxyList := list.Filter(name)
	if len(apiProxyList.Items) == 0 {
		return nil
	}

	if len(apiProxyList.Items) > 1 {
		return fmt.Errorf("one and only one %q block is required", name)
	}

	item := apiProxyList.Items[0]

	var apiProxy APIProxy
	err := hcl.DecodeObject(&apiProxy, item.Val)
	if err != nil {
		return err
	}

	if apiProxy.UseAutoAuthTokenRaw != nil {
		apiProxy.UseAutoAuthToken, err = parseutil.ParseBool(apiProxy.UseAutoAuthTokenRaw)
		if err != nil {
			// Could be a value of "force" instead of "true"/"false"
			switch apiProxy.UseAutoAuthTokenRaw.(type) {
			case string:
				v := apiProxy.UseAutoAuthTokenRaw.(string)

				if !strings.EqualFold(v, "force") {
					return fmt.Errorf("value of 'use_auto_auth_token' can be either true/false/force, %q is an invalid option", apiProxy.UseAutoAuthTokenRaw)
				}
				apiProxy.UseAutoAuthToken = true
				apiProxy.ForceAutoAuthToken = true

			default:
				return err
			}
		}
	}
	result.APIProxy = &apiProxy

	return nil
}

func parseCache(result *Config, list *ast.ObjectList) error {
	name := "cache"

	cacheList := list.Filter(name)
	if len(cacheList.Items) == 0 {
		return nil
	}

	if len(cacheList.Items) > 1 {
		return fmt.Errorf("one and only one %q block is required", name)
	}

	item := cacheList.Items[0]

	var c Cache
	err := hcl.DecodeObject(&c, item.Val)
	if err != nil {
		return err
	}

	if c.UseAutoAuthTokenRaw != nil {
		c.UseAutoAuthToken, err = parseutil.ParseBool(c.UseAutoAuthTokenRaw)
		if err != nil {
			// Could be a value of "force" instead of "true"/"false"
			switch c.UseAutoAuthTokenRaw.(type) {
			case string:
				v := c.UseAutoAuthTokenRaw.(string)

				if !strings.EqualFold(v, "force") {
					return fmt.Errorf("value of 'use_auto_auth_token' can be either true/false/force, %q is an invalid option", c.UseAutoAuthTokenRaw)
				}
				c.UseAutoAuthToken = true
				c.ForceAutoAuthToken = true

			default:
				return err
			}
		}
	}
	result.Cache = &c

	subs, ok := item.Val.(*ast.ObjectType)
	if !ok {
		return fmt.Errorf("could not parse %q as an object", name)
	}
	subList := subs.List
	if err := parsePersist(result, subList); err != nil {
		return fmt.Errorf("error parsing persist: %w", err)
	}

	return nil
}

func parsePersist(result *Config, list *ast.ObjectList) error {
	name := "persist"

	persistList := list.Filter(name)
	if len(persistList.Items) == 0 {
		return nil
	}

	if len(persistList.Items) > 1 {
		return fmt.Errorf("only one %q block is required", name)
	}

	item := persistList.Items[0]

	var p Persist
	err := hcl.DecodeObject(&p, item.Val)
	if err != nil {
		return err
	}

	if p.Type == "" {
		if len(item.Keys) == 1 {
			p.Type = strings.ToLower(item.Keys[0].Token.Value().(string))
		}
		if p.Type == "" {
			return errors.New("persist type must be specified")
		}
	}

	result.Cache.Persist = &p

	return nil
}

func parseAutoAuth(result *Config, list *ast.ObjectList) error {
	name := "auto_auth"

	autoAuthList := list.Filter(name)
	if len(autoAuthList.Items) == 0 {
		return nil
	}
	if len(autoAuthList.Items) > 1 {
		return fmt.Errorf("at most one %q block is allowed", name)
	}

	// Get our item
	item := autoAuthList.Items[0]

	var a AutoAuth
	if err := hcl.DecodeObject(&a, item.Val); err != nil {
		return err
	}

	result.AutoAuth = &a

	subs, ok := item.Val.(*ast.ObjectType)
	if !ok {
		return fmt.Errorf("could not parse %q as an object", name)
	}
	subList := subs.List

	if err := parseMethod(result, subList); err != nil {
		return fmt.Errorf("error parsing 'method': %w", err)
	}
	if a.Method == nil {
		return fmt.Errorf("no 'method' block found")
	}

	if err := parseSinks(result, subList); err != nil {
		return fmt.Errorf("error parsing 'sink' stanzas: %w", err)
	}

	if result.AutoAuth.Method.WrapTTL > 0 {
		if len(result.AutoAuth.Sinks) != 1 {
			return fmt.Errorf("error parsing auto_auth: wrapping enabled on auth method and 0 or many sinks defined")
		}

		if result.AutoAuth.Sinks[0].WrapTTL > 0 {
			return fmt.Errorf("error parsing auto_auth: wrapping enabled both on auth method and sink")
		}
	}

	if result.AutoAuth.Method.MaxBackoffRaw != nil {
		var err error
		if result.AutoAuth.Method.MaxBackoff, err = parseutil.ParseDurationSecond(result.AutoAuth.Method.MaxBackoffRaw); err != nil {
			return err
		}
		result.AutoAuth.Method.MaxBackoffRaw = nil
	}

	if result.AutoAuth.Method.MinBackoffRaw != nil {
		var err error
		if result.AutoAuth.Method.MinBackoff, err = parseutil.ParseDurationSecond(result.AutoAuth.Method.MinBackoffRaw); err != nil {
			return err
		}
		result.AutoAuth.Method.MinBackoffRaw = nil
	}

	return nil
}

func parseMethod(result *Config, list *ast.ObjectList) error {
	name := "method"

	methodList := list.Filter(name)
	if len(methodList.Items) != 1 {
		return fmt.Errorf("one and only one %q block is required", name)
	}

	// Get our item
	item := methodList.Items[0]

	var m Method
	if err := hcl.DecodeObject(&m, item.Val); err != nil {
		return err
	}

	if m.Type == "" {
		if len(item.Keys) == 1 {
			m.Type = strings.ToLower(item.Keys[0].Token.Value().(string))
		}
		if m.Type == "" {
			return errors.New("method type must be specified")
		}
	}

	// Default to Vault's default
	if m.MountPath == "" {
		m.MountPath = fmt.Sprintf("auth/%s", m.Type)
	}
	// Standardize on no trailing slash
	m.MountPath = strings.TrimSuffix(m.MountPath, "/")

	if m.WrapTTLRaw != nil {
		var err error
		if m.WrapTTL, err = parseutil.ParseDurationSecond(m.WrapTTLRaw); err != nil {
			return err
		}
		m.WrapTTLRaw = nil
	}

	// Canonicalize namespace path if provided
	m.Namespace = namespace.Canonicalize(m.Namespace)

	result.AutoAuth.Method = &m
	return nil
}

func parseSinks(result *Config, list *ast.ObjectList) error {
	name := "sink"

	sinkList := list.Filter(name)
	if len(sinkList.Items) < 1 {
		return nil
	}

	var ts []*Sink

	for _, item := range sinkList.Items {
		var s Sink
		if err := hcl.DecodeObject(&s, item.Val); err != nil {
			return err
		}

		if s.Type == "" {
			if len(item.Keys) == 1 {
				s.Type = strings.ToLower(item.Keys[0].Token.Value().(string))
			}
			if s.Type == "" {
				return errors.New("sink type must be specified")
			}
		}

		if s.WrapTTLRaw != nil {
			var err error
			if s.WrapTTL, err = parseutil.ParseDurationSecond(s.WrapTTLRaw); err != nil {
				return multierror.Prefix(err, fmt.Sprintf("sink.%s", s.Type))
			}
			s.WrapTTLRaw = nil
		}

		switch s.DHType {
		case "":
		case "curve25519":
		default:
			return multierror.Prefix(errors.New("invalid value for 'dh_type'"), fmt.Sprintf("sink.%s", s.Type))
		}

		if s.AADEnvVar != "" {
			s.AAD = os.Getenv(s.AADEnvVar)
			s.AADEnvVar = ""
		}

		switch {
		case s.DHPath == "" && s.DHType == "":
			if s.AAD != "" {
				return multierror.Prefix(errors.New("specifying AAD data without 'dh_type' does not make sense"), fmt.Sprintf("sink.%s", s.Type))
			}
			if s.DeriveKey {
				return multierror.Prefix(errors.New("specifying 'derive_key' data without 'dh_type' does not make sense"), fmt.Sprintf("sink.%s", s.Type))
			}
		case s.DHPath != "" && s.DHType != "":
		default:
			return multierror.Prefix(errors.New("'dh_type' and 'dh_path' must be specified together"), fmt.Sprintf("sink.%s", s.Type))
		}

		ts = append(ts, &s)
	}

	result.AutoAuth.Sinks = ts
	return nil
}

func parseTemplateConfig(result *Config, list *ast.ObjectList) error {
	name := "template_config"

	templateConfigList := list.Filter(name)
	if len(templateConfigList.Items) == 0 {
		return nil
	}

	if len(templateConfigList.Items) > 1 {
		return fmt.Errorf("at most one %q block is allowed", name)
	}

	// Get our item
	item := templateConfigList.Items[0]

	var cfg TemplateConfig
	if err := hcl.DecodeObject(&cfg, item.Val); err != nil {
		return err
	}

	result.TemplateConfig = &cfg

	if result.TemplateConfig.StaticSecretRenderIntRaw != nil {
		var err error
		if result.TemplateConfig.StaticSecretRenderInt, err = parseutil.ParseDurationSecond(result.TemplateConfig.StaticSecretRenderIntRaw); err != nil {
			return err
		}
		result.TemplateConfig.StaticSecretRenderIntRaw = nil
	}

	return nil
}

func parseTemplates(result *Config, list *ast.ObjectList) error {
	name := "template"

	templateList := list.Filter(name)
	if len(templateList.Items) < 1 {
		return nil
	}

	var tcs []*ctconfig.TemplateConfig

	for _, item := range templateList.Items {
		var shadow interface{}
		if err := hcl.DecodeObject(&shadow, item.Val); err != nil {
			return fmt.Errorf("error decoding config: %s", err)
		}

		// Convert to a map and flatten the keys we want to flatten
		parsed, ok := shadow.(map[string]interface{})
		if !ok {
			return errors.New("error converting config")
		}

		// flatten the wait or exec fields. The initial "wait" or "exec" value, if given, is a
		// []map[string]interface{}, but we need it to be map[string]interface{}.
		// Consul Template has a method flattenKeys that walks all of parsed and
		// flattens every key. For Vault Agent, we only care about the wait input.
		// Only one wait/exec stanza is supported, however Consul Template does not error
		// with multiple instead it flattens them down, with last value winning.
		// Here we take the last element of the parsed["wait"] or parsed["exec"] slice to keep
		// consistency with Consul Template behavior.
		wait, ok := parsed["wait"].([]map[string]interface{})
		if ok {
			parsed["wait"] = wait[len(wait)-1]
		}

		exec, ok := parsed["exec"].([]map[string]interface{})
		if ok {
			parsed["exec"] = exec[len(exec)-1]
		}

		var tc ctconfig.TemplateConfig

		// Use mapstructure to populate the basic config fields
		var md mapstructure.Metadata
		decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				ctconfig.StringToFileModeFunc(),
				ctconfig.StringToWaitDurationHookFunc(),
				mapstructure.StringToSliceHookFunc(","),
				mapstructure.StringToTimeDurationHookFunc(),
			),
			ErrorUnused: true,
			Metadata:    &md,
			Result:      &tc,
		})
		if err != nil {
			return errors.New("mapstructure decoder creation failed")
		}
		if err := decoder.Decode(parsed); err != nil {
			return err
		}
		tcs = append(tcs, &tc)
	}
	result.Templates = tcs
	return nil
}
