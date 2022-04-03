package gox

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"

	"github.com/goplus/gox/internal"
)

// ----------------------------------------------------------------------------

type VFields interface { // virtual fields
	FindField(cb *CodeBuilder, t *types.Named, name string, arg *Element, src ast.Node) MemberKind
	FieldRef(cb *CodeBuilder, t *types.Named, name string, src ast.Node) MemberKind
}

type vFieldsMgr struct {
	vfts map[*types.Named]VFields
}

func (p *CodeBuilder) refVField(t *types.Named, name string, src ast.Node) MemberKind {
	if vft, ok := p.vfts[t]; ok {
		return vft.FieldRef(p, t, name, src)
	}
	return MemberInvalid
}

func (p *CodeBuilder) findVField(t *types.Named, name string, arg *Element, src ast.Node) MemberKind {
	if vft, ok := p.vfts[t]; ok {
		return vft.FindField(p, t, name, arg, src)
	}
	return MemberInvalid
}

func (p *Package) SetVFields(t *types.Named, vft VFields) {
	if p.cb.vfts == nil {
		p.cb.vfts = make(map[*types.Named]VFields)
	}
	p.cb.vfts[t] = vft
}

// ----------------------------------------------------------------------------

type BitField struct {
	Name    string // bit field name
	FldName string // real field name
	Off     int
	Bits    int
	Pos     token.Pos
}

type BitFields struct {
	flds []*BitField
}

func NewBitFields(flds []*BitField) *BitFields {
	return &BitFields{flds: flds}
}

func (p *BitFields) FindField(
	cb *CodeBuilder, t *types.Named, name string, arg *Element, src ast.Node) MemberKind {
	for _, v := range p.flds {
		if v.Name == name {
			o := t.Underlying().(*types.Struct)
			if kind := cb.field(o, v.FldName, "", MemberFlagVal, arg, src); kind != MemberInvalid {
				tfld := cb.stk.Get(-1).Type.(*types.Basic)
				if (tfld.Info() & types.IsUnsigned) != 0 {
					if v.Off != 0 {
						cb.Val(v.Off).BinaryOp(token.SHR)
					}
					cb.Val((1 << v.Bits) - 1).BinaryOp(token.AND)
				} else {
					bits := int(std.Sizeof(tfld)<<3) - v.Bits
					cb.Val(bits - v.Off).BinaryOp(token.SHL).Val(bits).BinaryOp(token.SHR)
				}
				return kind
			}
			break
		}
	}
	return MemberInvalid
}

func (p *bfRefType) assign(cb *CodeBuilder, lhs, rhs *ast.Expr) {
	// *addr = *addr &^ ((1 << bits) - 1) << off) | (rhs << off)
	tname := cb.pkg.autoName()
	tvar := ident(tname)
	addr := &ast.UnaryExpr{Op: token.AND, X: *lhs}
	stmt := &ast.AssignStmt{Lhs: []ast.Expr{tvar}, Tok: token.DEFINE, Rhs: []ast.Expr{addr}}
	cb.emitStmt(stmt)
	mask := ((1 << p.bits) - 1) << p.off
	maskLit := &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(mask)}
	valMask := &ast.BinaryExpr{X: &ast.StarExpr{X: tvar}, Op: token.AND_NOT, Y: maskLit}
	if p.off != 0 {
		offLit := &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(p.off)}
		*rhs = &ast.BinaryExpr{X: *rhs, Op: token.SHL, Y: offLit}
	}
	*lhs = &ast.StarExpr{X: tvar}
	*rhs = &ast.BinaryExpr{X: valMask, Op: token.OR, Y: *rhs}
}

func (p *BitFields) FieldRef(cb *CodeBuilder, t *types.Named, name string, src ast.Node) MemberKind {
	for _, v := range p.flds {
		if v.Name == name {
			stk := cb.stk
			o := t.Underlying().(*types.Struct)
			if cb.fieldRef(stk.Get(-1).Val, o, v.FldName) {
				fld := stk.Get(-1)
				tfld := fld.Type.(*refType).typ.(*types.Basic)
				stk.Ret(1, &internal.Elem{
					Val: fld.Val, Src: src,
					Type: &bfRefType{typ: tfld, bits: v.Bits, off: v.Off},
				})
				return MemberField
			}
			break
		}
	}
	return MemberInvalid
}

// ----------------------------------------------------------------------------

type UnionField struct {
	Name string
	Off  int
	Type types.Type
	Pos  token.Pos
}

type UnionFields struct {
	flds []*UnionField
}

func NewUnionFields(flds []*UnionField) *UnionFields {
	return &UnionFields{flds: flds}
}

func (p *UnionFields) getField(
	cb *CodeBuilder, tfld *types.Named, name string, src ast.Node, ref bool) MemberKind {
	for _, v := range p.flds {
		if v.Name == name {
			obj := cb.stk.Pop()
			tobj, isPtr := obj.Type, false
			if tt, ok := tobj.(*types.Pointer); ok {
				tobj, isPtr = tt.Elem(), true
			}
			cb.Typ(types.NewPointer(v.Type)).Typ(types.Typ[types.UnsafePointer])
			if v.Off != 0 {
				cb.Typ(types.Typ[types.Uintptr]).Typ(types.Typ[types.UnsafePointer])
			}
			cb.stk.Push(obj)
			if tt, ok := tobj.(*types.Named); ok && tt == tfld { // it's an union type
				if !isPtr {
					cb.UnaryOp(token.AND)
				}
			} else { // it's a type contains a field with union type
				cb.MemberRef(tfld.Obj().Name()).UnaryOp(token.AND)
			}
			if v.Off != 0 {
				cb.Call(1).Call(1).Val(v.Off).BinaryOp(token.ADD) // => voidptr => uintptr
			}
			cb.Call(1).Call(1) // => voidptr => *type
			if ref {
				cb.ElemRef()
			} else {
				cb.Elem()
			}
			return MemberField
		}
	}
	return MemberInvalid
}

func (p *UnionFields) FindField(
	cb *CodeBuilder, tfld *types.Named, name string, arg *Element, src ast.Node) MemberKind {
	return p.getField(cb, tfld, name, src, false)
}

func (p *UnionFields) FieldRef(cb *CodeBuilder, tfld *types.Named, name string, src ast.Node) MemberKind {
	return p.getField(cb, tfld, name, src, true)
}

// ----------------------------------------------------------------------------