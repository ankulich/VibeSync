// Package config loads layered configuration for VibeSync services.
//
// Layering (lowest precedence first):
//
//  1. Built-in defaults supplied by the caller.
//  2. /etc/vibesync/<service>.yaml and /etc/vibesync/<service>.<env>.yaml
//     (present in containers; absent locally).
//  3. ./config/<service>.<env>.yaml relative to the working directory.
//  4. Environment variables, prefixed VB_, where nested keys map via "_"
//     (e.g. VB_DATABASE_PRIMARY_HOST sets database.primary.host).
//  5. Explicit overrides passed to Loader (CLI flags, tests).
//
// Env wins on top so deployments can override file values without rebuilding.
// Secrets MUST come from env — never from files. This is the 12-factor
// contract; see ADR-0007.
//
// The returned *viper.Viper is typed via Unmarshal into a service-specific
// struct; VibeSync does not read values piecemeal at runtime.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// Env is the deployment flavor. Determines which overlay file is loaded.
type Env string

const (
	// EnvLocal is the laptop development environment.
	EnvLocal Env = "local"
	// EnvDev is the shared development/staging environment.
	EnvDev Env = "dev"
	// EnvProd is the production environment.
	EnvProd Env = "prod"
)

// Options configure the Loader. Service name doubles as the file basename.
type Options struct {
	// Service is the service identifier, e.g. "auth-service". Used as the
	// config file base name and the env-prefix.
	Service string
	// Env selects the overlay file: <service>.<env>.yaml. Defaults to "local".
	Env Env
	// ConfigDirs are additional directories to search for config files, in
	// priority order (earlier = higher precedence among files, but still below
	// env vars).
	ConfigDirs []string
	// Defaults are baked-in fallback values. Lowest precedence of all.
	Defaults map[string]any
	// Overrides are explicit key/value pairs (e.g. from CLI flags). Highest
	// precedence. Keys use the same dotted form as Viper ("db.host").
	Overrides map[string]any
}

// Loader builds a Viper instance per Options. Construct once at process start.
type Loader struct{ opts Options }

// New returns a Loader bound to the given options.
func New(opts Options) *Loader {
	if opts.Env == "" {
		opts.Env = EnvLocal
	}
	if opts.Service == "" {
		opts.Service = "vibesync"
	}
	return &Loader{opts: opts}
}

// Load assembles the Viper instance and returns it ready to Unmarshal.
//
// Missing files are NOT an error — env and defaults may fully specify the
// config, especially in containers. An error indicates malformed YAML or a
// type mismatch during defaults binding.
func (l *Loader) Load() (*viper.Viper, error) {
	v := viper.New()
	v.SetEnvPrefix("VB")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()

	// Defaults: lowest precedence.
	for k, val := range l.opts.Defaults {
		v.SetDefault(k, val)
	}

	// File layering: service.yaml always, then service.<env>.yaml as overlay.
	// Search the built-in dirs and any caller-supplied dirs.
	v.SetConfigName(l.opts.Service)
	v.SetConfigType("yaml")
	for _, dir := range l.searchDirs() {
		v.AddConfigPath(dir)
	}

	// Base file. Missing files are normal (env may fully specify config);
	// any other error is a parse problem worth surfacing.
	if err := v.ReadInConfig(); err != nil {
		if !isNotFound(err) {
			return nil, fmt.Errorf("config: read base: %w", err)
		}
	}

	// Env overlay (merges on top of base).
	overlay := viper.New()
	overlay.SetConfigName(fmt.Sprintf("%s.%s", l.opts.Service, l.opts.Env))
	overlay.SetConfigType("yaml")
	for _, dir := range l.searchDirs() {
		overlay.AddConfigPath(dir)
	}
	if err := overlay.ReadInConfig(); err == nil {
		if err := v.MergeConfigMap(overlay.AllSettings()); err != nil {
			return nil, fmt.Errorf("config: merge %s overlay: %w", l.opts.Env, err)
		}
	} else if !isNotFound(err) {
		return nil, fmt.Errorf("config: read %s overlay: %w", l.opts.Env, err)
	}

	// Explicit overrides: highest precedence.
	for k, val := range l.opts.Overrides {
		v.Set(k, val)
	}

	return v, nil
}

// searchDirs returns the directories to scan for config files, ordered from
// least to most specific among the FILE layer. Environment variables still
// outrank everything here.
func (l *Loader) searchDirs() []string {
	dirs := []string{
		"./config",      // local dev: <repo>/apps/<svc>/config
		"/etc/vibesync", // container conventional path
	}
	dirs = append(dirs, l.opts.ConfigDirs...)
	return dirs
}

// isNotFound reports whether err is Viper's "config file not found". Uses
// errors.As with Viper's exported ConfigFileNotFoundError for robust
// detection; falls back to string matching for wrapped/path errors.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var cfgNotFound viper.ConfigFileNotFoundError
	if errors.As(err, &cfgNotFound) {
		return true
	}
	// Viper may also surface os.PathError when the search dir doesn't exist.
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return true
	}
	// Last-resort string match (covers format variations across Viper versions).
	msg := err.Error()
	return strings.Contains(msg, "Not Found") ||
		strings.Contains(msg, "no such file or directory")
}

// MustLoad is Load for callers that want a panic on failure (CLI entrypoints).
func (l *Loader) MustLoad() *viper.Viper {
	v, err := l.Load()
	if err != nil {
		panic(err)
	}
	return v
}
