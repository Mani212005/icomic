package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// PostgreSQL can infer conflicting types for the same untyped placeholder even
// when every bound Go value has the right type. Require one explicit SQL type
// per parameter in the persistence statements, including nullable parameters.
func TestPersistenceSQLParametersHaveExplicitTypes(t *testing.T) {
	files := map[string]map[string]bool{
		"generation.go":          nil,
		"credit_transactions.go": nil,
		"postgres.go":            {"RecordAdjustment": true, "GetCreditBalance": true, "GetUserDailyImageCount": true, "GetUserMonthlyImageCount": true},
	}
	placeholders := regexp.MustCompile(`\$[0-9]+`)
	cast := regexp.MustCompile(`^::([a-zA-Z][a-zA-Z0-9_]*)`)
	for name, functions := range files {
		positions := token.NewFileSet()
		file, err := parser.ParseFile(positions, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || (functions != nil && !functions[fn.Name.Name]) {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				query, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatal(err)
				}
				types := map[string]string{}
				for _, span := range placeholders.FindAllStringIndex(query, -1) {
					parameter := query[span[0]:span[1]]
					explicit := cast.FindStringSubmatch(query[span[1]:])
					if explicit == nil {
						t.Errorf("%s: %s in %s needs an explicit SQL cast", positions.Position(literal.Pos()), parameter, fn.Name.Name)
						continue
					}
					kind := strings.ToLower(explicit[1])
					if previous := types[parameter]; previous != "" && previous != kind {
						t.Errorf("%s: %s has conflicting types %s and %s", positions.Position(literal.Pos()), parameter, previous, kind)
					}
					types[parameter] = kind
				}
				return true
			})
		}
	}
}
