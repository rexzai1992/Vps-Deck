// Package nginxscanner reads existing Nginx config files on the host filesystem
// in a strictly read-only manner. It never modifies, reloads, or overwrites any
// Nginx configuration. Its sole purpose is to surface what is already on disk.
package nginxscanner

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ScannedRoute is one server{} block from an Nginx config file that contains
// a proxy_pass directive. Read-only; never written back to disk.
type ScannedRoute struct {
	Hostname    string     // first server_name value; empty for port-only configs
	ConfigFile  string     // absolute path to the source config file
	Enabled     bool       // a symlink to this file exists in a sites-enabled dir
	TargetHost  string     // parsed from proxy_pass (e.g. "127.0.0.1")
	TargetPort  int        // parsed from proxy_pass (e.g. 8080)
	ProxyPass   string     // raw proxy_pass value
	TargetType  string     // "panel", "ollama", "project", or "unknown"
	SSLEnabled  bool
	CertPath    string
	CertExpiry  *time.Time
	DaysLeft    int
	CertWarning bool // expiry within 14 days
	CertExpired bool
	ListenPorts []int
}

// ScanResult is returned by Scan.
type ScanResult struct {
	Routes    []ScannedRoute
	ScannedAt time.Time
	Errors    []string // non-fatal scan errors (e.g. unreadable directory)
}

// ScanOptions configures which Nginx directories are inspected.
type ScanOptions struct {
	// SitesAvailDirs lists directories whose config files are parsed.
	SitesAvailDirs []string
	// SitesEnabledDirs lists directories whose contents mark configs as enabled
	// (typically via symlink, but direct files are also accepted).
	SitesEnabledDirs []string
	// PanelPort is the local port VPSDeck listens on; used to label
	// proxy_pass targets as "panel".
	PanelPort int
}

// Scan reads nginx config files from the given directories and returns all
// server{} blocks that contain a proxy_pass directive. It never modifies any
// file on disk.
func Scan(opts ScanOptions) ScanResult {
	result := ScanResult{ScannedAt: time.Now()}

	enabled := buildEnabledSet(opts.SitesEnabledDirs)

	// Collect all config files from sites-available directories, deduped.
	seen := map[string]bool{}
	var configFiles []string
	for _, dir := range opts.SitesAvailDirs {
		files, err := listConfigs(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				result.Errors = append(result.Errors, "scan "+dir+": "+err.Error())
			}
			continue
		}
		for _, f := range files {
			if !seen[f] {
				seen[f] = true
				configFiles = append(configFiles, f)
			}
		}
	}
	// Include any enabled symlink targets not already in sites-avail.
	for f := range enabled {
		if !seen[f] {
			seen[f] = true
			configFiles = append(configFiles, f)
		}
	}

	for _, path := range configFiles {
		routes, err := scanFile(path, enabled, opts.PanelPort)
		if err != nil {
			result.Errors = append(result.Errors, path+": "+err.Error())
			continue
		}
		result.Routes = append(result.Routes, routes...)
	}
	return result
}

// buildEnabledSet returns a set of absolute config file paths that are
// symlinked (or directly present) in any of the given sites-enabled directories.
func buildEnabledSet(sitesEnabledDirs []string) map[string]bool {
	set := map[string]bool{}
	for _, dir := range sitesEnabledDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			full := filepath.Join(dir, entry.Name())
			if entry.Type()&os.ModeSymlink != 0 {
				target, err := os.Readlink(full)
				if err != nil {
					continue
				}
				if !filepath.IsAbs(target) {
					target = filepath.Join(dir, target)
				}
				set[filepath.Clean(target)] = true
			} else if !entry.IsDir() {
				set[filepath.Clean(full)] = true
			}
		}
	}
	return set
}

// listConfigs returns absolute paths to all non-directory, non-hidden files in dir.
func listConfigs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	return files, nil
}

// scanFile parses all server{} blocks in path that contain a proxy_pass.
func scanFile(path string, enabled map[string]bool, panelPort int) ([]ScannedRoute, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := removeComments(string(data))
	rawBlocks := extractServerBlocks(content)

	var routes []ScannedRoute
	for _, raw := range rawBlocks {
		block := parseBlock(raw)
		if block.ProxyPass == "" {
			continue // redirect-only blocks (e.g. HTTP→HTTPS redirects)
		}
		targetHost, targetPort := proxyPassParts(block.ProxyPass)

		hostname := ""
		if len(block.ServerNames) > 0 {
			hostname = block.ServerNames[0]
		}

		var certExpiry *time.Time
		if block.CertPath != "" {
			certExpiry, _ = ReadCertExpiry(block.CertPath)
		}

		route := ScannedRoute{
			Hostname:    hostname,
			ConfigFile:  filepath.Clean(path),
			Enabled:     enabled[filepath.Clean(path)],
			TargetHost:  targetHost,
			TargetPort:  targetPort,
			ProxyPass:   block.ProxyPass,
			TargetType:  guessTargetType(targetPort, panelPort),
			SSLEnabled:  block.SSLEnabled,
			CertPath:    block.CertPath,
			CertExpiry:  certExpiry,
			ListenPorts: block.ListenPorts,
		}
		if certExpiry != nil {
			days := int(time.Until(*certExpiry).Hours() / 24)
			route.DaysLeft = days
			route.CertExpired = days < 0
			route.CertWarning = days >= 0 && days < 14
		}
		routes = append(routes, route)
	}
	return routes, nil
}

// ScanSingleFile parses all proxy_pass server blocks from one config file.
// Enabled status is always false (no sites-enabled context). Used by the
// import handler to validate and read data from a specific config path.
func ScanSingleFile(path string, panelPort int) ([]ScannedRoute, error) {
	return scanFile(path, map[string]bool{}, panelPort)
}

func guessTargetType(targetPort, panelPort int) string {
	switch {
	case targetPort == 0:
		return "unknown"
	case targetPort == panelPort:
		return "panel"
	case targetPort == 11434:
		return "ollama"
	default:
		return "project"
	}
}
