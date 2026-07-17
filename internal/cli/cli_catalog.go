package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/freakhill/safeslop/internal/engine/policy"
	"github.com/freakhill/safeslop/internal/jsoncontract"
)

const defaultCatalogDir = "internal/engine/policy"

type httpFetcher struct {
	client *http.Client
}

func newHTTPFetcher() httpFetcher {
	return httpFetcher{client: &http.Client{Timeout: 30 * time.Second}}
}

func (h httpFetcher) Get(url string) ([]byte, error) {
	client := h.client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http status %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func cmdCatalog() *cobra.Command {
	return cmdCatalogWithDeps(defaultDependencies())
}

func cmdCatalogWithDeps(d *dependencies) *cobra.Command {
	c := &cobra.Command{Use: "catalog", Short: "Inspect and maintain the curated package catalog"}
	c.AddCommand(cmdCatalogList(), cmdCatalogBumpWithDeps(d), cmdCatalogProposeVersionWithDeps(d), cmdCatalogAdd(), cmdCatalogAuditWithDeps(d))
	return c
}

func cmdCatalogList() *cobra.Command {
	var output string
	var bundles bool
	c := &cobra.Command{
		Use:   "list [--bundles] --output json",
		Short: "List catalog packages or bundles (enveloped JSON contract)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("catalog list requires --output json")
			}
			cat := policy.DefaultCatalog()
			if bundles {
				emitContract(jsoncontract.OK(map[string]any{"bundles": cat.Bundles(), "defaults": cat.Defaults()}))
				return nil
			}
			emitContract(jsoncontract.OK(map[string]any{"packages": cat.Packages()}))
			return nil
		},
	}
	c.Flags().BoolVar(&bundles, "bundles", false, "list bundles instead of packages")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdCatalogBump() *cobra.Command {
	return cmdCatalogBumpWithDeps(defaultDependencies())
}

func cmdCatalogBumpWithDeps(d *dependencies) *cobra.Command {
	var target, catalogDir, output string
	var security bool
	c := &cobra.Command{
		Use:   "bump <pkg> --to V [--security] [--catalog-dir DIR] [--output json]",
		Short: "Bump a catalog package after resolving pinned digests",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := validateOptionalCatalogOutput(output); err != nil {
				return err
			}
			if len(args) != 1 {
				return catalogMaybeContractError(output, fmt.Errorf("catalog bump requires exactly one package name"))
			}
			if strings.TrimSpace(target) == "" {
				return catalogMaybeContractError(output, fmt.Errorf("catalog bump requires --to"))
			}

			cat, err := loadCatalogDir(catalogDir)
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			lane := "default"
			if security {
				lane = "security"
			}
			next, sheet, err := policy.Bump(cat, args[0], target, lane, d.catalogFetcher)
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			if err := writeCatalogDir(catalogDir, next); err != nil {
				return catalogMaybeContractError(output, err)
			}

			if output == "json" {
				emitContract(jsoncontract.OK(map[string]any{
					"plan_sheet": sheet,
					"package":    args[0],
					"version":    target,
					"written":    []string{"catalog.cue", "catalog.json"},
				}))
				return nil
			}
			fmt.Print(sheet.Render())
			return nil
		},
	}
	c.Flags().StringVar(&target, "to", "", "target version (required)")
	c.Flags().BoolVar(&security, "security", false, "use the security lane to waive major-bump soak only")
	c.Flags().StringVar(&catalogDir, "catalog-dir", defaultCatalogDir, "catalog source directory")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdCatalogProposeVersion() *cobra.Command {
	return cmdCatalogProposeVersionWithDeps(defaultDependencies())
}

func cmdCatalogProposeVersionWithDeps(d *dependencies) *cobra.Command {
	var catalogDir, output string
	c := &cobra.Command{
		Use:   "propose-version <pkg> [--catalog-dir DIR] [--output json]",
		Short: "List upstream version candidates for a catalog package",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := validateOptionalCatalogOutput(output); err != nil {
				return err
			}
			if len(args) != 1 {
				return catalogMaybeContractError(output, fmt.Errorf("catalog propose-version requires exactly one package name"))
			}
			cat, err := loadCatalogForRead(catalogDir)
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			candidates, err := policy.ProposeVersions(cat, args[0], d.catalogFetcher)
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			if output == "json" {
				emitContract(jsoncontract.OK(map[string]any{"candidates": candidates}))
				return nil
			}
			printCatalogCandidates(candidates)
			return nil
		},
	}
	c.Flags().StringVar(&catalogDir, "catalog-dir", "", "catalog source directory (default: embedded catalog)")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdCatalogAdd() *cobra.Command {
	var kind, version, note, catalogDir, output string
	var buildFetch, requires, sha256 []string
	c := &cobra.Command{
		Use:   "add <pkg> --kind K --version V [--note T] [--build-fetch H]... [--requires P]... [--sha256 arch=hex]... [--catalog-dir DIR] [--output json]",
		Short: "Add a pinned package to the catalog",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := validateOptionalCatalogOutput(output); err != nil {
				return err
			}
			if len(args) != 1 {
				return catalogMaybeContractError(output, fmt.Errorf("catalog add requires exactly one package name"))
			}
			if strings.TrimSpace(kind) == "" {
				return catalogMaybeContractError(output, fmt.Errorf("catalog add requires --kind"))
			}
			if strings.TrimSpace(version) == "" {
				return catalogMaybeContractError(output, fmt.Errorf("catalog add requires --version"))
			}
			shaByArch, err := parseCatalogSHA256Flags(sha256)
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			cat, err := loadCatalogDir(catalogDir)
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			next, _, err := policy.AddPackage(cat, policy.Package{
				Name:       args[0],
				Kind:       policy.PackageKind(kind),
				Version:    version,
				SHA256:     shaByArch,
				Requires:   cloneCLIStrings(requires),
				BuildFetch: cloneCLIStrings(buildFetch),
				Note:       note,
			})
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			if err := writeCatalogDir(catalogDir, next); err != nil {
				return catalogMaybeContractError(output, err)
			}
			if output == "json" {
				emitContract(jsoncontract.OK(map[string]any{"package": args[0], "added": true}))
				return nil
			}
			fmt.Printf("added package %s\nwrote catalog.cue catalog.json\n", args[0])
			return nil
		},
	}
	c.Flags().StringVar(&kind, "kind", "", "package kind: apt, npm, binary, or pip")
	c.Flags().StringVar(&version, "version", "", "pinned package version")
	c.Flags().StringVar(&note, "note", "", "provenance/review note")
	c.Flags().StringArrayVar(&buildFetch, "build-fetch", nil, "build-time fetch host (repeatable)")
	c.Flags().StringArrayVar(&requires, "requires", nil, "required catalog package (repeatable)")
	c.Flags().StringArrayVar(&sha256, "sha256", nil, "binary digest as arch=64hex (repeatable)")
	c.Flags().StringVar(&catalogDir, "catalog-dir", defaultCatalogDir, "catalog source directory")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdCatalogAudit() *cobra.Command {
	return cmdCatalogAuditWithDeps(defaultDependencies())
}

func cmdCatalogAuditWithDeps(d *dependencies) *cobra.Command {
	var catalogDir, output string
	c := &cobra.Command{
		Use:   "audit [--catalog-dir DIR] [--output json]",
		Short: "Report catalog staleness and advisory lanes",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := validateOptionalCatalogOutput(output); err != nil {
				return err
			}
			if len(args) != 0 {
				return catalogMaybeContractError(output, fmt.Errorf("catalog audit takes no arguments"))
			}
			cat, err := loadCatalogForRead(catalogDir)
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			report, err := policy.Audit(cat, d.catalogFetcher)
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			if output == "json" {
				emitContract(jsoncontract.OK(map[string]any{"report": report}))
				return nil
			}
			printCatalogAudit(report)
			return nil
		},
	}
	c.Flags().StringVar(&catalogDir, "catalog-dir", "", "catalog source directory (default: embedded catalog)")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdBundle() *cobra.Command {
	return cmdBundleWithDeps(defaultDependencies())
}

func cmdBundleWithDeps(_ *dependencies) *cobra.Command {
	c := &cobra.Command{Use: "bundle", Short: "Manage curated package bundles"}
	c.AddCommand(cmdBundleAdd(), cmdBundleRemove(), cmdBundleList())
	return c
}

func cmdBundleAdd() *cobra.Command {
	var catalogDir, output string
	c := &cobra.Command{
		Use:   "add <name> <pkg> [<pkg>...] [--catalog-dir DIR] [--output json]",
		Short: "Add packages to an existing bundle",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := validateOptionalCatalogOutput(output); err != nil {
				return err
			}
			if len(args) < 2 {
				return catalogMaybeContractError(output, fmt.Errorf("bundle add requires a bundle name and at least one package"))
			}
			cat, err := loadCatalogDir(catalogDir)
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			next, diff, err := policy.BundleAdd(cat, args[0], args[1:]...)
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			if err := writeCatalogDir(catalogDir, next); err != nil {
				return catalogMaybeContractError(output, err)
			}
			if output == "json" {
				emitContract(jsoncontract.OK(map[string]any{"bundle": args[0], "added": diff.AddedPackages, "written": []string{"catalog.cue", "catalog.json"}}))
				return nil
			}
			fmt.Printf("bundle %s added: %s\nwrote catalog.cue catalog.json\n", args[0], strings.Join(diff.AddedPackages, ", "))
			return nil
		},
	}
	c.Flags().StringVar(&catalogDir, "catalog-dir", defaultCatalogDir, "catalog source directory")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdBundleRemove() *cobra.Command {
	var catalogDir, output string
	c := &cobra.Command{
		Use:   "remove <name> <pkg> [<pkg>...] [--catalog-dir DIR] [--output json]",
		Short: "Remove packages from an existing bundle",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if err := validateOptionalCatalogOutput(output); err != nil {
				return err
			}
			if len(args) < 2 {
				return catalogMaybeContractError(output, fmt.Errorf("bundle remove requires a bundle name and at least one package"))
			}
			cat, err := loadCatalogDir(catalogDir)
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			next, diff, err := policy.BundleRemove(cat, args[0], args[1:]...)
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			if err := writeCatalogDir(catalogDir, next); err != nil {
				return catalogMaybeContractError(output, err)
			}
			if output == "json" {
				emitContract(jsoncontract.OK(map[string]any{"bundle": args[0], "removed": diff.RemovedPackages, "written": []string{"catalog.cue", "catalog.json"}}))
				return nil
			}
			fmt.Printf("bundle %s removed: %s\nwrote catalog.cue catalog.json\n", args[0], strings.Join(diff.RemovedPackages, ", "))
			return nil
		},
	}
	c.Flags().StringVar(&catalogDir, "catalog-dir", defaultCatalogDir, "catalog source directory")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func cmdBundleList() *cobra.Command {
	var catalogDir, output string
	c := &cobra.Command{
		Use:   "list [--catalog-dir DIR] --output json",
		Short: "List curated package bundles (enveloped JSON contract)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if output != "json" {
				return fmt.Errorf("bundle list requires --output json")
			}
			if len(args) != 0 {
				return catalogMaybeContractError(output, fmt.Errorf("bundle list takes no arguments"))
			}
			cat, err := loadCatalogForRead(catalogDir)
			if err != nil {
				return catalogMaybeContractError(output, err)
			}
			emitContract(jsoncontract.OK(map[string]any{"bundles": cat.Bundles()}))
			return nil
		},
	}
	c.Flags().StringVar(&catalogDir, "catalog-dir", "", "catalog source directory (default: embedded catalog)")
	c.Flags().StringVar(&output, "output", "", "output format: json")
	return c
}

func loadCatalogForRead(catalogDir string) (*policy.Catalog, error) {
	if strings.TrimSpace(catalogDir) == "" {
		cat := policy.DefaultCatalog()
		if err := cat.Validate(); err != nil {
			return nil, err
		}
		return cat, nil
	}
	return loadCatalogDir(catalogDir)
}

func loadCatalogDir(catalogDir string) (*policy.Catalog, error) {
	cat, err := policy.LoadCatalogFile(filepath.Join(catalogDir, "catalog.json"))
	if err != nil {
		return nil, err
	}
	if err := cat.Validate(); err != nil {
		return nil, err
	}
	return cat, nil
}

func writeCatalogDir(catalogDir string, cat *policy.Catalog) error {
	return policy.WriteCatalog(filepath.Join(catalogDir, "catalog.cue"), filepath.Join(catalogDir, "catalog.json"), cat)
}

func validateOptionalCatalogOutput(output string) error {
	if output != "" && output != "json" {
		return fmt.Errorf("--output must be json")
	}
	return nil
}

func catalogMaybeContractError(output string, err error) error {
	if output != "json" {
		return err
	}
	emitContract(jsoncontract.Error(jsoncontract.NewMessage(jsoncontract.CodeInvalidArgument, err.Error(), false, nil)))
	return errOutputEmitted
}

func parseCatalogSHA256Flags(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, value := range values {
		arch, digest, ok := strings.Cut(value, "=")
		arch = strings.TrimSpace(arch)
		digest = strings.TrimSpace(digest)
		if !ok || arch == "" || digest == "" {
			return nil, fmt.Errorf("--sha256 must be arch=hex")
		}
		if _, exists := out[arch]; exists {
			return nil, fmt.Errorf("--sha256 repeats arch %q", arch)
		}
		out[arch] = digest
	}
	return out, nil
}

func cloneCLIStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func printCatalogCandidates(candidates []policy.Candidate) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "VERSION\tMAGNITUDE\tHUMAN-CONFIRM\tYANKED")
	for _, c := range candidates {
		fmt.Fprintf(tw, "%s\t%s\t%t\t%t\n", c.Version, c.Magnitude, c.RequiresHumanConfirm, c.IsYanked)
	}
	_ = tw.Flush()
}

func printCatalogAudit(report *policy.Report) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCURRENT\tLATEST\tBEHIND\tLANE")
	if report != nil {
		for _, row := range report.Rows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", row.Name, row.Current, dashIfEmpty(row.Latest), row.VersionsBehind, row.SuggestedLane)
		}
	}
	_ = tw.Flush()
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
