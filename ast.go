package gox

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"log"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"github.com/goplus/gox/internal"
)

// ----------------------------------------------------------------------------

var (
	underscore = &ast.Ident{Name: "_"}
)

var (
	identTrue   = ident("true")
	identFalse  = ident("false")
	identNil    = ident("nil")
	identAppend = ident("append")
	identLen    = ident("len")
	identCap    = ident("cap")
	identNew    = ident("new")
	identMake   = ident("make")
	identIota   = ident("iota")
)

func ident(name string) *ast.Ident {
	return &ast.Ident{Name: name}
}

func boolean(v bool) *ast.Ident {
	if v {
		return identTrue
	}
	return identFalse
}

func toRecv(pkg *Package, recv *types.Var) *ast.FieldList {
	var names []*ast.Ident
	if name := recv.Name(); name != "" {
		names = []*ast.Ident{ident(name)}
	}
	fld := &ast.Field{Names: names, Type: toType(pkg, recv.Type())}
	return &ast.FieldList{List: []*ast.Field{fld}}
}

// -----------------------------------------------------------------------------
// function type

func toFieldList(pkg *Package, t *types.Tuple) []*ast.Field {
	if t == nil {
		return nil
	}
	n := t.Len()
	flds := make([]*ast.Field, n)
	for i := 0; i < n; i++ {
		item := t.At(i)
		var names []*ast.Ident
		if name := item.Name(); name != "" {
			names = []*ast.Ident{ident(name)}
		}
		typ := toType(pkg, item.Type())
		flds[i] = &ast.Field{Names: names, Type: typ}
	}
	return flds
}

func toFields(pkg *Package, t *types.Struct) []*ast.Field {
	n := t.NumFields()
	flds := make([]*ast.Field, n)
	for i := 0; i < n; i++ {
		item := t.Field(i)
		var names []*ast.Ident
		if !item.Embedded() {
			names = []*ast.Ident{{Name: item.Name()}}
		}
		typ := toType(pkg, item.Type())
		fld := &ast.Field{Names: names, Type: typ}
		if tag := t.Tag(i); tag != "" {
			fld.Tag = &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(tag)}
		}
		flds[i] = fld
	}
	return flds
}

func toVariadic(fld *ast.Field) {
	t, ok := fld.Type.(*ast.ArrayType)
	if !ok || t.Len != nil {
		panic("TODO: not a slice type")
	}
	fld.Type = &ast.Ellipsis{Elt: t.Elt}
}

func toFuncType(pkg *Package, sig *types.Signature) *ast.FuncType {
	params := toFieldList(pkg, sig.Params())
	results := toFieldList(pkg, sig.Results())
	if sig.Variadic() {
		n := len(params)
		if n == 0 {
			panic("TODO: toFuncType error")
		}
		toVariadic(params[n-1])
	}
	return &ast.FuncType{
		Params:  &ast.FieldList{List: params},
		Results: &ast.FieldList{List: results},
	}
}

// -----------------------------------------------------------------------------

func toType(pkg *Package, typ types.Type) ast.Expr {
retry:
	switch t := typ.(type) {
	case *types.Basic: // bool, int, etc
		return toBasicType(pkg, t)
	case *types.Pointer:
		return &ast.StarExpr{X: toType(pkg, t.Elem())}
	case *types.Named:
		return toNamedType(pkg, t)
	case *types.Interface:
		return toInterface(pkg, t)
	case *types.Slice:
		return toSliceType(pkg, t)
	case *types.Array:
		return toArrayType(pkg, t)
	case *types.Map:
		return toMapType(pkg, t)
	case *types.Struct:
		return toStructType(pkg, t)
	case *types.Chan:
		return toChanType(pkg, t)
	case *types.Signature:
		return toFuncType(pkg, t)
	case *unboundType:
		if t.tBound == nil {
			panic("TODO: unbound type")
		}
		typ = t.tBound
		goto retry
	}
	log.Panicln("TODO: toType -", reflect.TypeOf(typ))
	return nil
}

func toBasicType(pkg *Package, t *types.Basic) ast.Expr {
	if t.Kind() == types.UnsafePointer {
		return toObjectExpr(pkg, pkg.unsafe().Ref("Pointer"))
	}
	if (t.Info() & types.IsUntyped) != 0 {
		panic("unexpected: untyped type")
	}
	return &ast.Ident{Name: t.Name()}
}

func isUntyped(pkg *Package, typ types.Type) bool {
	switch t := typ.(type) {
	case *types.Basic:
		return (t.Info() & types.IsUntyped) != 0
	case *types.Named:
		switch t {
		case pkg.utBigInt, pkg.utBigRat, pkg.utBigFlt:
			return true
		}
	}
	return false
}

func toNamedType(pkg *Package, t *types.Named) ast.Expr {
	return toObjectExpr(pkg, t.Obj())
}

func toChanType(pkg *Package, t *types.Chan) ast.Expr {
	return &ast.ChanType{Value: toType(pkg, t.Elem()), Dir: chanDirs[t.Dir()]}
}

var (
	chanDirs = [...]ast.ChanDir{
		types.SendRecv: ast.SEND | ast.RECV,
		types.SendOnly: ast.SEND,
		types.RecvOnly: ast.RECV,
	}
)

func toStructType(pkg *Package, t *types.Struct) ast.Expr {
	list := toFields(pkg, t)
	return &ast.StructType{Fields: &ast.FieldList{List: list}}
}

func toArrayType(pkg *Package, t *types.Array) ast.Expr {
	var len ast.Expr
	if n := t.Len(); n < 0 {
		len = &ast.Ellipsis{}
	} else {
		len = &ast.BasicLit{Kind: token.INT, Value: strconv.FormatInt(t.Len(), 10)}
	}
	return &ast.ArrayType{Len: len, Elt: toType(pkg, t.Elem())}
}

func toSliceType(pkg *Package, t *types.Slice) ast.Expr {
	return &ast.ArrayType{Elt: toType(pkg, t.Elem())}
}

func toMapType(pkg *Package, t *types.Map) ast.Expr {
	return &ast.MapType{Key: toType(pkg, t.Key()), Value: toType(pkg, t.Elem())}
}

func toInterface(pkg *Package, t *types.Interface) ast.Expr {
	var flds []*ast.Field
	for i, n := 0, t.NumEmbeddeds(); i < n; i++ {
		typ := toType(pkg, t.EmbeddedType(i))
		fld := &ast.Field{Type: typ}
		flds = append(flds, fld)
	}
	for i, n := 0, t.NumExplicitMethods(); i < n; i++ {
		fn := t.ExplicitMethod(i)
		name := ident(fn.Name())
		typ := toFuncType(pkg, fn.Type().(*types.Signature))
		fld := &ast.Field{Names: []*ast.Ident{name}, Type: typ}
		flds = append(flds, fld)
	}
	return &ast.InterfaceType{Methods: &ast.FieldList{List: flds}}
}

// -----------------------------------------------------------------------------
// expression

func toExpr(pkg *Package, val interface{}, src ast.Node) *internal.Elem {
	if val == nil {
		return &internal.Elem{
			Val:  identNil,
			Type: types.Typ[types.UntypedNil],
			Src:  src,
		}
	}
	switch v := val.(type) {
	case *ast.BasicLit:
		return &internal.Elem{
			Val:  v,
			Type: types.Typ[toBasicKind(v.Kind)],
			CVal: constant.MakeFromLiteral(v.Value, v.Kind, 0),
			Src:  src,
		}
	case *types.TypeName:
		if typ := v.Type(); isType(typ) {
			return &internal.Elem{
				Val: toType(pkg, typ), Type: NewTypeType(typ), Src: src,
			}
		} else {
			return toObject(pkg, v, src)
		}
	case *types.Builtin:
		if o := pkg.builtin.Scope().Lookup(v.Name()); o != nil {
			return toObject(pkg, o, src)
		}
		log.Panicln("TODO: unsupported builtin -", v.Name())
	case types.Object:
		if v == iotaObj {
			v := pkg.cb.iotav
			return &internal.Elem{
				Val:  identIota,
				Type: types.Typ[types.UntypedInt],
				CVal: constant.MakeInt64(int64(v)),
				Src:  src,
			}
		}
		return toObject(pkg, v, src)
	case *Element:
		return v
	case int:
		return &internal.Elem{
			Val:  &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(v)},
			Type: types.Typ[types.UntypedInt],
			CVal: constant.MakeInt64(int64(v)),
			Src:  src,
		}
	case string:
		return &internal.Elem{
			Val:  &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(v)},
			Type: types.Typ[types.UntypedString],
			CVal: constant.MakeString(v),
			Src:  src,
		}
	case bool:
		return &internal.Elem{
			Val:  boolean(v),
			Type: types.Typ[types.UntypedBool],
			CVal: constant.MakeBool(v),
			Src:  src,
		}
	case rune:
		return &internal.Elem{
			Val:  &ast.BasicLit{Kind: token.CHAR, Value: strconv.QuoteRune(v)},
			Type: types.Typ[types.UntypedRune],
			CVal: constant.MakeInt64(int64(v)),
			Src:  src,
		}
	case float64:
		val := strconv.FormatFloat(v, 'g', -1, 64)
		if !strings.ContainsAny(val, ".e") {
			val += ".0"
		}
		return &internal.Elem{
			Val:  &ast.BasicLit{Kind: token.FLOAT, Value: val},
			Type: types.Typ[types.UntypedFloat],
			CVal: constant.MakeFloat64(v),
			Src:  src,
		}
	}
	panic("unexpected: unsupport value type")
}

var (
	iotaObj = types.Universe.Lookup("iota")
)

func toBasicKind(tok token.Token) types.BasicKind {
	return tok2BasicKinds[tok]
}

var (
	tok2BasicKinds = [...]types.BasicKind{
		token.INT:    types.UntypedInt,
		token.STRING: types.UntypedString,
		token.CHAR:   types.UntypedRune,
		token.FLOAT:  types.UntypedFloat,
		token.IMAG:   types.UntypedComplex,
	}
)

func toObject(pkg *Package, v types.Object, src ast.Node) *internal.Elem {
	var cval constant.Value
	if cv, ok := v.(*types.Const); ok {
		cval = cv.Val()
	}
	return &internal.Elem{
		Val: toObjectExpr(pkg, v), Type: realType(v.Type()), CVal: cval, Src: src,
	}
}

func toObjectExpr(pkg *Package, v types.Object) ast.Expr {
	atPkg, name := v.Pkg(), v.Name()
	if atPkg == nil || atPkg == pkg.Types { // at universe or at this package
		return ident(name)
	}
	if atPkg == pkg.builtin { // at builtin package
		if strings.HasPrefix(name, goxPrefix) {
			opName := name[len(goxPrefix):]
			if op, ok := nameToOps[opName]; ok {
				switch op.Arity {
				case 2:
					return &ast.BinaryExpr{Op: op.Tok}
				case 1:
					return &ast.UnaryExpr{Op: op.Tok}
				}
			}
		}
		return ident(name)
	}
	importPkg := pkg.Import(atPkg.Path())
	importPkg.EnsureImported()
	x := ident(atPkg.Name())
	importPkg.nameRefs = append(importPkg.nameRefs, x)
	return &ast.SelectorExpr{
		X:   x,
		Sel: ident(v.Name()),
	}
}

type operator struct {
	Tok   token.Token
	Arity int
}

var (
	nameToOps = map[string]operator{
		"Add":    {token.ADD, 2},
		"Sub":    {token.SUB, 2},
		"Mul":    {token.MUL, 2},
		"Quo":    {token.QUO, 2},
		"Rem":    {token.REM, 2},
		"Or":     {token.OR, 2},
		"Xor":    {token.XOR, 2},
		"And":    {token.AND, 2},
		"AndNot": {token.AND_NOT, 2},

		"LOr":  {token.LOR, 2},
		"LAnd": {token.LAND, 2},

		"Lsh": {token.SHL, 2},
		"Rsh": {token.SHR, 2},

		"LT": {token.LSS, 2},
		"LE": {token.LEQ, 2},
		"GT": {token.GTR, 2},
		"GE": {token.GEQ, 2},
		"EQ": {token.EQL, 2},
		"NE": {token.NEQ, 2},

		"Neg":  {token.SUB, 1},
		"Pos":  {token.ADD, 1},
		"Not":  {token.XOR, 1},
		"LNot": {token.NOT, 1},
		"Recv": {token.ARROW, 1},
		"Addr": {token.AND, 1},
	}
)

func toFuncCall(pkg *Package, fn *internal.Elem, args []*internal.Elem, flags InstrFlags) *internal.Elem {
	ret, err := matchFuncCall(pkg, fn, args, flags)
	if err != nil {
		panic(err)
	}
	return ret
}

func unaryOp(tok token.Token, args []*internal.Elem) constant.Value {
	if len(args) == 1 {
		if a := args[0].CVal; a != nil {
			return constant.UnaryOp(tok, a, 0) // TODO: prec should not be 0
		}
	}
	return nil
}

func binaryOp(cb *CodeBuilder, tok token.Token, args []*internal.Elem) constant.Value {
	if len(args) == 2 {
		if a, b := args[0].CVal, args[1].CVal; a != nil && b != nil {
			if tok == token.QUO && isNormalInt(cb, args[0]) && isNormalInt(cb, args[1]) {
				tok = token.QUO_ASSIGN // issue #805
			}
			return doBinaryOp(a, tok, b)
		}
	}
	return nil
}

func isNormalInt(cb *CodeBuilder, arg *internal.Elem) bool { // is integer but not bigint
	argType := arg.Type
retry:
	switch t := argType.(type) {
	case *types.Basic:
		return (t.Info() & types.IsInteger) != 0
	case *types.Named:
		argType = cb.getUnderlying(t)
		goto retry
	}
	return false
}

func doBinaryOp(a constant.Value, tok token.Token, b constant.Value) constant.Value {
	switch binaryOpKinds[tok] {
	case binaryOpNormal:
		return constant.BinaryOp(a, tok, b)
	case binaryOpCompare:
		return constant.MakeBool(constant.Compare(a, tok, b))
	default:
		a, b = constant.ToInt(a), constant.ToInt(b)
		if s, exact := constant.Int64Val(b); exact {
			return constant.Shift(a, tok, uint(s))
		}
		panic("constant value is overflow")
	}
}

const (
	binaryOpNormal = iota
	binaryOpCompare
	binaryOpShift
)

var (
	binaryOpKinds = [...]int{
		token.ADD: 0, // +
		token.SUB: 0, // -
		token.MUL: 0, // *
		token.QUO: 0, // /
		token.REM: 0, // %

		token.AND:     0,             // &
		token.OR:      0,             // |
		token.XOR:     0,             // ^
		token.AND_NOT: 0,             // &^
		token.SHL:     binaryOpShift, // <<
		token.SHR:     binaryOpShift, // >>

		token.LAND: 0, // &&
		token.LOR:  0, // ||

		token.LSS: binaryOpCompare,
		token.LEQ: binaryOpCompare,
		token.GTR: binaryOpCompare,
		token.GEQ: binaryOpCompare,
		token.EQL: binaryOpCompare,
		token.NEQ: binaryOpCompare,
	}
)

func getParamLen(sig *types.Signature) int {
	n := sig.Params().Len()
	if sig.Recv() != nil {
		n++
	}
	return n
}

func getParam(sig *types.Signature, i int) *types.Var {
	if sig.Recv() != nil {
		i--
	}
	if i < 0 {
		return sig.Recv()
	}
	return sig.Params().At(i)
}

func getParam1st(sig *types.Signature) int {
	if sig.Recv() != nil {
		return 1
	}
	return 0
}

func matchFuncCall(pkg *Package, fn *internal.Elem, args []*internal.Elem, flags InstrFlags) (ret *internal.Elem, err error) {
	fnType := fn.Type
	if debugMatch {
		var funcs []types.Object
		var ok bool
		if t, ok1 := fnType.(*types.Signature); ok1 {
			funcs, ok = CheckOverloadMethod(t)
		}
		if ok {
			log.Println("==> MatchFuncCall", funcs)
		} else {
			log.Println("==> MatchFuncCall", fnType)
		}
	}
	var it *instantiated
	var sig *types.Signature
	var cval constant.Value
retry:
	switch t := fnType.(type) {
	case *types.Signature:
		if funcs, ok := CheckOverloadMethod(t); ok {
			backup := backupArgs(args)
			for _, o := range funcs {
				mfn := *fn
				mfn.Val.(*ast.SelectorExpr).Sel = ident(o.Name())
				mfn.Type = methodTypeOf(o.Type())
				if ret, err = matchFuncCall(pkg, &mfn, args, flags); err == nil {
					fn.Val, fn.Type = mfn.Val, mfn.Type
					return
				}
				restoreArgs(args, backup)
			}
			return
		} else {
			sig = t
		}
	case *TypeType: // type convert
		valArgs := make([]ast.Expr, len(args))
		for i, v := range args { // TODO: type check
			valArgs[i] = v.Val
		}
		fnVal := fn.Val
		switch t.typ.(type) {
		case *types.Pointer, *types.Chan:
			fnVal = &ast.ParenExpr{X: fnVal}
		}
		ret = &internal.Elem{
			Val:  &ast.CallExpr{Fun: fnVal, Args: valArgs, Ellipsis: flags & InstrFlagEllipsis},
			Type: t.typ,
		}
		if len(args) == 1 { // TODO: const value may changed by type-convert
			ret.CVal = args[0].CVal
		}
		return
	case *TemplateSignature: // template function
		sig, it = t.instantiate()
		if t.isUnaryOp() {
			cval = unaryOp(t.tok(), args)
		} else if t.isOp() {
			cval = binaryOp(&pkg.cb, t.tok(), args)
		} else if t.hasApproxType() {
			flags |= instrFlagApproxType
		}
	case *overloadFuncType:
		backup := backupArgs(args)
		for _, o := range t.funcs {
			if ret, err = matchFuncCall(pkg, toObject(pkg, o, fn.Src), args, flags); err == nil {
				if ret.CVal == nil && isUntyped(pkg, ret.Type) {
					ret.CVal = builtinCall(fn, args)
				}
				return
			}
			restoreArgs(args, backup)
		}
		return
	case *templateRecvMethodType:
		if mth, ok := fn.Val.(*ast.SelectorExpr); ok {
			if recv := denoteRecv(mth); recv != nil {
				backup := backupArgs(args)
				for i := 0; i < 2; i++ {
					tfn := toObject(pkg, t.fn, nil)
					targs := make([]*internal.Elem, len(args)+1)
					targ0 := *recv
					if i == 1 {
						targ0.Val = &ast.UnaryExpr{Op: token.AND, X: targ0.Val}
						targ0.Type = types.NewPointer(targ0.Type)
					}
					targs[0] = &targ0
					for j, arg := range args {
						targs[j+1] = arg
					}
					if ret, err = matchFuncCall(pkg, tfn, targs, flags); err == nil {
						return
					}
					if isPointer(targ0.Type) {
						break
					}
					restoreArgs(args, backup)
				}
			}
		}
		fatal("TODO: unmatched templateRecvMethodType")
	case *instructionType:
		return t.instr.Call(pkg, args, flags, fn.Src)
	case *types.Named:
		fnType = pkg.cb.getUnderlying(t)
		goto retry
	default:
		src, pos := pkg.cb.loadExpr(fn.Src)
		pkg.cb.panicCodeErrorf(&pos, "cannot call non-function %s (type %v)", src, fn.Type)
	}
	if err = matchFuncType(pkg, args, flags, sig, fn); err != nil {
		return
	}
	tyRet := toRetType(sig.Results(), it)
	if cval != nil { // untyped bigint/bigrat
		if ret, ok := untypeBig(pkg, cval, tyRet); ok {
			pkg.files[pkg.testingFile].removedExprs = true
			return ret, nil
		}
	}
	switch t := fn.Val.(type) {
	case *ast.BinaryExpr:
		t.X, t.Y = checkParenExpr(args[0].Val), checkParenExpr(args[1].Val)
		return &internal.Elem{Val: t, Type: tyRet, CVal: cval}, nil
	case *ast.UnaryExpr:
		t.X = args[0].Val
		return &internal.Elem{Val: t, Type: tyRet, CVal: cval}, nil
	}
	var valArgs []ast.Expr
	var recv = getParam1st(sig)
	if n := len(args); n > recv { // for method, args[0] is already in fn.Val
		valArgs = make([]ast.Expr, n-recv)
		for i := recv; i < n; i++ {
			valArgs[i-recv] = args[i].Val
		}
	}
	return &internal.Elem{
		Type: tyRet, CVal: cval,
		Val: &ast.CallExpr{Fun: fn.Val, Args: valArgs, Ellipsis: flags & InstrFlagEllipsis},
	}, nil
}

func isPointer(typ types.Type) bool {
	_, ok := typ.(*types.Pointer)
	return ok
}

func checkParenExpr(x ast.Expr) ast.Expr {
	if _, ok := x.(*ast.CompositeLit); ok {
		return &ast.ParenExpr{X: x}
	}
	return x
}

func backupArgs(args []*internal.Elem) []ast.Expr {
	backup := make([]ast.Expr, len(args))
	for i, arg := range args {
		backup[i] = arg.Val
	}
	return backup
}

func restoreArgs(args []*internal.Elem, backup []ast.Expr) {
	for i, arg := range args {
		arg.Val = backup[i]
	}
}

func untypeBig(pkg *Package, cval constant.Value, tyRet types.Type) (*internal.Elem, bool) {
	switch tyRet {
	case pkg.utBigInt:
		var val *big.Int
		switch v := constant.Val(cval).(type) {
		case int64:
			val = big.NewInt(v)
		case *big.Int:
			val = v
		case *big.Rat:
			return pkg.cb.UntypedBigRat(v).stk.Pop(), true
		default:
			panic("unexpected constant")
		}
		return pkg.cb.UntypedBigInt(val).stk.Pop(), true
	case pkg.utBigRat:
		var val *big.Rat
		switch v := constant.Val(cval).(type) {
		case int64:
			val = big.NewRat(v, 1)
		case *big.Rat:
			val = v
		case *big.Int:
			val = new(big.Rat).SetInt(v)
		default:
			panic("unexpected constant")
		}
		return pkg.cb.UntypedBigRat(val).stk.Pop(), true
	case types.Typ[types.UntypedBool], types.Typ[types.Bool]:
		return &internal.Elem{
			Val: boolean(constant.BoolVal(cval)), Type: tyRet, CVal: cval,
		}, true
	}
	return nil, false
}

func toRetType(t *types.Tuple, it *instantiated) types.Type {
	if t == nil {
		return nil
	} else if t.Len() == 1 {
		return it.normalize(t.At(0).Type())
	}
	return it.normalizeTuple(t)
}

func matchFuncType(
	pkg *Package, args []*internal.Elem, flags InstrFlags, sig *types.Signature, fn *internal.Elem) error {
	var t *types.Tuple
	n := len(args)
	if len(args) == 1 && checkTuple(&t, args[0].Type) {
		n = t.Len()
		args = make([]*internal.Elem, n)
		for i := 0; i < n; i++ {
			args[i] = &internal.Elem{Type: t.At(i).Type()}
		}
	} else if (flags&instrFlagApproxType) != 0 && n > 0 {
		if typ, ok := args[0].Type.(*types.Named); ok {
			switch t := pkg.cb.getUnderlying(typ).(type) {
			case *types.Slice, *types.Map, *types.Chan:
				args[0].Type = t
			}
		}
	}
	var at interface{}
	if fn == nil {
		at = "closure argument" // fn = nil means it is a closure
	} else {
		at = func() string {
			src, _ := pkg.cb.loadExpr(fn.Src)
			return "argument to " + src
		}
	}
	if sig.Variadic() {
		if (flags & InstrFlagEllipsis) == 0 {
			n1 := getParamLen(sig) - 1
			if n < n1 {
				return errors.New("TODO: not enough function parameters")
			}
			tyVariadic, ok := getParam(sig, n1).Type().(*types.Slice)
			if !ok {
				return errors.New("TODO: tyVariadic not a slice")
			}
			if err := matchFuncArgs(pkg, args[:n1], sig, at); err != nil {
				return err
			}
			return matchElemType(pkg, args[n1:], tyVariadic.Elem(), at)
		}
	} else if (flags & InstrFlagEllipsis) != 0 {
		var caller string
		var pos token.Position
		if fn != nil {
			caller, pos = pkg.cb.loadExpr(fn.Src)
		}
		return pkg.cb.newCodeError(&pos, fmt.Sprintf("invalid use of ... in call to %v", caller))
	}
	if nreq := getParamLen(sig); nreq != n {
		fewOrMany := "few"
		if n > nreq {
			fewOrMany = "many"
		}
		var caller string
		var pos token.Position
		if fn != nil {
			caller, pos = pkg.cb.loadExpr(fn.Src)
		}
		return pkg.cb.newCodeError(&pos, fmt.Sprintf(
			"too %s arguments in call to %s\n\thave (%v)\n\twant %v", fewOrMany, caller, getTypes(args), sig.Params()))
	}
	return matchFuncArgs(pkg, args, sig, at)
}

func matchFuncArgs(
	pkg *Package, args []*internal.Elem, sig *types.Signature, at interface{}) error {
	for i, arg := range args {
		if err := matchType(pkg, arg, getParam(sig, i).Type(), at); err != nil {
			return err
		}
	}
	return nil
}

func checkFuncResults(pkg *Package, rets []*internal.Elem, results *types.Tuple, src ast.Node) {
	n := len(rets)
	need := results.Len()
	switch n {
	case 0:
		if need > 0 && isUnnamedParams(results) {
			pos := pkg.cb.nodePosition(src)
			pkg.cb.panicCodeErrorf(
				&pos, "not enough arguments to return\n\thave ()\n\twant %v", results)
		}
		return
	case 1:
		if t, ok := rets[0].Type.(*types.Tuple); ok {
			if n1 := t.Len(); n1 != need {
				fewOrMany := "few"
				if n1 > need {
					fewOrMany = "many"
				}
				pos := pkg.cb.nodePosition(src)
				pkg.cb.panicCodeErrorf(
					&pos, "too %s arguments to return\n\thave %v\n\twant %v", fewOrMany, t, results)
			}
			for i := 0; i < need; i++ {
				arg := &internal.Elem{Type: t.At(i).Type(), Src: src}
				if err := matchType(pkg, arg, results.At(i).Type(), "return argument"); err != nil {
					panic(err)
				}
			}
			return
		}
	}
	if n == need {
		for i := 0; i < need; i++ {
			if err := matchType(pkg, rets[i], results.At(i).Type(), "return argument"); err != nil {
				panic(err)
			}
		}
		return
	}
	fewOrMany := "few"
	if n > need {
		fewOrMany = "many"
	}
	pos := pkg.cb.nodePosition(src)
	pkg.cb.panicCodeErrorf(
		&pos, "too %s arguments to return\n\thave (%v)\n\twant %v", fewOrMany, getTypes(rets), results)
}

func getTypes(rets []*internal.Elem) string {
	typs := make([]string, len(rets))
	for i, ret := range rets {
		typs[i] = ret.Type.String()
	}
	return strings.Join(typs, ", ")
}

func isUnnamedParams(t *types.Tuple) bool {
	if t == nil {
		return true
	}
	for i, n := 0, t.Len(); i < n; i++ {
		if t.At(i).Name() == "" {
			return true
		}
	}
	return false
}

func matchElemType(pkg *Package, vals []*internal.Elem, elt types.Type, at interface{}) error {
	for _, val := range vals {
		if err := matchType(pkg, val, elt, at); err != nil {
			return err
		}
	}
	return nil
}

func checkAssignType(pkg *Package, varRef types.Type, val *internal.Elem) {
	if rt, ok := varRef.(*refType); ok {
		if err := matchType(pkg, val, rt.typ, "assignment"); err != nil {
			panic(err)
		}
	} else if varRef == nil { // underscore
		// do nothing
	} else {
		panic("TODO: unassignable")
	}
}

func checkAssign(pkg *Package, ref *internal.Elem, val types.Type, at string) {
	if rt, ok := ref.Type.(*refType); ok {
		elem := &internal.Elem{Type: val}
		if err := matchType(pkg, elem, rt.typ, at); err != nil {
			src, pos := pkg.cb.loadExpr(ref.Src)
			pkg.cb.panicCodeErrorf(
				&pos, "cannot assign type %v to %s (type %v) in %s", val, src, rt.typ, at)
		}
	} else if ref.Type == nil { // underscore
		// do nothing
	} else {
		panic("TODO: unassignable")
	}
}

type MatchError struct {
	Src   ast.Node
	Arg   types.Type
	Param types.Type
	At    interface{}
	cb    *CodeBuilder
	fstmt bool
}

func strval(at interface{}) string {
	switch v := at.(type) {
	case string:
		return v
	case func() string:
		return v()
	default:
		panic("strval unexpected: unknown type")
	}
}

func (p *MatchError) Error() string {
	if p.fstmt {
		pos := p.cb.nodePosition(p.Src)
		return fmt.Sprintf(
			"%v: cannot use %v value as type %v in %s", pos, p.Arg, p.Param, strval(p.At))
	}
	src, pos := p.cb.loadExpr(p.Src)
	return fmt.Sprintf(
		"%v: cannot use %s (type %v) as type %v in %s", pos, src, p.Arg, p.Param, strval(p.At))
}

// TODO: use matchType to all assignable check
func matchType(pkg *Package, arg *internal.Elem, param types.Type, at interface{}) error {
	if debugMatch {
		log.Printf("==> MatchType %v, %v\n", arg.Type, param)
	}
	switch t := param.(type) {
	case *unboundType: // variable to bound type
		if t2, ok := arg.Type.(*unboundType); ok {
			if t2.tBound == nil {
				if t == t2 {
					return nil
				}
				return fmt.Errorf("TODO: can't match two unboundTypes")
			}
			arg.Type = t2.tBound
		}
		if t.tBound == nil {
			arg.Type = DefaultConv(pkg, arg.Type, &arg.Val)
			t.boundTo(pkg, arg.Type)
		}
		param = t.tBound
	case *unboundMapElemType:
		if t2, ok := arg.Type.(*unboundType); ok {
			if t2.tBound == nil {
				panic("TODO: don't pass unbound variables")
			}
			arg.Type = t2.tBound
		}
		arg.Type = DefaultConv(pkg, arg.Type, &arg.Val)
		mapTy := types.NewMap(Default(pkg, t.key), arg.Type)
		t.typ.boundTo(pkg, mapTy)
		return nil
	default:
		if isUnboundParam(param) {
			if t, ok := arg.Type.(*unboundType); ok {
				if t.tBound == nil {
					// panic("TODO: don't pass unbound variables as template function params.")
					return nil
				}
				arg.Type = t.tBound
			}
			return boundType(pkg, arg.Type, param, arg)
		}
	}
	if AssignableConv(pkg, arg.Type, param, arg) {
		return nil
	}
	return &MatchError{
		Src: arg.Src, Arg: arg.Type, Param: param, At: at, cb: &pkg.cb, fstmt: arg.Val == nil}
}

// -----------------------------------------------------------------------------

func boundElementType(pkg *Package, elts []*internal.Elem, base, max, step int) types.Type {
	var tBound types.Type
	for i := base; i < max; i += step {
		e := elts[i]
		if tBound == e.Type {
			// nothing to do
		} else if tBound == nil || AssignableTo(pkg, tBound, e.Type) {
			tBound = e.Type
		} else if !AssignableTo(pkg, e.Type, tBound) {
			return TyEmptyInterface
		}
	}
	return tBound
}

// -----------------------------------------------------------------------------
