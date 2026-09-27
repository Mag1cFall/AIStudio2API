package goja

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja/ast"
	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja/file"
	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja/token"
	"github.com/Mag1cFall/AIStudio2API/internal/waa/goja/unistring"
)

// functionNameResolver 按 SpiderMonkey NameFunctions 规则推断匿名函数的栈显示名
type functionNameResolver struct {
	parents []ast.Node
	prefix  string
	names   map[file.Idx]unistring.String
	source  string
	base    int
}

var (
	astNodeType       = reflect.TypeOf((*ast.Node)(nil)).Elem()
	astIdentifierType = reflect.TypeOf(ast.Identifier{})
)

// guessFunctionNames 返回以函数起始位置为键的推断显示名
func guessFunctionNames(root ast.Node) map[file.Idx]unistring.String {
	resolver := &functionNameResolver{names: make(map[file.Idx]unistring.String)}
	if program, ok := root.(*ast.Program); ok && program.File != nil {
		resolver.source, resolver.base = program.File.Source(), program.File.Base()
	}
	resolver.visit(root)
	return resolver.names
}

func (r *functionNameResolver) visit(node ast.Node) {
	if node == nil {
		return
	}
	value := reflect.ValueOf(node)
	if value.Kind() == reflect.Ptr && value.IsNil() {
		return
	}
	switch node.(type) {
	case *ast.FunctionLiteral, *ast.ArrowFunctionLiteral:
		saved := r.prefix
		name := r.resolve(node)
		if len(r.parents) == 0 || !r.isDirectCall(len(r.parents)-1, node) {
			r.prefix = name
		}
		r.parents = append(r.parents, node)
		r.visitFields(value)
		r.parents = r.parents[:len(r.parents)-1]
		r.prefix = saved
		return
	}
	r.parents = append(r.parents, node)
	r.visitFields(value)
	r.parents = r.parents[:len(r.parents)-1]
}

func (r *functionNameResolver) visitFields(value reflect.Value) {
	for value.Kind() == reflect.Ptr || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return
	}
	valueType := value.Type()
	for index := 0; index < value.NumField(); index++ {
		field := valueType.Field(index)
		if !field.IsExported() || field.Name == "DeclarationList" || field.Name == "File" {
			continue
		}
		r.visitValue(value.Field(index))
	}
}

func (r *functionNameResolver) visitValue(value reflect.Value) {
	switch value.Kind() {
	case reflect.Interface, reflect.Ptr:
		if value.IsNil() {
			return
		}
		if node, ok := value.Interface().(ast.Node); ok {
			r.visit(node)
			return
		}
		r.visitFields(value)
	case reflect.Slice:
		for index := 0; index < value.Len(); index++ {
			r.visitValue(value.Index(index))
		}
	case reflect.Struct:
		if value.Type() == astIdentifierType {
			return
		}
		if value.CanAddr() {
			if node, ok := value.Addr().Interface().(ast.Node); ok {
				r.visit(node)
				return
			}
		}
		r.visitFields(value)
	}
}

// resolve 计算函数的显示名并返回其子函数使用的前缀
func (r *functionNameResolver) resolve(function ast.Node) string {
	var buf strings.Builder
	if name := r.explicitName(function); name != "" {
		if r.prefix == "" {
			return name
		}
		return r.prefix + "/" + name
	}
	if r.prefix != "" {
		buf.WriteString(r.prefix)
		buf.WriteByte('/')
	}
	nameable, assignment := r.gatherNameable()
	if assignment != nil {
		if !r.nameExpression(assignment, &buf) {
			return ""
		}
	}
	for index := len(nameable) - 1; index >= 0; index-- {
		switch node := nameable[index].(type) {
		case *ast.PropertyKeyed:
			if node.Computed {
				continue
			}
			switch key := node.Key.(type) {
			case *ast.StringLiteral:
				appendPropertyReference(&buf, key.Value.String())
			case *ast.NumberLiteral:
				buf.WriteByte('[')
				buf.WriteString(numberLiteralString(key))
				buf.WriteByte(']')
			case *ast.Identifier:
				appendPropertyReference(&buf, key.Name.String())
			}
		case *ast.PropertyShort:
			appendPropertyReference(&buf, node.Name.Name.String())
		default:
			if buf.Len() > 0 && !strings.HasSuffix(buf.String(), "<") {
				buf.WriteByte('<')
			}
		}
	}
	if buf.Len() > 0 && strings.HasSuffix(buf.String(), "/") {
		buf.WriteByte('<')
	}
	name := buf.String()
	if name != "" {
		switch function := function.(type) {
		case *ast.FunctionLiteral:
			r.names[function.Idx0()] = unistring.NewFromString(name)
		case *ast.ArrowFunctionLiteral:
			r.names[function.Idx0()] = unistring.NewFromString(name)
		}
	}
	return name
}

// explicitName 返回函数声明名或 ES NamedEvaluation 名
func (r *functionNameResolver) explicitName(function ast.Node) string {
	if literal, ok := function.(*ast.FunctionLiteral); ok && literal.Name != nil {
		return literal.Name.Name.String()
	}
	if len(r.parents) == 0 {
		return ""
	}
	switch parent := r.parents[len(r.parents)-1].(type) {
	case *ast.Binding:
		if identifier, ok := parent.Target.(*ast.Identifier); ok && parent.Initializer == function {
			return identifier.Name.String()
		}
	case *ast.AssignExpression:
		if identifier, ok := parent.Left.(*ast.Identifier); ok && parent.Right == function && parent.Operator == token.ASSIGN {
			return identifier.Name.String()
		}
	case *ast.PropertyKeyed:
		if !parent.Computed && parent.Value == function {
			switch key := parent.Key.(type) {
			case *ast.StringLiteral:
				return key.Value.String()
			case *ast.Identifier:
				return key.Name.String()
			case *ast.NumberLiteral:
				return numberLiteralString(key)
			}
		}
	case *ast.MethodDefinition:
		if !parent.Computed {
			if key, ok := parent.Key.(*ast.StringLiteral); ok {
				return key.Value.String()
			}
		}
	}
	return ""
}

// gatherNameable 自内向外收集参与命名的父节点，并返回赋值目标
func (r *functionNameResolver) gatherNameable() ([]ast.Node, ast.Node) {
	nameable := make([]ast.Node, 0)
	for pos := len(r.parents) - 1; pos >= 0; pos-- {
		current := r.parents[pos]
		switch node := current.(type) {
		case *ast.AssignExpression:
			return nameable, node.Left
		case *ast.Binding:
			return nameable, node.Target
		case *ast.FunctionLiteral, *ast.ArrowFunctionLiteral:
			return nameable, nil
		case *ast.ReturnStatement:
			for tmp := pos - 1; tmp > 0; tmp-- {
				if r.isDirectCall(tmp, current) {
					pos = tmp
					break
				}
				if _, isCall := current.(*ast.CallExpression); isCall {
					break
				}
				current = r.parents[tmp]
			}
		case *ast.PropertyKeyed, *ast.PropertyShort:
			nameable = append(nameable, current)
			pos--
		default:
			nameable = append(nameable, current)
		}
	}
	return nameable, nil
}

// isDirectCall 判断 node 是否为不贡献命名前缀的立即调用被调者，仅在无前缀的上下文中成立且被调者须带括号
func (r *functionNameResolver) isDirectCall(pos int, node ast.Node) bool {
	call, ok := r.parents[pos].(*ast.CallExpression)
	return ok && r.prefix == "" && call.Callee == node && r.parenthesized(node)
}

// parenthesized 判断节点源码起始位置之前是否紧邻左括号
func (r *functionNameResolver) parenthesized(node ast.Node) bool {
	offset := int(node.Idx0()) - r.base
	for offset--; offset >= 0 && offset < len(r.source); offset-- {
		switch r.source[offset] {
		case ' ', '\t', '\n', '\r':
			continue
		case '(':
			return true
		}
		return false
	}
	return false
}

// nameExpression 把赋值目标写成 a.b[0] 形式，无法命名时返回 false
func (r *functionNameResolver) nameExpression(node ast.Node, buf *strings.Builder) bool {
	switch expression := node.(type) {
	case *ast.DotExpression:
		if !r.nameExpression(expression.Left, buf) {
			return false
		}
		appendPropertyReference(buf, expression.Identifier.Name.String())
		return true
	case *ast.Identifier:
		buf.WriteString(expression.Name.String())
		return true
	case *ast.ThisExpression:
		buf.WriteString("this")
		return true
	case *ast.BracketExpression:
		if key, ok := expression.Member.(*ast.StringLiteral); ok && isIdentifierName(key.Value.String()) {
			if !r.nameExpression(expression.Left, buf) {
				return false
			}
			appendPropertyReference(buf, key.Value.String())
			return true
		}
		if !r.nameExpression(expression.Left, buf) {
			return false
		}
		buf.WriteByte('[')
		if !r.nameExpression(expression.Member, buf) {
			return false
		}
		buf.WriteByte(']')
		return true
	case *ast.NumberLiteral:
		buf.WriteString(numberLiteralString(expression))
		return true
	}
	return false
}

func appendPropertyReference(buf *strings.Builder, name string) {
	if isIdentifierName(name) {
		buf.WriteByte('.')
		buf.WriteString(name)
		return
	}
	buf.WriteByte('[')
	buf.WriteString(strconv.Quote(name))
	buf.WriteByte(']')
}

func numberLiteralString(literal *ast.NumberLiteral) string {
	switch value := literal.Value.(type) {
	case int64:
		return strconv.FormatInt(value, 10)
	case float64:
		return floatToValue(value).String()
	}
	return literal.Literal
}

func isIdentifierName(name string) bool {
	if name == "" {
		return false
	}
	for index, character := range name {
		if character == '$' || character == '_' || character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character > 0x7f {
			continue
		}
		if index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}
