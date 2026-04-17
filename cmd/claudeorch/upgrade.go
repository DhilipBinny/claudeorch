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

	fmt.Fprintf(out, "\nDownloading %s\n", assetName)
	// Progress output goes to stderr (status, not result), so piping the
	// upgrade output doesn't capture progress-bar noise.
	binData, err := fetchAndVerify(ctx, binURL, sumsURL, assetName, cmd.ErrOrStderr())
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "\ndownload failed")
		return fmt.Errorf("download: %w", err)
	}
	fmt.Fprintf(out, "  verified (%s, sha256 OK)\n", humanBytes(int64(len(binData))))

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

// fetchAndVerify downloads the binary (with progress output) and its
// SHA256SUMS, verifies the hash for assetName, and returns the binary bytes.
func fetchAndVerify(ctx context.Context, binURL, sumsURL, assetName string, progress io.Writer) ([]byte, error) {
	sumsData, err := httpGet(ctx, sumsURL)
	if err != nil {
		return nil, fmt.Errorf("fetch SHA256SUMS: %w", err)
	}
	expected := findChecksum(string(sumsData), assetName)
	if expected == "" {
		return nil, fmt.Errorf("no checksum for %s in SHA256SUMS", assetName)
	}

	binData, err := httpDownloadWithProgress(ctx, binURL, progress)
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
	exePath, err := resolvedExecutable()
	if err != nil {
		return fmt.Errorf("resolve current binary path: %w", err)
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

// httpDownloadWithProgress is httpGet + a live progress indicator written
// to 'progress'. Uses Content-Length when the server provides it to render
// "<received> / <total>  <pct>%  <speed>"; otherwise just byte count + speed.
func httpDownloadWithProgress(ctx context.Context, url string, progress io.Writer) ([]byte, error) {
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
	pr := &progressReader{
		r:     io.LimitReader(resp.Body, httpMaxBody),
		total: resp.ContentLength,
		out:   progress,
		start: time.Now(),
		tty:   stderrIsTerminal(),
	}
	data, readErr := io.ReadAll(pr)
	pr.finish()
	return data, readErr
}

// progressReader wraps an io.Reader and prints a live download progress
// bar to 'out'. Two display modes:
//
//   - TTY: an in-place 20-cell bar with bytes/percent/speed/ETA,
//     rate-limited to ~10 fps and redrawn via \r on each tick.
//   - non-TTY: emit one line every ~25% so CI logs stay readable
//     without drowning in progress updates.
//
// Speed is an EWMA over the last ~1 s of byte deltas so the number
// doesn't jump around between ticks.
type progressReader struct {
	r        io.Reader
	total    int64 // -1 when server omits Content-Length
	read     int64
	start    time.Time
	lastTick time.Time
	lastRead int64
	speed    float64 // EWMA bytes/sec
	out      io.Writer
	tty      bool
	// non-TTY progress throttling: emit a milestone line when we cross
	// the next threshold.
	nextMilestone float64
}

const (
	barWidth     = 20
	tickInterval = 100 * time.Millisecond
	ewmaAlpha    = 0.3 // smoothing factor — higher = more reactive
)

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	now := time.Now()
	if now.Sub(p.lastTick) >= tickInterval || err != nil {
		p.update(now)
		p.render()
		p.lastTick = now
	}
	return n, err
}

// update refreshes the EWMA speed based on bytes transferred since the last tick.
func (p *progressReader) update(now time.Time) {
	if p.lastTick.IsZero() {
		// First tick — seed speed from overall rate so it's non-zero immediately.
		elapsed := now.Sub(p.start).Seconds()
		if elapsed > 0 {
			p.speed = float64(p.read) / elapsed
		}
		p.lastRead = p.read
		return
	}
	dt := now.Sub(p.lastTick).Seconds()
	if dt <= 0 {
		return
	}
	instant := float64(p.read-p.lastRead) / dt
	if p.speed == 0 {
		p.speed = instant
	} else {
		p.speed = ewmaAlpha*instant + (1-ewmaAlpha)*p.speed
	}
	p.lastRead = p.read
}

func (p *progressReader) render() {
	if !p.tty {
		p.renderMilestone()
		return
	}
	if p.total > 0 {
		pct := float64(p.read) / float64(p.total)
		filled := int(pct * barWidth)
		if filled > barWidth {
			filled = barWidth
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		eta := "--"
		switch {
		case p.read >= p.total && p.total > 0:
			eta = "done"
		case p.speed > 0:
			remaining := float64(p.total-p.read) / p.speed
			eta = formatDurationShort(time.Duration(remaining * float64(time.Second)))
		}
		fmt.Fprintf(p.out, "\r  \x1b[36m%s\x1b[0m  %3d%%  %s / %s  %s/s  ETA %s    ",
			bar, int(pct*100+0.5),
			humanBytes(p.read), humanBytes(p.total),
			humanBytes(int64(p.speed)), eta)
	} else {
		fmt.Fprintf(p.out, "\r  %s  %s/s          ",
			humanBytes(p.read), humanBytes(int64(p.speed)))
	}
}

// renderMilestone prints one line every ~25 % for non-TTY consumers.
// When total is unknown, emits one line every ~2 MB.
func (p *progressReader) renderMilestone() {
	var cross float64
	if p.total > 0 {
		cross = float64(p.read) / float64(p.total)
		if cross < p.nextMilestone && p.read < p.total {
			return
		}
		next := p.nextMilestone + 0.25
		if next > 1 {
			next = 1
		}
		p.nextMilestone = next
		fmt.Fprintf(p.out, "  %3d%%  %s / %s  %s/s\n",
			int(cross*100+0.5), humanBytes(p.read), humanBytes(p.total), humanBytes(int64(p.speed)))
		return
	}
	// Unknown total — every ~2 MB.
	threshold := p.nextMilestone
	if float64(p.read) < threshold {
		return
	}
	p.nextMilestone = threshold + (2 << 20)
	fmt.Fprintf(p.out, "  %s  %s/s\n",
		humanBytes(p.read), humanBytes(int64(p.speed)))
}

// finish leaves the final bar state visible and moves to a fresh line so
// subsequent output starts cleanly. In TTY mode this means a completed bar
// (100%, full green glyphs) stays on screen as a "done" marker rather than
// vanishing between "Downloading…" and the next status line.
func (p *progressReader) finish() {
	if p.tty {
		fmt.Fprintln(p.out)
	}
}

// formatDurationShort formats a duration compactly: "42s", "3m15s", "1h4m".
func formatDurationShort(d time.Duration) string {
	if d < 0 {
		return "--"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int((d - time.Duration(m)*time.Minute).Seconds())
		return fmt.Sprintf("%dm%ds", m, s)
	}
	h := int(d.Hours())
	m := int((d - time.Duration(h)*time.Hour).Minutes())
	return fmt.Sprintf("%dh%dm", h, m)
}

// humanBytes formats bytes as e.g. "8.6 MB" / "1.4 KB" / "500 B".
func humanBytes(n int64) string {
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1f GB", float64(n)/gb)
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/mb)
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/kb)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
