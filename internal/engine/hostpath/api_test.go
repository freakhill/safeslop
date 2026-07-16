package hostpath

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestHostPathExportedAPI(t *testing.T) {
	allowedTop := map[string]bool{
		"PiOAuthSourceStatus": true, "PiOAuthSourceOK": true, "PiOAuthSourceMissing": true,
		"PiOAuthSourceUnsafe": true, "PiOAuthSourceBusy": true, "ReadPiOAuthSource": true,
		"ProjectionTargetOutsideRoot": true, "ProjectionTargetExcluded": true,
		"ProjectionSymlinkLoop": true, "ProjectionUnsafeDescendant": true,
		"ProjectionSourceType": true, "ProjectionSnapshotChanged": true,
		"ProjectionSafetyUnsupported": true, "ProjectionRequiredAbsent": true,
		"ProjectionError": true, "ProjectionMount": true, "ProjectionManifest": true,
		"ProjectionBoundaryError": true, "SnapshotProjection": true,
	}
	allowedMethods := map[string]bool{
		"ProjectionError.Error": true, "ProjectionError.Failure": true,
		"ProjectionManifest.MarshalJSON": true, "ProjectionManifest.PresentMounts": true,
	}
	allowedFields := map[string]bool{
		"ProjectionMount.Host": true, "ProjectionMount.Container": true,
		"ProjectionMount.Target": true, "ProjectionMount.Optional": true,
		"ProjectionMount.Label": true, "ProjectionMount.Status": true,
		"ProjectionManifest.Items": true,
	}

	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Dir(thisFile)
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	foundTop := map[string]bool{}
	foundMethods := map[string]bool{}
	foundFields := map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		for _, decl := range file.Decls {
			switch value := decl.(type) {
			case *ast.FuncDecl:
				if value.Recv == nil {
					if ast.IsExported(value.Name.Name) {
						foundTop[value.Name.Name] = true
					}
					continue
				}
				if !ast.IsExported(value.Name.Name) {
					continue
				}
				receiver := receiverName(value.Recv.List[0].Type)
				if ast.IsExported(receiver) {
					foundMethods[receiver+"."+value.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					switch item := spec.(type) {
					case *ast.ValueSpec:
						for _, ident := range item.Names {
							if ast.IsExported(ident.Name) {
								foundTop[ident.Name] = true
							}
						}
					case *ast.TypeSpec:
						if ast.IsExported(item.Name.Name) {
							foundTop[item.Name.Name] = true
						}
						structType, ok := item.Type.(*ast.StructType)
						if !ok {
							continue
						}
						for _, field := range structType.Fields.List {
							for _, ident := range field.Names {
								if ast.IsExported(ident.Name) {
									foundFields[item.Name.Name+"."+ident.Name] = true
								}
							}
						}
					}
				}
			}
		}
	}
	assertExactAPI(t, "top-level", foundTop, allowedTop)
	assertExactAPI(t, "methods", foundMethods, allowedMethods)
	assertExactAPI(t, "fields", foundFields, allowedFields)
}

func receiverName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return receiverName(value.X)
	default:
		return "<generic>"
	}
}

func assertExactAPI(t *testing.T, kind string, got, want map[string]bool) {
	t.Helper()
	var unexpected, missing []string
	for name := range got {
		if !want[name] {
			unexpected = append(unexpected, name)
		}
	}
	for name := range want {
		if !got[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(unexpected)
	sort.Strings(missing)
	if len(unexpected) != 0 || len(missing) != 0 {
		t.Fatalf("hostpath exported %s changed: unexpected=%v missing=%v", kind, unexpected, missing)
	}
}
