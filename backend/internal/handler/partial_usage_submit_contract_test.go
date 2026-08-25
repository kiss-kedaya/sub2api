package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSemanticStreamFailoverSubmitsPartialUsageBeforeReturning(t *testing.T) {
	tests := []struct {
		name       string
		file       string
		submitCall string
		handleCall string
	}{
		{
			name:       "openai responses",
			file:       "openai_gateway_handler.go",
			submitCall: "submitResponsesUsage",
			handleCall: "handleFailoverExhausted",
		},
		{
			name:       "openai messages",
			file:       "openai_gateway_handler.go",
			submitCall: "submitMessagesUsage",
			handleCall: "handleAnthropicFailoverExhausted",
		},
		{
			name:       "gateway chat completions",
			file:       "gateway_handler_chat_completions.go",
			submitCall: "submitForwardUsage",
			handleCall: "handleCCFailoverExhausted",
		},
		{
			name:       "gateway responses",
			file:       "gateway_handler_responses.go",
			submitCall: "submitForwardUsage",
			handleCall: "handleResponsesFailoverExhausted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, filepath.Join(".", tt.file), nil, 0)
			require.NoError(t, err)

			found := false
			ast.Inspect(file, func(node ast.Node) bool {
				block, ok := node.(*ast.BlockStmt)
				if !ok {
					return true
				}
				for i := 1; i < len(block.List); i++ {
					if statementCalls(block.List[i-1], tt.submitCall) && statementCalls(block.List[i], tt.handleCall) {
						found = true
						return false
					}
				}
				return true
			})

			require.True(t, found,
				"semantic-output failover must submit its partial usage immediately before the terminal handler")
		})
	}
}

func statementCalls(statement ast.Stmt, name string) bool {
	found := false
	ast.Inspect(statement, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			found = fn.Name == name
		case *ast.SelectorExpr:
			found = fn.Sel.Name == name
		}
		return !found
	})
	return found
}
