package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/fsio"
	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/ui"
	"github.com/DhilipBinny/claudeorch/internal/usage"
	"github.com/spf13/cobra"
)

func init() {
	registerSubcommand(func(root *cobra.Command) {
		root.AddCommand(newListCmd())
	})
}

func newListCmd() *cobra.Command {
	var noUsage bool
	cmd := &cobra.Command{
		Use:           "list",
		Aliases:       []string{"ls"},
		Short:         "List saved profiles with usage bars.",
		RunE:          func(cmd *cobra.Command, args []string) error { return runList(cmd, noUsage) },
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().BoolVar(&noUsage, "no-usage", false, "skip fetching usage data (faster)")
	return cmd
}

func runList(cmd *cobra.Command, noUsage bool) error {
	ui.Init(NoColor())

	// Reconcile before reading — list shows usage from the profile's
	// access token. If Claude Code auto-refreshed tokens in live ~/.claude/,
	// the profile copy is stale and the usage API call returns 401. A quick
	// reconcile pulls the fresh tokens into the profile so the usage fetch
	// works on the first try, without the user having to remember 'sync'.
	lockPath, err := paths.LockFile()
	if err != nil {
		return err
	}
	if err := fsio.EnsureDir(filepath.Dir(lockPath), 0o700); err != nil {
		return err
	}
	release, err := fsio.AcquireLock(context.Background(), lockPath)
	if err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}

	storePath, err := paths.StoreFile()
	if err != nil {
		_ = release()
		return err
	}
	store, err := profile.Load(storePath)
	if err != nil {
		_ = release()
		return fmt.Errorf("load store: %w", err)
	}

	rep, reconcileErr := reconcileProfiles(store, cmd.ErrOrStderr())
	if reconcileErr == nil && rep.Changed() {
		_ = store.Save(storePath)
	}
	_ = release()
	// Lock released — the rest is read-only (usage API calls, rendering).

	names := make([]string, 0, len(store.Profiles))
	for n := range store.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)

	// Track whether we auto-refreshed any tokens (need to save store if so).
	storeModified := false

	rows := make([]ui.ProfileRow, 0, len(names))
	for _, name := range names {
		p := store.Profiles[name]
		row := ui.ProfileRow{
			Name:          name,
			Email:         p.Email,
			OrgName:       p.OrganizationName,
			Active:        store.IsActive(name),
			NeedsReauth:   p.NeedsReauth,
			FiveHourPct:   -1,
			SevenDayPct:   -1,
			FiveHourReset: "-",
			SevenDayReset: "-",
		}

		if !noUsage {
			// freshAccessToken transparently refreshes if expired — standard
			// OAuth client behaviour. Dormant profiles whose access tokens
			// expired hours ago get a fresh one via their stored refresh token.
			accessToken, tokenErr := freshAccessToken(name, store, storePath)
			if tokenErr == nil {
				storeModified = true
			}
			if u, err := fetchUsageWithToken(accessToken, tokenErr); err == nil && u != nil {
				row.FiveHourPct = u.FiveHour.Percent
				row.SevenDayPct = u.SevenDay.Percent
				if !u.FiveHour.ResetsAt.IsZero() {
					row.FiveHourReset = formatDuration(time.Until(u.FiveHour.ResetsAt))
				}
				if !u.SevenDay.ResetsAt.IsZero() {
					row.SevenDayReset = formatDuration(time.Until(u.SevenDay.ResetsAt))
				}
			}
		}
		rows = append(rows, row)
	}

	// Save store if any auto-refreshes happened.
	if storeModified {
		if release2, lockErr := fsio.AcquireLock(context.Background(), lockPath); lockErr == nil {
			_ = store.Save(storePath)
			_ = release2()
		}
	}

	// Refresh NeedsReauth in rows (may have changed from auto-refresh).
	for i, name := range names {
		rows[i].NeedsReauth = store.Profiles[name].NeedsReauth
	}

	if flagJSON {
		return printListJSON(cmd, rows)
	}
	ui.RenderTable(cmd.OutOrStdout(), rows)
	return nil
}

// fetchUsageWithToken calls the usage API with the given access token.
// If tokenErr is non-nil (couldn't obtain a valid token), returns the
// error without making an API call.
func fetchUsageWithToken(accessToken string, tokenErr error) (*usage.Usage, error) {
	if tokenErr != nil {
		return nil, tokenErr
	}
	if accessToken == "" {
		return nil, fmt.Errorf("no access token")
	}
	return usage.Fetch(context.Background(), accessToken)
}

func printListJSON(cmd *cobra.Command, rows []ui.ProfileRow) error {
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"name":            r.Name,
			"email":           r.Email,
			"org":             r.OrgName,
			"active":          r.Active,
			"needs_reauth":    r.NeedsReauth,
			"five_hour_pct":   r.FiveHourPct,
			"seven_day_pct":   r.SevenDayPct,
			"five_hour_reset": r.FiveHourReset,
			"seven_day_reset": r.SevenDayReset,
		})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return nil
}

// formatDuration renders a positive duration with the largest two meaningful
// units: "6d4h", "3h12m", "12m", "42s". Zero-valued higher components are
// dropped so the output never reads "0h12m" or "0d3h".
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm", minutes)
	default:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
}
