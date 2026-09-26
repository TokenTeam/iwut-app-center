package architecture_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "iwut-app-center"

var forbiddenGlobalPackages = map[string]struct{}{
	"biz":     {},
	"data":    {},
	"domain":  {},
	"service": {},
	"util":    {},
}

var domainInfrastructureImports = []string{
	"database/sql",
	"encoding/json",
	"log",
	"log/slog",
	"net/http",
}

type packageLocation struct {
	capability  string
	layer       string
	packagePath string
}

func TestArchitectureBoundaries(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == ".git" || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}

		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relativePath = filepath.ToSlash(relativePath)
		location := locatePackage(relativePath)
		if reason := sourcePathViolation(relativePath); reason != "" {
			violations = append(violations, fmt.Sprintf("%s: %s", relativePath, reason))
		}

		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", relativePath, err)
		}
		for _, importSpec := range parsed.Imports {
			importPath, err := strconv.Unquote(importSpec.Path.Value)
			if err != nil {
				return fmt.Errorf("parse import in %s: %w", relativePath, err)
			}
			if reason := importViolation(location, importPath); reason != "" {
				violations = append(violations, fmt.Sprintf("%s imports %s: %s", relativePath, importPath, reason))
			}
		}
		if location.layer == "domain" {
			violations = append(violations, infrastructureTagViolations(relativePath, parsed)...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}

	violations = append(violations, globalPackageViolations(root)...)
	if len(violations) == 0 {
		return
	}
	sort.Strings(violations)
	t.Fatalf("architecture boundary violations:\n- %s", strings.Join(violations, "\n- "))
}

func TestImportRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		location   packageLocation
		importPath string
		allowed    bool
	}{
		{name: "domain may use shared values", location: packageLocation{capability: "version", layer: "domain"}, importPath: modulePath + "/internal/shared", allowed: true},
		{name: "domain may not use another capability", location: packageLocation{capability: "version", layer: "domain"}, importPath: modulePath + "/internal/application/domain", allowed: false},
		{name: "domain may not use MongoDB", location: packageLocation{capability: "version", layer: "domain"}, importPath: "go.mongodb.org/mongo-driver/v2/bson", allowed: false},
		{name: "domain external dependency needs an architecture decision", location: packageLocation{capability: "version", layer: "domain"}, importPath: "golang.org/x/text/unicode/norm", allowed: false},
		{name: "profile domain NFC approved narrow exception", location: packageLocation{capability: "profile", layer: "domain", packagePath: "internal/profile/domain"}, importPath: "golang.org/x/text/unicode/norm", allowed: true},
		{name: "profile domain child package NFC denied", location: packageLocation{capability: "profile", layer: "domain", packagePath: "internal/profile/domain/nested"}, importPath: "golang.org/x/text/unicode/norm", allowed: false},
		{name: "profile domain other xtext package denied", location: packageLocation{capability: "profile", layer: "domain"}, importPath: "golang.org/x/text/cases", allowed: false},
		{name: "profile usecase NFC denied", location: packageLocation{capability: "profile", layer: "usecase"}, importPath: "golang.org/x/text/unicode/norm", allowed: false},
		{name: "profile port NFC denied", location: packageLocation{capability: "profile", layer: "port"}, importPath: "golang.org/x/text/unicode/norm", allowed: false},
		{name: "shared NFC denied", location: packageLocation{capability: "shared", layer: "shared"}, importPath: "golang.org/x/text/unicode/norm", allowed: false},
		{name: "port may use own domain", location: packageLocation{capability: "version", layer: "port"}, importPath: modulePath + "/internal/version/domain", allowed: true},
		{name: "port may not use another capability", location: packageLocation{capability: "version", layer: "port"}, importPath: modulePath + "/internal/application/domain", allowed: false},
		{name: "usecase may use own port", location: packageLocation{capability: "version", layer: "usecase"}, importPath: modulePath + "/internal/version/port", allowed: true},
		{name: "usecase may not use adapter", location: packageLocation{capability: "version", layer: "usecase"}, importPath: modulePath + "/internal/adapter/mongo", allowed: false},
		{name: "adapter may use capability port", location: packageLocation{capability: "adapter", layer: "mongo"}, importPath: modulePath + "/internal/version/port", allowed: true},
		{name: "adapter may use external dependency", location: packageLocation{capability: "adapter", layer: "mongo"}, importPath: "go.mongodb.org/mongo-driver/v2/bson", allowed: true},
		{name: "adapter may not use another adapter", location: packageLocation{capability: "adapter", layer: "mongo"}, importPath: modulePath + "/internal/adapter/auth", allowed: false},
		{name: "non-transport adapter may not use usecase", location: packageLocation{capability: "adapter", layer: "mongo"}, importPath: modulePath + "/internal/version/usecase", allowed: false},
		{name: "transport adapter may use usecase", location: packageLocation{capability: "adapter", layer: "transport"}, importPath: modulePath + "/internal/version/usecase", allowed: true},
		{name: "adapter may not read config", location: packageLocation{capability: "adapter", layer: "mongo"}, importPath: modulePath + "/internal/config", allowed: false},
		{name: "shared may not depend on a capability", location: packageLocation{capability: "shared", layer: "shared"}, importPath: modulePath + "/internal/version/domain", allowed: false},
		{name: "composition root may use adapter", location: packageLocation{capability: "cmd", layer: "composition"}, importPath: modulePath + "/internal/adapter/mongo", allowed: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allowed := importViolation(test.location, test.importPath) == ""
			if allowed != test.allowed {
				t.Fatalf("allowed = %t, want %t", allowed, test.allowed)
			}
		})
	}
}

func TestSourcePathRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path    string
		allowed bool
	}{
		{path: "architecture_test.go", allowed: true},
		{path: "cmd/app-center/main.go", allowed: true},
		{path: "internal/adapter/mongo/repository.go", allowed: true},
		{path: "internal/application/domain/application.go", allowed: true},
		{path: "internal/config/config.go", allowed: true},
		{path: "internal/application/application.go", allowed: false},
		{path: "internal/application/service/application.go", allowed: false},
		{path: "internal/adapter/adapter.go", allowed: false},
		{path: "internal/util/strings.go", allowed: false},
		{path: "cmd/worker/main.go", allowed: false},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			allowed := sourcePathViolation(test.path) == ""
			if allowed != test.allowed {
				t.Fatalf("allowed = %t, want %t", allowed, test.allowed)
			}
		})
	}
}

func TestDomainInfrastructureTagRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		source        string
		wantViolation bool
	}{
		{name: "plain field", source: "package domain\ntype sample struct { Value string }\n", wantViolation: false},
		{name: "unrelated tag", source: "package domain\ntype sample struct { Value string `validate:\"required\"` }\n", wantViolation: false},
		{name: "JSON tag", source: "package domain\ntype sample struct { Value string `json:\"value\"` }\n", wantViolation: true},
		{name: "BSON tag", source: "package domain\ntype sample struct { Value string `bson:\"value\"` }\n", wantViolation: true},
		{name: "Protobuf tag", source: "package domain\ntype sample struct { Value string `protobuf:\"bytes,1,opt\"` }\n", wantViolation: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := parser.ParseFile(token.NewFileSet(), "sample.go", test.source, 0)
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			violations := infrastructureTagViolations("sample.go", parsed)
			if got := len(violations) > 0; got != test.wantViolation {
				t.Fatalf("has violation = %t, want %t; violations = %v", got, test.wantViolation, violations)
			}
		})
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture test")
	}
	return filepath.Dir(filename)
}

func locatePackage(relativePath string) packageLocation {
	parts := strings.Split(relativePath, "/")
	if len(parts) >= 2 && parts[0] == "cmd" {
		return packageLocation{capability: "cmd", layer: "composition"}
	}
	if len(parts) < 3 || parts[0] != "internal" {
		return packageLocation{}
	}
	if parts[1] == "shared" {
		return packageLocation{capability: "shared", layer: "shared"}
	}
	return packageLocation{capability: parts[1], layer: parts[2], packagePath: filepath.ToSlash(filepath.Dir(relativePath))}
}

func sourcePathViolation(relativePath string) string {
	parts := strings.Split(relativePath, "/")
	if len(parts) >= 2 && parts[0] == "cmd" && parts[1] != "app-center" {
		return "composition code must live under cmd/app-center"
	}
	if len(parts) < 2 || parts[0] != "internal" {
		return ""
	}
	if parts[1] == "adapter" {
		if len(parts) < 4 {
			return "adapter Go files must live in a named adapter package"
		}
		return ""
	}
	if parts[1] == "config" || parts[1] == "shared" {
		return ""
	}
	if len(parts) < 4 {
		return "business capability Go files must live in domain, usecase, or port"
	}
	switch parts[2] {
	case "domain", "usecase", "port":
		return ""
	default:
		return "business capability Go files must live in domain, usecase, or port"
	}
}

func importViolation(location packageLocation, importPath string) string {
	if location.layer == "domain" && hasAnyPrefix(importPath, domainInfrastructureImports) {
		return "Domain cannot depend on infrastructure, transport, configuration, or concrete logging"
	}
	if isCoreLayer(location.layer) && isExternalImport(importPath) &&
		!(location.capability == "profile" && location.layer == "domain" && location.packagePath == "internal/profile/domain" && importPath == "golang.org/x/text/unicode/norm") {
		return "Domain, UseCase, Port, and shared code require an accepted architecture change before adding an external dependency"
	}

	internalPrefix := modulePath + "/internal/"
	if !strings.HasPrefix(importPath, internalPrefix) {
		return ""
	}
	internalPackage := strings.TrimPrefix(importPath, internalPrefix)

	if strings.HasPrefix(internalPackage, "adapter/") {
		switch {
		case location.capability == "cmd":
			return ""
		case location.capability != "adapter":
			return "only the composition root may import concrete adapter packages"
		case internalPackage != "adapter/"+location.layer && !strings.HasPrefix(internalPackage, "adapter/"+location.layer+"/"):
			return "one concrete adapter package may not import another adapter package"
		}
	}
	if (internalPackage == "config" || strings.HasPrefix(internalPackage, "config/")) && location.capability != "config" && location.capability != "cmd" {
		return "only config and the composition root may import configuration packages"
	}
	if location.capability == "adapter" && location.layer != "transport" && isCapabilityLayerImport(internalPackage, "usecase") {
		return "only transport adapters may import UseCase packages"
	}

	allowed := map[string]struct{}{}
	switch location.layer {
	case "domain":
		allowed["shared"] = struct{}{}
	case "port":
		allowed["shared"] = struct{}{}
		allowed[location.capability+"/domain"] = struct{}{}
	case "usecase":
		allowed["shared"] = struct{}{}
		allowed[location.capability+"/domain"] = struct{}{}
		allowed[location.capability+"/port"] = struct{}{}
	case "shared":
		// Shared is dependency-leaf code and cannot import a capability.
	default:
		return ""
	}

	for allowedPackage := range allowed {
		if internalPackage == allowedPackage || strings.HasPrefix(internalPackage, allowedPackage+"/") {
			return ""
		}
	}
	return fmt.Sprintf("%s/%s may not import internal package %s", location.capability, location.layer, internalPackage)
}

func isCoreLayer(layer string) bool {
	switch layer {
	case "domain", "usecase", "port", "shared":
		return true
	default:
		return false
	}
}

func isExternalImport(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return strings.Contains(first, ".")
}

func isCapabilityLayerImport(internalPackage string, layer string) bool {
	parts := strings.Split(internalPackage, "/")
	return len(parts) >= 2 && parts[1] == layer
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if value == prefix || strings.HasSuffix(prefix, "/") && strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func infrastructureTagViolations(relativePath string, file *ast.File) []string {
	var violations []string
	ast.Inspect(file, func(node ast.Node) bool {
		field, ok := node.(*ast.Field)
		if !ok || field.Tag == nil {
			return true
		}
		tagText, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s has an unreadable struct tag", relativePath))
			return true
		}
		tag := reflect.StructTag(tagText)
		for _, key := range []string{"bson", "json", "protobuf"} {
			if _, exists := tag.Lookup(key); exists {
				violations = append(violations, fmt.Sprintf("%s has infrastructure tag %q in Domain", relativePath, key))
			}
		}
		return true
	})
	return violations
}

func globalPackageViolations(root string) []string {
	internalRoot := filepath.Join(root, "internal")
	entries, err := os.ReadDir(internalRoot)
	if err != nil {
		return []string{fmt.Sprintf("read internal packages: %v", err)}
	}
	var violations []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, forbidden := forbiddenGlobalPackages[entry.Name()]; forbidden {
			violations = append(violations, fmt.Sprintf("internal/%s is a forbidden global layer package", entry.Name()))
		}
	}
	return violations
}
