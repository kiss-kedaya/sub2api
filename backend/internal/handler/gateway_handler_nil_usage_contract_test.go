package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGatewayMessagesUsageSubmitIgnoresNilForwardResult(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "gateway_handler.go", nil, 0)
	require.NoError(t, err)

	guarded := false
	ast.Inspect(file, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		name, ok := assign.Lhs[0].(*ast.Ident)
		if !ok || name.Name != "submitForwardUsage" {
			return true
		}
		fn, ok := assign.Rhs[0].(*ast.FuncLit)
		if !ok || len(fn.Body.List) == 0 {
			return false
		}
		guard, ok := fn.Body.List[0].(*ast.IfStmt)
		if !ok || guard.Else != nil || !isNilForwardResultCheck(guard.Cond) {
			return false
		}
		guarded = len(guard.Body.List) == 1 && isBareReturn(guard.Body.List[0])
		return false
	})

	require.True(t, guarded, "submitForwardUsage must return before dereferencing a nil ForwardResult")
}

func isNilForwardResultCheck(expr ast.Expr) bool {
	comparison, ok := expr.(*ast.BinaryExpr)
	if !ok || comparison.Op != token.EQL {
		return false
	}
	left, leftOK := comparison.X.(*ast.Ident)
	right, rightOK := comparison.Y.(*ast.Ident)
	return leftOK && rightOK && left.Name == "result" && right.Name == "nil"
}

func isBareReturn(statement ast.Stmt) bool {
	ret, ok := statement.(*ast.ReturnStmt)
	return ok && len(ret.Results) == 0
}
