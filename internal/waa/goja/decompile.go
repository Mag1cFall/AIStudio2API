package goja

import (
	"strconv"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja/ast"
	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja/token"
)

// decompileExpression 按 SpiderMonkey 表达式反编译规则生成错误消息中的表达式文本
func decompileExpression(expression ast.Expression) string {
	switch node := expression.(type) {
	case *ast.Identifier:
		return node.Name.String()
	case *ast.ThisExpression:
		return "this"
	case *ast.NullLiteral:
		return "null"
	case *ast.BooleanLiteral:
		return strconv.FormatBool(node.Value)
	case *ast.NumberLiteral:
		return numberLiteralText(node)
	case *ast.StringLiteral:
		return strconv.Quote(node.Value.String())
	case *ast.ArrayLiteral:
		if len(node.Value) == 0 {
			return "[]"
		}
	case *ast.UnaryExpression:
		if node.Operator == token.VOID {
			if number, ok := node.Operand.(*ast.NumberLiteral); ok {
				return "(void " + numberLiteralText(number) + ")"
			}
		}
	case *ast.DotExpression:
		return decompileExpression(node.Left) + "." + node.Identifier.Name.String()
	case *ast.BracketExpression:
		return decompileExpression(node.Left) + decompileMember(node.Member)
	case *ast.CallExpression:
		return decompileExpression(node.Callee) + "()"
	case *ast.NewExpression:
		return "(new " + decompileExpression(node.Callee) + "())"
	case *ast.SequenceExpression:
		if len(node.Sequence) > 0 {
			return decompileExpression(node.Sequence[len(node.Sequence)-1])
		}
	case *ast.OptionalChain:
		return decompileExpression(node.Expression)
	case *ast.Optional:
		return decompileExpression(node.Expression)
	case *ast.BinaryExpression:
		if value, ok := foldNumeric(node); ok {
			return formatNumber(value)
		}
	}
	return "(intermediate value)"
}

func decompileMember(member ast.Expression) string {
	switch node := member.(type) {
	case *ast.StringLiteral:
		name := node.Value.String()
		if isIdentifierName(name) {
			return "." + name
		}
		return "['" + strings.ReplaceAll(name, "'", `\'`) + "']"
	case *ast.NumberLiteral:
		return "[" + numberLiteralText(node) + "]"
	case *ast.BinaryExpression:
		if value, ok := foldNumeric(node); ok {
			return "[" + formatNumber(value) + "]"
		}
	}
	return "[" + decompileExpression(member) + "]"
}

func numberLiteralText(node *ast.NumberLiteral) string {
	switch value := node.Value.(type) {
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return formatNumber(value)
	}
	return node.Literal
}

func formatNumber(value float64) string {
	return floatToValue(value).String()
}

func foldNumeric(node *ast.BinaryExpression) (float64, bool) {
	left, ok := numericConstant(node.Left)
	if !ok {
		return 0, false
	}
	right, ok := numericConstant(node.Right)
	if !ok {
		return 0, false
	}
	switch node.Operator {
	case token.PLUS:
		return left + right, true
	case token.MINUS:
		return left - right, true
	case token.MULTIPLY:
		return left * right, true
	}
	return 0, false
}

func numericConstant(expression ast.Expression) (float64, bool) {
	switch node := expression.(type) {
	case *ast.NumberLiteral:
		switch value := node.Value.(type) {
		case int64:
			return float64(value), true
		case float64:
			return value, true
		}
	case *ast.BinaryExpression:
		return foldNumeric(node)
	}
	return 0, false
}
