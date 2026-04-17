package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/DhilipBinny/claudeorch/internal/paths"
	"github.com/DhilipBinny/claudeorch/internal/profile"
	"github.com/DhilipBinny/claudeorch/internal/schema"
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

	storePath, err := paths.StoreFile()
	if err != nil {
		return err
	}
	store, err := profile.Load(storePath)
	if err != nil {
		return fmt.Errorf("load store: %w", err)
	}

	// Sort profile names for stable output.
	names := make([]string, 0, len(store.Profiles))
	for n := range store.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)

	rows := make([]ui.ProfileRow, 0, len(names))
	for _, name := range names {
		p := store.Profiles[name]
		row := ui.ProfileRow{
			Name:        name,
			Email:       p.Email,
			OrgName:     p.OrganizationName,
			Active:      store.IsActive(name),
			NeedsReauth: p.NeedsReauth,
			UsagePct:    -1,
			UsageLabel:  "-",
			ResetLabel:  "-",
		}

		if !noUsage {
			if u, usageErr := fetchProfileUsage(name); usageErr == nil && u != nil {
				row.UsagePct = u.PercentUsed()
				row.UsageLabel = formatTokens(u.UsedTokens, u.LimitTokens)
				if !u.ResetAt.IsZero() {
					row.ResetLabel = formatDuration(time.Until(u.ResetAt))
				}
			}
		}
		rows = append(rows, row)
	}

	if flagJSON {
		return printListJSON(cmd, rows)
	}
	ui.RenderTable(cmd.OutOrStdout(), rows)
	return nil
}

// fetchProfileUsage reads stored credentials for name and calls the usage API.
func fetchProfileUsage(name string) (*usage.Usage, error) {
	profileDir, err := paths.ProfileDir(name)
	if err != nil {
		return nil, err
	}
	credsData, err := os.ReadFile(filepath.Join(profileDir, "credentials.json"))
	if err != nil {
		return nil, err
	}
	creds, err := schema.ParseCredentials(credsData)
	if err != nil {
		return nil, err
	}
	return usage.Fetch(context.Background(), creds.AccessToken)
}

func printListJSON(cmd *cobra.Command, rows []ui.ProfileRow) error {
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		m := map[string]any{
			"name":         r.Name,
			"email":        r.Email,
			"org":          r.OrgName,
			"active":       r.Active,
			"needs_reauth": r.NeedsReauth,
			"usage_pct":    r.UsagePct,
			"usage_label":  r.UsageLabel,
			"reset_label":  r.ResetLabel,
		}
		out = append(out, m)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return nil
}

func formatTokens(used, limit int64) string {
	if limit <= 0 {
		return formatK(used)
	}
	return fmt.Sprintf("%s / %s", formatK(used), formatK(limit))
}

func formatK(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
