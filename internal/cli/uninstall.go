package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/freakhill/safeslop/internal/engine/install"
	"github.com/freakhill/safeslop/internal/engine/receipt"
	"github.com/freakhill/safeslop/internal/engine/uninstall"
)

// cmdUninstall is the receipt-driven, consent-gated mirror of `install`: it removes only what safeslop's
// install receipt says it placed, states the Path A/B reversibility asymmetry honestly, and enumerates
// the tools it will NOT touch. There is no --force overriding the untouched boundary (specs/0040/0041).
func cmdUninstall() *cobra.Command {
	c := &cobra.Command{
		Use:   "uninstall [tool...]",
		Short: "Remove tools safeslop installed (receipt-driven; never touches what it didn't install)",
	}

	c.AddCommand(&cobra.Command{
		Use:   "plan [tool...]",
		Short: "Show what would be removed (Path A files / Path B delegate) and what is left untouched",
		RunE: func(_ *cobra.Command, args []string) error {
			if jsonOut {
				out, err := renderUninstallPlanJSON(args)
				if err != nil {
					return err
				}
				fmt.Println(out)
				return nil
			}
			p, err := buildUninstallPlan(args)
			if err != nil {
				return err
			}
			printUninstallPlan(p)
			return nil
		},
	})

	c.AddCommand(func() *cobra.Command {
		var dryRun, yes, purge bool
		ac := &cobra.Command{
			Use:   "apply [tool...]",
			Short: "Remove the installed tools (typed confirmation required unless --yes)",
			RunE: func(cmd *cobra.Command, args []string) error {
				p, err := buildUninstallPlan(args)
				if err != nil {
					return err
				}
				printUninstallPlan(p)
				if len(p.Items) == 0 {
					fmt.Println("\nnothing to uninstall.")
					return nil
				}
				if dryRun {
					fmt.Println("\n(dry-run: nothing removed)")
					return nil
				}

				// Consent gate — the symmetric mirror of the install gate. Typed, not a bare y/n.
				confirmed := yes
				if !confirmed {
					if !promptTyped(os.Stdin, "uninstall") {
						return fmt.Errorf("aborted: confirmation not given")
					}
					confirmed = true
				}
				if purge {
					// Second tier: no recursive rm of user dirs today (nix/rustup define no extra purge
					// data), so purge == uninstall here — but still gate it behind its own confirmation so
					// the escalation is explicit when per-tool purge data is added later.
					fmt.Println("\n--purge: no additional user-data removal is defined for these tools; proceeding as a normal uninstall.")
					if !yes && !promptTyped(os.Stdin, "purge") {
						return fmt.Errorf("aborted: purge confirmation not given")
					}
				}

				dirs, err := install.DefaultDirs()
				if err != nil {
					return err
				}
				rcPath, err := receipt.DefaultPath()
				if err != nil {
					return err
				}
				store, err := receipt.Load(rcPath)
				if err != nil {
					return err
				}
				eng := uninstall.NewEngine(dirs)

				// Path A first (recoverable), then Path B; halt the whole run on any Path B failure.
				for _, ph := range []uninstall.Kind{uninstall.RemovePathA, uninstall.DelegatePathB} {
					for _, it := range p.Items {
						if it.Kind != ph {
							continue
						}
						res, err := eng.ApplyItem(cmd.Context(), it, confirmed)
						if err != nil {
							return fmt.Errorf("uninstall %s: %w", it.Tool, err)
						}
						reportItem(res)
						if err := store.Remove(it.Tool); err != nil {
							return fmt.Errorf("uninstall %s: clear receipt: %w", it.Tool, err)
						}
					}
				}
				return nil
			},
		}
		ac.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be removed without doing it")
		ac.Flags().BoolVar(&yes, "yes", false, "skip the interactive confirmation (for automation); does NOT widen what is removed")
		ac.Flags().BoolVar(&purge, "purge", false, "also remove user data where a tool defines it (second confirmation)")
		return ac
	}())

	c.AddCommand(&cobra.Command{
		Use:   "rollback [stamp]",
		Short: "Restore Path A files from the trash (the most recent removal if no stamp given)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			stamp := ""
			if len(args) == 1 {
				stamp = args[0]
			}
			restored, err := uninstall.Rollback(stamp)
			if err != nil {
				return err
			}
			if jsonOut {
				emitJSON(map[string]any{"ok": true, "restored": restored})
				return nil
			}
			fmt.Printf("restored %d file(s)\n", len(restored))
			for _, r := range restored {
				fmt.Printf("  %s\n", r)
			}
			return nil
		},
	})

	c.AddCommand(func() *cobra.Command {
		var olderThan time.Duration
		pc := &cobra.Command{
			Use:   "prune",
			Short: "Delete trashed removals older than the TTL (default 7 days)",
			Args:  cobra.NoArgs,
			RunE: func(_ *cobra.Command, _ []string) error {
				n, err := uninstall.Prune(olderThan, time.Now())
				if err != nil {
					return err
				}
				if jsonOut {
					emitJSON(map[string]any{"pruned": n})
					return nil
				}
				fmt.Printf("pruned %d trashed removal(s)\n", n)
				return nil
			},
		}
		pc.Flags().DurationVar(&olderThan, "older-than", 7*24*time.Hour, "delete trash older than this")
		return pc
	}())

	return c
}

// promptTyped prints the consent prompt and returns true only if the user types exactly want.
func promptTyped(in *os.File, want string) bool {
	fmt.Printf("\nType %q to proceed (anything else aborts): ", want)
	line, _ := bufio.NewReader(in).ReadString('\n')
	return confirmationMatches(line, want)
}

// confirmationMatches is the pure comparison behind the consent gate (trims surrounding whitespace).
func confirmationMatches(input, want string) bool {
	return strings.TrimSpace(input) == want
}

func buildUninstallPlan(tools []string) (uninstall.Plan, error) {
	rcPath, err := receipt.DefaultPath()
	if err != nil {
		return uninstall.Plan{}, err
	}
	store, err := receipt.Load(rcPath)
	if err != nil {
		return uninstall.Plan{}, err
	}
	st := install.Status(context.Background(), Version)
	return uninstall.Build(store, st, tools)
}

func renderUninstallPlanJSON(tools []string) (string, error) {
	p, err := buildUninstallPlan(tools)
	if err != nil {
		return "", err
	}
	b, _ := json.MarshalIndent(map[string]any{
		"items":     p.Items,
		"untouched": p.Untouched,
	}, "", "  ")
	return string(b), nil
}

func printUninstallPlan(p uninstall.Plan) {
	fmt.Printf("%d tool(s) to remove\n", len(p.Items))
	for _, it := range p.Items {
		switch it.Kind {
		case uninstall.DelegatePathB:
			fmt.Printf("  %-10s path B  delegate: %s\n", it.Tool, strings.Join(it.Delegate, " "))
		default:
			rev := "restorable from trash"
			fmt.Printf("  %-10s path A  %d file(s), %s\n", it.Tool, len(it.Files), rev)
		}
	}
	if p.HasIrreversible() {
		fmt.Println("\nreversibility: Path A removals are restorable from ~/.local/share/safeslop/trash for 7 days;")
		fmt.Println("               Path B removals (e.g. a destroyed APFS volume / daemon) are IRREVERSIBLE.")
	} else if len(p.Items) > 0 {
		fmt.Println("\nreversibility: all removals are Path A — restorable from trash for 7 days.")
	}
	if len(p.Untouched) > 0 {
		fmt.Println("\nUntouched (not installed by safeslop — left in place):")
		for _, u := range p.Untouched {
			loc := u.Path
			if loc == "" {
				loc = "-"
			}
			fmt.Printf("  %-10s %s  (%s)\n", u.Tool, loc, u.Reason)
		}
	}
}

func reportItem(res uninstall.Result) {
	if jsonOut {
		emitJSON(map[string]any{
			"tool": res.Tool, "kind": res.Kind.String(),
			"trashed": res.Trashed, "skipped": res.Skipped, "notes": res.Notes,
		})
		return
	}
	fmt.Printf("removed %s (path %s)\n", res.Tool, res.Kind.String())
	for _, s := range res.Skipped {
		fmt.Printf("  skipped %s\n", s)
	}
	for _, n := range res.Notes {
		fmt.Printf("  note: %s\n", n)
	}
}
