package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	releasesAPIURL  = "https://api.github.com/repos/DhilipBinny/claudeorch/releases"
	releasesBaseURL = "https://github.com/DhilipBinny/claudeorch/releases/download"
	httpTimeout     = 60 * time.Second
	httpMaxBody     = 50 << 20 // 50 MiB — plenty for a binary, prevents runaway downloads
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newUpgradeCmd())
	})
}

func newUpgradeCmd() *cobra.Command {
	var checkOnly bool
	var pinned string
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Replace the running binary with the latest release.",
		Long: `Fetches the most recent release from GitHub, verifies its SHA-256,
and atomically replaces this binary.

--check          See if an upgrade is available without installing.
--to VERSION     Install a specific version tag (default: latest release).

The binary is replaced via an atomic rename in the same directory, so the
current process continues to run its old code until it exits. The next
invocation uses the new binary.

Requires write permission on the directory containing the binary. If the
binary lives in a system path like /usr/local/bin, run with sudo.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpgrade(cmd, checkOnly, pinned)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "check for updates without installing")
	cmd.Flags().StringVar(&pinned, "to", "", "upgrade to a specific version tag")
	return cmd
}

func runUpgrade(cmd *cobra.Command, checkOnly bool, pinned string) error {
	out := cmd.OutOrStdout()
	ctx := context.Background()

	target := pinned
	if target == "" {
		latest, err := fetchLatestTag(ctx)
		if err != nil {
			return fmt.Errorf("fetch latest release: %w", err)
		}
		target = latest
	}

	current := Version
	fmt.Fprintf(out, "Current version: %s\n", current)
	fmt.Fprintf(out, "Target version:  %s\n", target)

	if current == target {
		fmt.Fprintln(out, "Already up to date.")
		return nil
	}

	if checkOnly {
		fmt.Fprintf(out, "\nUpgrade available: %s → %s\n", current, target)
		fmt.Fprintln(out, "Run 'claudeorch upgrade' to install.")
		return nil
	}

	assetName := fmt.Sprintf("claudeorch-%s-%s", runtime.GOOS, runtime.GOARCH)
	binURL := fmt.Sprintf("%s/%s/%s", releasesBaseURL, target, assetName)
	sumsURL := fmt.Sprintf("%s/%s/SHA256SUMS", releasesBaseURL, target)

	fmt.Fprintf(out, "\nDownloading %s ... ", assetName)
	binData, err := fetchAndVerify(ctx, binURL, sumsURL, assetName)
	if err != nil {
		fmt.Fprintln(out, "failed")
		return fmt.Errorf("download: %w", err)
	}
	fmt.Fprintf(out, "done (%d bytes, sha256 verified)\n", len(binData))

	if err := replaceRunningBinary(binData); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	fmt.Fprintf(out, "\nUpgraded %s → %s\n", current, target)
	fmt.Fprintln(out, "The next 'claudeorch' invocation will run the new binary.")
	return nil
}

// fetchLatestTag returns the most recent release tag, including pre-releases.
// Uses the /releases list endpoint (not /releases/latest) so pre-release
// candidates like v0.1.0-rc1 are discoverable.
func fetchLatestTag(ctx context.Context) (string, error) {
	body, err := httpGet(ctx, releasesAPIURL)
	if err != nil {
		return "", err
	}
	var releases []struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &releases); err != nil {
		return "", fmt.Errorf("parse releases: %w", err)
	}
	if len(releases) == 0 {
		return "", fmt.Errorf("no releases found")
	}
	return releases[0].TagName, nil
}

// fetchAndVerify downloads the binary and its SHA256SUMS, verifies the hash
// for assetName, and returns the binary bytes.
func fetchAndVerify(ctx context.Context, binURL, sumsURL, assetName string) ([]byte, error) {
	sumsData, err := httpGet(ctx, sumsURL)
	if err != nil {
		return nil, fmt.Errorf("fetch SHA256SUMS: %w", err)
	}
	expected := findChecksum(string(sumsData), assetName)
	if expected == "" {
		return nil, fmt.Errorf("no checksum for %s in SHA256SUMS", assetName)
	}

	binData, err := httpGet(ctx, binURL)
	if err != nil {
		return nil, fmt.Errorf("fetch binary: %w", err)
	}
	h := sha256.Sum256(binData)
	actual := hex.EncodeToString(h[:])
	if actual != expected {
		return nil, fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
	}
	return binData, nil
}

// findChecksum extracts the hex sha256 for assetName from a SHA256SUMS body.
// Line format: "<hex>  <filename>".
func findChecksum(sumsBody, assetName string) string {
	for _, line := range strings.Split(sumsBody, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == assetName {
			return fields[0]
		}
	}
	return ""
}

// replaceRunningBinary writes binData next to the current executable and
// atomically renames it into place. Works on Linux/macOS because POSIX
// rename-over-open-file keeps the running process's inode intact.
func replaceRunningBinary(binData []byte) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve current binary path: %w", err)
	}
	// Follow symlinks so the actual binary file is replaced, not the symlink.
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	dir := filepath.Dir(exePath)
	tmp, err := os.CreateTemp(dir, "claudeorch-upgrade-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(binData); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close temp: %w", err)
	}

	if err := os.Rename(tmpPath, exePath); err != nil {
		cleanup()
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, exePath, err)
	}
	return nil
}

// httpGet performs a GET with a bounded response size and request timeout.
func httpGet(ctx context.Context, url string) ([]byte, error) {
	tCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(tCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "claudeorch-upgrade/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, httpMaxBody))
}
