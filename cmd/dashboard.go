package cmd

import (
	"os"
	"strings"

	"github.com/atredispartners/flamingo/pkg/dashboard"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var dashboardParams struct {
	Addr          string
	RevealSecrets bool
}

var dashboardCmd = &cobra.Command{
	Use:   "dashboard [files...]",
	Short: "Serve the capture dashboard over existing JSON-lines capture logs",
	Long: `Serves a local web view over one or more flamingo capture logs
(JSON lines, as written by capture runs). Secrets are masked in the UI
and API unless --reveal-secrets is set; the dashboard binds loopback
unless --addr says otherwise.`,
	Args: cobra.ArbitraryArgs,
	Run:  runDashboard,
}

func runDashboard(cmd *cobra.Command, args []string) {
	files := args
	if len(files) == 0 {
		// Same default output file the capture command writes.
		files = []string{"flamingo.log"}
	}

	store := dashboard.NewStore()
	for _, f := range files {
		n, skipped, err := store.IngestFile(f)
		if err != nil {
			// An explicitly named log that cannot be read is a hard error:
			// the operator probably mistyped a path.
			log.Errorf("dashboard: %s", err)
			os.Exit(1)
		}
		log.Infof("dashboard: loaded %d records from %s (%d lines skipped)", n, f, skipped)
	}

	srv, err := dashboard.NewServer(store, dashboard.Options{
		Addr:          dashboardParams.Addr,
		RevealSecrets: dashboardParams.RevealSecrets,
		SourceLabel:   strings.Join(files, ", "),
	})
	if err != nil {
		log.Errorf("dashboard: %s", err)
		os.Exit(1)
	}

	log.Infof("dashboard: serving on http://%s (secrets %s)", dashboardParams.Addr, revealWord(dashboardParams.RevealSecrets))
	if err := srv.ListenAndServe(); err != nil {
		log.Errorf("dashboard: %s", err)
		os.Exit(1)
	}
}

// revealWord picks the human word for the current secret-handling state.
func revealWord(reveal bool) string {
	if reveal {
		return "revealed"
	}
	return "masked"
}

func init() {
	dashboardCmd.Flags().StringVarP(&dashboardParams.Addr, "addr", "", dashboard.DefaultAddr, "Bind address for the dashboard (loopback by default)")
	dashboardCmd.Flags().BoolVarP(&dashboardParams.RevealSecrets, "reveal-secrets", "", false, "Unmask captured secrets (passwords, keys, hashes, community strings)")
	rootCmd.AddCommand(dashboardCmd)
}
