// The writer guard is a test so it composes with the existing scripts package
// without adding a second package-level main function.
package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Scan all production packages rather than only the current operation entry
// points. Storage bootstrap and instance-lock code use dedicated raw handles;
// the guard below only permits those through explicit file-local exceptions.
var productionRoots = []string{"internal"}

func TestWriterGuard(t *testing.T) {
	set := token.NewFileSet()
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "internal", "app")); err != nil {
		repoRoot = filepath.Dir(repoRoot)
	}
	for _, relativeRoot := range productionRoots {
		root := filepath.Join(repoRoot, relativeRoot)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(set, path, nil, 0)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			ast.Inspect(file, func(node ast.Node) bool {
				selector, selectorOK := node.(*ast.SelectorExpr)
				if selectorOK {
					_, nestedSelector := selector.X.(*ast.SelectorExpr)
					if nestedSelector && selector.Sel.Name == "DB" && filepath.Clean(path) != filepath.Join(repoRoot, "internal", "storage", "store.go") {
						t.Errorf("%s:%d: production code accesses the embedded raw database handle directly; use storage DB read/write APIs", path, set.Position(selector.Pos()).Line)
						return true
					}
				}
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok = call.Fun.(*ast.SelectorExpr)
				if !ok || (selector.Sel.Name != "Exec" && selector.Sel.Name != "ExecContext" &&
					selector.Sel.Name != "Begin" && selector.Sel.Name != "BeginTx") {
					return true
				}
				receiver := expressionText(set, selector.X)
				if strings.HasSuffix(receiver, ".DB") && filepath.Clean(path) != filepath.Join(repoRoot, "internal", "storage", "store.go") {
					t.Errorf("%s:%d: %s bypasses storage writer through embedded DB", path, set.Position(call.Pos()).Line, selector.Sel.Name)
					return true
				}
				if strings.Contains(receiver, "ReadDB()") {
					if selector.Sel.Name == "BeginTx" && path == filepath.Join(repoRoot, "internal", "knowledge", "search.go") {
						return true
					}
					t.Errorf("%s:%d: %s on read handle", path, set.Position(call.Pos()).Line, selector.Sel.Name)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestOperationHandlerGuard(t *testing.T) {
	set := token.NewFileSet()
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "internal", "web")); err != nil {
		repoRoot = filepath.Dir(repoRoot)
	}
	path := filepath.Join(repoRoot, "internal", "web", "api.go")
	file, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// These handlers are receipt-producing control-plane entry points. Any
	// physical/model operation here would make a 202 response imply work has
	// already completed and would bypass the durable executor boundary.
	guarded := map[string]bool{
		"restoreBase": true, "addDocument": true, "addFiles": true,
		"reindexBase": true, "addURLDocument": true, "importDirectory": true,
		"rescanDirectory": true, "refreshDocument": true, "deleteDocument": true,
		"deleteDocumentJob": true, "deleteDocumentTree": true,
		"deleteDocumentTreeJob": true, "deleteDocuments": true,
		"reindexDocuments": true, "reindexOne": true, "downloadOCRModel": true,
		"removeOCRModel": true, "selfTestLocalReranker": true,
		"downloadLocalModel": true, "removeLocalModel": true,
		"migrateModelCache": true, "pullOllamaModel": true,
		"deleteOllamaModel": true,
	}
	forbidden := map[string]bool{
		"AddTextDocumentWithID": true, "AddFileDocumentWithID": true,
		"AddFiles": true, "RunDirectoryImport": true, "RunDirectoryRescan": true,
		"ReindexDocument": true, "ReindexBase": true, "DeleteDocument": true,
		"DeleteDocumentWithProgress": true, "DeleteDirectoryRecursiveWithProgress": true,
		"DeleteBase": true, "Download": true, "DownloadOCR": true,
		"Remove": true, "Pull": true, "Delete": true,
		"PlanModelCacheMigration": true, "MigrateModelCache": true,
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || !guarded[function.Name.Name] {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && forbidden[selector.Sel.Name] {
				t.Errorf("%s:%d: handler %s directly calls forbidden executor method %s", path,
					set.Position(call.Pos()).Line, function.Name.Name, selector.Sel.Name)
			}
			return true
		})
	}
}

func TestGenericOperationHandlerGuard(t *testing.T) {
	set := token.NewFileSet()
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "internal", "web")); err != nil {
		repoRoot = filepath.Dir(repoRoot)
	}
	path := filepath.Join(repoRoot, "internal", "web", "operations.go")
	file, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{
		"PlanModelCacheMigration": true, "MigrateModelCache": true,
		"Download": true, "DownloadOCR": true, "Remove": true,
		"Pull": true, "Delete": true, "AddTextDocumentWithID": true,
		"AddFileDocumentWithID": true, "RunDirectoryImport": true,
		"RunDirectoryRescan": true, "ReindexDocument": true,
		"DeleteDocument": true, "DeleteBase": true,
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "submitOperation" || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && forbidden[selector.Sel.Name] {
				t.Errorf("%s:%d: generic operation handler directly calls forbidden executor method %s",
					path, set.Position(call.Pos()).Line, selector.Sel.Name)
			}
			return true
		})
	}
}

func expressionText(set *token.FileSet, expression ast.Expr) string {
	var builder strings.Builder
	if err := format.Node(&builder, set, expression); err != nil {
		return ""
	}
	return builder.String()
}
