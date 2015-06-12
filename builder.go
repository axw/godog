package godog

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"golang.org/x/tools/imports"
)

type builder struct {
	files    map[string]*ast.File
	fset     *token.FileSet
	Contexts []string
	tpl      *template.Template
}

func newBuilder() *builder {
	return &builder{
		files: make(map[string]*ast.File),
		fset:  token.NewFileSet(),
		tpl: template.Must(template.New("main").Parse(`package main

import (
	"github.com/DATA-DOG/godog"
)

func main() {
	suite := godog.New()
	{{range $c := .Contexts}}
		{{$c}}(suite)
	{{end}}
	suite.Run()
}`)),
	}
}

func (b *builder) parseFile(path string) error {
	f, err := parser.ParseFile(b.fset, path, nil, 0)
	if err != nil {
		return err
	}
	b.deleteMainFunc(f)
	b.registerSteps(f)
	b.deleteImports(f)
	b.files[path] = f
	return nil
}

func (b *builder) deleteImports(f *ast.File) {
	var decls []ast.Decl
	for _, d := range f.Decls {
		fun, ok := d.(*ast.GenDecl)
		if !ok {
			decls = append(decls, d)
			continue
		}
		if fun.Tok != token.IMPORT {
			decls = append(decls, fun)
		}
	}
	f.Decls = decls
}

func (b *builder) deleteMainFunc(f *ast.File) {
	var decls []ast.Decl
	for _, d := range f.Decls {
		fun, ok := d.(*ast.FuncDecl)
		if !ok {
			decls = append(decls, d)
			continue
		}
		if fun.Name.Name != "main" {
			decls = append(decls, fun)
		}
	}
	f.Decls = decls
}

func (b *builder) registerSteps(f *ast.File) {
	for _, d := range f.Decls {
		switch fun := d.(type) {
		case *ast.FuncDecl:
			for _, param := range fun.Type.Params.List {
				switch expr := param.Type.(type) {
				case *ast.SelectorExpr:
					switch x := expr.X.(type) {
					case *ast.Ident:
						if x.Name == "godog" && expr.Sel.Name == "Suite" {
							b.Contexts = append(b.Contexts, fun.Name.Name)
						}
					}
				case *ast.Ident:
					if expr.Name == "Suite" {
						b.Contexts = append(b.Contexts, fun.Name.Name)
					}
				}
			}
		}
	}
}

func (b *builder) merge() (*ast.File, error) {
	var buf bytes.Buffer
	if err := b.tpl.Execute(&buf, b); err != nil {
		return nil, err
	}

	f, err := parser.ParseFile(b.fset, "", &buf, 0)
	if err != nil {
		return nil, err
	}
	b.deleteImports(f)
	b.files["main.go"] = f

	pkg, _ := ast.NewPackage(b.fset, b.files, nil, nil)
	pkg.Name = "main"

	return ast.MergePackageFiles(pkg, ast.FilterImportDuplicates), nil
}

// Build creates a runnable godog executable file
// from current package source and test files
// it merges the files with the help of go/ast into
// a single main package file which has a custom
// main function to run features
func Build() ([]byte, error) {
	b := newBuilder()
	err := filepath.Walk(".", func(path string, file os.FileInfo, err error) error {
		if file.IsDir() && file.Name() != "." {
			return filepath.SkipDir
		}
		if err == nil && strings.HasSuffix(path, ".go") {
			if err := b.parseFile(path); err != nil {
				return err
			}
		}
		return err
	})
	if err != nil {
		return nil, err
	}

	merged, err := b.merge()
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer

	if err := format.Node(&buf, b.fset, merged); err != nil {
		return nil, err
	}

	return imports.Process("", buf.Bytes(), nil)
}