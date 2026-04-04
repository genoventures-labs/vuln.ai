package taloscli

import (
	"fmt"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/spf13/cobra"
)

var debugMemoryCmd = &cobra.Command{
	Use:   "debug-memory",
	Short: "Debug the memory collections.",
	Run: func(cmd *cobra.Command, args []string) {
		mm, err := memory.NewMemoryManager()
		if err != nil {
			fmt.Printf("Error initializing memory manager: %v\n", err)
			return
		}

		fmt.Println("--- History Collection ---")
		fmt.Printf("Count: %d\n", mm.HistoryCount())

		results, err := mm.RetrieveContext("secret code", 100)
		if err != nil {
			fmt.Printf("Error retrieving history: %v\n", err)
		} else {
			for i, res := range results {
				fmt.Printf("%d: %s\n", i+1, res)
			}
		}

		fmt.Println("\n--- Knowledge Collection ---")
		fmt.Printf("Count: %d\n", mm.KnowledgeCount())
		results, err = mm.RetrieveKnowledge("secret code", 100)
		if err != nil {
			fmt.Printf("Error retrieving knowledge: %v\n", err)
		} else {
			for i, res := range results {
				fmt.Printf("%d: %s\n", i+1, res)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(debugMemoryCmd)
}
