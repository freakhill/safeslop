package policy

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/load"
)

//go:embed schema/catalog.cue
var catalogSchemaSrc string

// LoadCatalogFile loads a rendered catalog.json from disk and returns the indexed
// catalog view used by the mutation primitives. Callers that need policy checks should
// call Validate on the returned catalog (the embedded/default path validates at render
// time; file-backed CLI mutations validate after edits).
func LoadCatalogFile(jsonPath string) (*Catalog, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("read catalog json %s: %w", jsonPath, err)
	}
	var d catalogData
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("decode catalog json %s: %w", jsonPath, err)
	}
	return newCatalog(d.Packages, d.Bundles, d.Defaults), nil
}

// WriteCatalog re-emits catalog.cue and catalog.json from the same in-memory catalog.
// Per specs/0059 D1 this intentionally normalizes catalog.cue to generated CUE so the
// two committed artifacts move in lockstep after any mutation.
func WriteCatalog(cuePath, jsonPath string, c *Catalog) error {
	if c == nil {
		return fmt.Errorf("write catalog: nil catalog")
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("write catalog: validate: %w", err)
	}

	data := catalogDataForWrite(c)
	wire := catalogWireFromData(data)
	jsonBytes, err := json.MarshalIndent(wire, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal catalog json: %w", err)
	}
	jsonBytes = append(jsonBytes, '\n')

	cueBytes, err := catalogCUEBytes(wire)
	if err != nil {
		return err
	}
	renderedJSON, err := renderCatalogToJSON(cueBytes)
	if err != nil {
		return fmt.Errorf("validate generated catalog cue: %w", err)
	}
	if !bytes.Equal(renderedJSON, jsonBytes) {
		return fmt.Errorf("write catalog: generated cue/json are not synchronized")
	}

	// Both artifacts are generated and cross-checked in memory above, so the only
	// remaining way to violate D1's "never drift" is a torn/partial write. Write each
	// file atomically (same-dir temp + rename) so a crash or ENOSPC leaves the old,
	// still-matched pair rather than a corrupt file or a half-updated pair.
	if err := writeFileAtomic(cuePath, cueBytes); err != nil {
		return fmt.Errorf("write catalog cue: %w", err)
	}
	if err := writeFileAtomic(jsonPath, jsonBytes); err != nil {
		return fmt.Errorf("write catalog json: %w", err)
	}
	return nil
}

// writeFileAtomic writes data to path via a same-directory temp file + rename. rename(2)
// is atomic within a filesystem, so a reader (or a crash) never observes a half-written
// catalog artifact: the target is replaced whole or not at all. This backstops specs/0059
// D1 — catalog.cue and catalog.json each swap atomically, so a failed write cannot leave a
// corrupt file, and any residual drift window shrinks to a metadata-only rename.
func writeFileAtomic(path string, data []byte) (retErr error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for %s: %w", path, err)
	}
	tmp := f.Name()
	// On any failure past this point, do not leave the temp behind.
	defer func() {
		if retErr != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write temp for %s: %w", path, err)
	}
	if err := f.Chmod(0o644); err != nil {
		return fmt.Errorf("chmod temp for %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync temp for %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp for %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename temp to %s: %w", path, err)
	}
	return nil
}

func catalogDataForWrite(c *Catalog) catalogData {
	defaults := cloneStringMap(c.defaults)
	if defaults == nil {
		defaults = map[string]string{}
	}
	return catalogData{
		Packages: c.sortedPackages(),
		Bundles:  c.sortedBundles(),
		Defaults: defaults,
	}
}

func (c *Catalog) sortedPackages() []Package {
	pkgs := c.Packages()
	if pkgs == nil {
		return []Package{}
	}
	for i := range pkgs {
		pkgs[i] = clonePackage(pkgs[i])
	}
	return pkgs
}

func (c *Catalog) sortedBundles() []Bundle {
	bundles := c.Bundles()
	if bundles == nil {
		return []Bundle{}
	}
	for i := range bundles {
		bundles[i].Packages = cloneStringSlice(bundles[i].Packages)
		if bundles[i].Packages == nil {
			bundles[i].Packages = []string{}
		}
	}
	return bundles
}

func catalogCUEBytes(data catalogWire) ([]byte, error) {
	ctx := cuecontext.New()
	root := catalogWireRoot{Catalog: data}
	val := ctx.Encode(root)
	if err := val.Err(); err != nil {
		return nil, fmt.Errorf("encode catalog cue: %w", err)
	}
	cat := val.LookupPath(cue.ParsePath("catalog"))
	if err := cat.Err(); err != nil {
		return nil, fmt.Errorf("encode catalog cue: lookup catalog: %w", err)
	}
	expr, err := format.Node(cat.Syntax(cue.Final(), cue.Concrete(true)))
	if err != nil {
		return nil, fmt.Errorf("format catalog cue expression: %w", err)
	}

	src := append([]byte(catalogGeneratedHeader), expr...)
	src = append(src, '\n')
	out, err := format.Source(src)
	if err != nil {
		return nil, fmt.Errorf("format catalog cue source: %w", err)
	}
	return out, nil
}

const catalogGeneratedHeader = `package safeslop

// Generated by WriteCatalog; specs/0059 D1 requires catalog.cue and catalog.json
// to be re-emitted together so mutation artifacts never drift.
catalog: `

const catalogVirtualDir = "/__safeslop_catalog__"

// renderCatalogToJSON mirrors cmd/rendercatalog's in-process CUE path for tests and
// for WriteCatalog's pre-write sync check: load catalog.cue with schema/catalog.cue as
// one package, validate the concrete catalog value, decode it, and MarshalIndent it.
func renderCatalogToJSON(cueBytes []byte) ([]byte, error) {
	overlay := map[string]load.Source{
		filepath.Join(catalogVirtualDir, "cue.mod", "module.cue"): load.FromString(`module: "safeslop.local/catalog"` + "\n" + `language: version: "v0.9.0"`),
		filepath.Join(catalogVirtualDir, "catalog.cue"):           load.FromBytes(cueBytes),
		filepath.Join(catalogVirtualDir, "schema", "catalog.cue"): load.FromString(catalogSchemaSrc),
	}
	insts := load.Instances([]string{"catalog.cue", "schema/catalog.cue"}, &load.Config{Dir: catalogVirtualDir, Overlay: overlay})
	if len(insts) == 0 {
		return nil, fmt.Errorf("load catalog cue/schema: no CUE instance produced")
	}
	if len(insts) != 1 {
		return nil, fmt.Errorf("load catalog cue/schema: got %d CUE instances, want 1", len(insts))
	}
	if insts[0].Err != nil {
		return nil, fmt.Errorf("load catalog cue/schema:\n%s", errors.Details(insts[0].Err, nil))
	}
	ctx := cuecontext.New()
	val := ctx.BuildInstance(insts[0])
	if err := val.Err(); err != nil {
		return nil, fmt.Errorf("build catalog cue:\n%s", errors.Details(err, nil))
	}
	cat := val.LookupPath(cue.ParsePath("catalog"))
	if err := cat.Err(); err != nil {
		return nil, fmt.Errorf("lookup catalog:\n%s", errors.Details(err, nil))
	}
	if err := cat.Validate(cue.Concrete(true)); err != nil {
		return nil, fmt.Errorf("validate catalog cue:\n%s", errors.Details(err, nil))
	}
	var data map[string]any
	if err := cat.Decode(&data); err != nil {
		return nil, fmt.Errorf("decode catalog cue: %w", err)
	}
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal catalog json: %w", err)
	}
	return append(out, '\n'), nil
}

type catalogWireRoot struct {
	Catalog catalogWire `json:"catalog"`
}

type catalogWire struct {
	Bundles  []catalogWireBundle  `json:"bundles"`
	Defaults map[string]string    `json:"defaults"`
	Packages []catalogWirePackage `json:"packages"`
}

type catalogWirePackage struct {
	BuildFetch    []string             `json:"buildFetch,omitempty"`
	Conflicts     []string             `json:"conflicts,omitempty"`
	Kind          PackageKind          `json:"kind"`
	Name          string               `json:"name"`
	Note          string               `json:"note,omitempty"`
	PublishedAt   string               `json:"publishedAt,omitempty"`
	Requires      []string             `json:"requires,omitempty"`
	Revision      int                  `json:"revision,omitempty"`
	RuntimeEgress []string             `json:"runtimeEgress,omitempty"`
	SHA256        map[string]string    `json:"sha256,omitempty"`
	Upstream      *catalogWireUpstream `json:"upstream,omitempty"`
	Version       string               `json:"version"`
}

type catalogWireBundle struct {
	Description string   `json:"description"`
	Name        string   `json:"name"`
	Packages    []string `json:"packages"`
}

type catalogWireUpstream struct {
	Asset       map[string]string `json:"asset,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	ManifestURL string            `json:"manifestURL,omitempty"`
	URL         string            `json:"url,omitempty"`
}

func catalogWireFromData(data catalogData) catalogWire {
	bundles := make([]catalogWireBundle, len(data.Bundles))
	for i, b := range data.Bundles {
		pkgs := cloneStringSlice(b.Packages)
		if pkgs == nil {
			pkgs = []string{}
		}
		bundles[i] = catalogWireBundle{
			Description: b.Description,
			Name:        b.Name,
			Packages:    pkgs,
		}
	}

	pkgs := make([]catalogWirePackage, len(data.Packages))
	for i, p := range data.Packages {
		// Empty optional collections normalize to nil so cue+json emits agree;
		// otherwise check-catalog-sync drifts after catalog add (specs/0059 W6a).
		// json omitempty already drops nil/len-0, but ctx.Encode emits `[]`/`{}`
		// for a non-nil empty slice/map, so coerce len==0 to nil before both emits.
		pkgs[i] = catalogWirePackage{
			BuildFetch:    emptyStringSliceToNil(p.BuildFetch),
			Conflicts:     emptyStringSliceToNil(p.Conflicts),
			Kind:          p.Kind,
			Name:          p.Name,
			Note:          p.Note,
			PublishedAt:   p.PublishedAt,
			Requires:      emptyStringSliceToNil(p.Requires),
			Revision:      p.Revision,
			RuntimeEgress: emptyStringSliceToNil(p.RuntimeEgress),
			SHA256:        emptyStringMapToNil(p.SHA256),
			Upstream:      catalogWireUpstreamFromPackage(p.Upstream),
			Version:       p.Version,
		}
	}
	defaults := cloneStringMap(data.Defaults)
	if defaults == nil {
		defaults = map[string]string{}
	}
	return catalogWire{Bundles: bundles, Defaults: defaults, Packages: pkgs}
}

func catalogWireUpstreamFromPackage(up *Upstream) *catalogWireUpstream {
	if up == nil {
		return nil
	}
	asset := emptyStringMapToNil(up.Asset)
	// Drop an Upstream whose every field is empty so cue+json emits agree; an
	// otherwise-zero Upstream{Asset: map{}} would emit an empty `upstream: {}`
	// block in cue while json omitempty drops it (specs/0059 W6a).
	if asset == nil && up.Kind == "" && up.ManifestURL == "" && up.URL == "" {
		return nil
	}
	return &catalogWireUpstream{
		Asset:       asset,
		Kind:        up.Kind,
		ManifestURL: up.ManifestURL,
		URL:         up.URL,
	}
}

// emptyStringSliceToNil / emptyStringMapToNil coerce a len-0 (but possibly non-nil)
// collection to nil so json omitempty and ctx.Encode agree on omitting it (specs/0059 W6a).
func emptyStringSliceToNil(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return cloneStringSlice(in)
}

func emptyStringMapToNil(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	return cloneStringMap(in)
}
