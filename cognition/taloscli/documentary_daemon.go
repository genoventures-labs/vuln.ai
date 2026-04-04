package taloscli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/orchestration"
	"github.com/spf13/cobra"
)

var (
	documentaryClientID string
	documentaryKeyStore string
	documentaryOnce     bool
	documentaryTickSec  int
)

var documentaryCmd = &cobra.Command{
	Use:   "documentary",
	Short: "Documentary engine IAM and daemon controls.",
}

var documentaryProvisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision dedicated IAM credentials for documentary-engine.",
	Run: func(cmd *cobra.Command, args []string) {
		id, err := orchestration.ProvisionDocumentaryEngineClient(strings.TrimSpace(documentaryClientID), strings.TrimSpace(documentaryKeyStore))
		if err != nil {
			fmt.Printf("Provision failed: %v\n", err)
			return
		}
		fmt.Printf("Provisioned documentary daemon client_id: %s\n", id)
		fmt.Printf("Saved credentials to: %s\n", strings.TrimSpace(documentaryKeyStore))
	},
}

var documentaryDaemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run documentary daemon loop (24/7 foreground).",
	Run: func(cmd *cobra.Command, args []string) {
		mm, err := memory.NewMemoryManager()
		if err != nil {
			fmt.Printf("Failed to init memory manager: %v\n", err)
			return
		}
		daemon := orchestration.NewDocumentaryDaemon(mm)
		if documentaryTickSec > 0 {
			daemon.Tick = time.Duration(documentaryTickSec) * time.Second
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-stop
			fmt.Println("Stopping documentary daemon...")
			cancel()
		}()

		fmt.Println("Documentary daemon started.")
		if documentaryOnce {
			if err := daemon.RunOnce(ctx); err != nil {
				fmt.Printf("Daemon run-once failed: %v\n", err)
				return
			}
			fmt.Println("Documentary daemon run-once completed.")
			return
		}
		if err := daemon.Run(ctx); err != nil {
			fmt.Printf("Documentary daemon stopped with error: %v\n", err)
		}
	},
}

func init() {
	documentaryProvisionCmd.Flags().StringVar(&documentaryClientID, "client-id", "documentary-engine", "Client ID for documentary daemon")
	documentaryProvisionCmd.Flags().StringVar(&documentaryKeyStore, "keys-file", "api_keys.yaml", "Local key store path")

	documentaryDaemonCmd.Flags().BoolVar(&documentaryOnce, "once", false, "Run one cycle and exit")
	documentaryDaemonCmd.Flags().IntVar(&documentaryTickSec, "tick-sec", 45, "Polling interval in seconds")

	documentaryCmd.AddCommand(documentaryProvisionCmd)
	documentaryCmd.AddCommand(documentaryDaemonCmd)
	rootCmd.AddCommand(documentaryCmd)
}
