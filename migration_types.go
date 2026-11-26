package bubblelint

import (
	"go/token"
	"go/types"

	"golang.org/x/tools/go/types/typeutil"
)

// Facts carry type descriptions, while assignability and method lookup use the
// importing pass's Go type objects. An unavailable type stays unknown.
type migrationType struct {
	Kind     byte                  `json:",omitempty"`
	Basic    types.BasicKind       `json:",omitempty"`
	Package  string                `json:",omitempty"`
	Name     string                `json:",omitempty"`
	Elem     *migrationType        `json:",omitempty"`
	Args     []*migrationType      `json:",omitempty"`
	Params   []*migrationType      `json:",omitempty"`
	Results  []*migrationType      `json:",omitempty"`
	Variadic bool                  `json:",omitempty"`
	Methods  []migrationTypeMethod `json:",omitempty"`
}

type migrationTypeMethod struct {
	Package   string
	Name      string
	Signature *migrationType
}

func describeMigrationType(typ types.Type, left *int) *migrationType {
	*left--
	if typ == nil || *left < 0 {
		return nil
	}
	typ = types.Unalias(typ)
	if _, parameter := typ.(*types.TypeParam); parameter {
		return nil
	}
	// Interfaces are structural, including unexported interface declarations
	// that need not be present in an imported package's export data.
	if iface, ok := typ.Underlying().(*types.Interface); ok {
		if !iface.IsMethodSet() {
			return nil
		}
		result := &migrationType{Kind: 'i'}
		for i := 0; i < iface.NumMethods(); i++ {
			method := iface.Method(i)
			signature := describeMigrationType(method.Type(), left)
			if signature == nil {
				return nil
			}
			pkg := ""
			if method.Pkg() != nil {
				pkg = method.Pkg().Path()
			}
			result.Methods = append(result.Methods, migrationTypeMethod{pkg, method.Name(), signature})
		}
		return result
	}
	switch typ := typ.(type) {
	case *types.Basic:
		if typ.Info()&types.IsUntyped == 0 {
			return &migrationType{Kind: 'b', Basic: typ.Kind()}
		}
	case *types.Named:
		if typ.Obj().Pkg() == nil || typ.Obj().Parent() != typ.Obj().Pkg().Scope() || typ.TypeParams().Len() != typ.TypeArgs().Len() {
			return nil
		}
		result := &migrationType{Kind: 'n', Package: typ.Obj().Pkg().Path(), Name: typ.Obj().Name()}
		for i := 0; i < typ.TypeArgs().Len(); i++ {
			arg := describeMigrationType(typ.TypeArgs().At(i), left)
			if arg == nil {
				return nil
			}
			result.Args = append(result.Args, arg)
		}
		return result
	case *types.Pointer:
		if elem := describeMigrationType(typ.Elem(), left); elem != nil {
			return &migrationType{Kind: 'p', Elem: elem}
		}
	case *types.Slice:
		if elem := describeMigrationType(typ.Elem(), left); elem != nil {
			return &migrationType{Kind: 's', Elem: elem}
		}
	case *types.Signature:
		if typ.TypeParams().Len() != 0 || typ.RecvTypeParams().Len() != 0 {
			return nil
		}
		result := &migrationType{Kind: 'f', Variadic: typ.Variadic()}
		for _, tuple := range []struct {
			from *types.Tuple
			to   *[]*migrationType
		}{{typ.Params(), &result.Params}, {typ.Results(), &result.Results}} {
			for i := 0; i < tuple.from.Len(); i++ {
				item := describeMigrationType(tuple.from.At(i).Type(), left)
				if item == nil {
					return nil
				}
				*tuple.to = append(*tuple.to, item)
			}
		}
		return result
	}
	return nil
}

type migrationTypeResolver struct {
	root     *types.Package
	packages map[string]*types.Package
	decoded  map[*migrationType]types.Type
	methods  typeutil.MethodSetCache
}

func (r *migrationTypeResolver) pkg(path string) *types.Package {
	if r.packages == nil {
		r.packages = make(map[string]*types.Package)
		var visit func(*types.Package)
		visit = func(pkg *types.Package) {
			if pkg == nil || r.packages[pkg.Path()] != nil {
				return
			}
			r.packages[pkg.Path()] = pkg
			for _, dependency := range pkg.Imports() {
				visit(dependency)
			}
		}
		visit(r.root)
	}
	return r.packages[path]
}

func (r *migrationTypeResolver) resolve(desc *migrationType) (result types.Type) {
	if desc == nil {
		return nil
	}
	if result, known := r.decoded[desc]; known {
		return result
	}
	if r.decoded == nil {
		r.decoded = make(map[*migrationType]types.Type)
	}
	r.decoded[desc] = nil
	defer func() { r.decoded[desc] = result }()
	switch desc.Kind {
	case 'b':
		if desc.Basic > types.Invalid && desc.Basic <= types.UnsafePointer {
			return types.Typ[desc.Basic]
		}
	case 'n':
		pkg := r.pkg(desc.Package)
		if pkg == nil {
			return nil
		}
		obj, ok := pkg.Scope().Lookup(desc.Name).(*types.TypeName)
		if !ok {
			return nil
		}
		result = types.Unalias(obj.Type())
		if len(desc.Args) == 0 {
			return result
		}
		var args []types.Type
		for _, arg := range desc.Args {
			typ := r.resolve(arg)
			if typ == nil {
				return nil
			}
			args = append(args, typ)
		}
		result, _ = types.Instantiate(nil, result, args, true)
		return result
	case 'p', 's':
		if elem := r.resolve(desc.Elem); elem != nil {
			if desc.Kind == 'p' {
				return types.NewPointer(elem)
			}
			return types.NewSlice(elem)
		}
	case 'f':
		params, paramsOK := r.tuple(desc.Params)
		results, resultsOK := r.tuple(desc.Results)
		if paramsOK && resultsOK {
			return types.NewSignatureType(nil, nil, nil, params, results, desc.Variadic)
		}
	case 'i':
		var methods []*types.Func
		for _, method := range desc.Methods {
			signature, ok := r.resolve(method.Signature).(*types.Signature)
			pkg := r.pkg(method.Package)
			if !ok || method.Package != "" && pkg == nil {
				return nil
			}
			methods = append(methods, types.NewFunc(token.NoPos, pkg, method.Name, signature))
		}
		return types.NewInterfaceType(methods, nil).Complete()
	}
	return nil
}

func (r *migrationTypeResolver) tuple(items []*migrationType) (*types.Tuple, bool) {
	var variables []*types.Var
	for _, item := range items {
		typ := r.resolve(item)
		if typ == nil {
			return nil, false
		}
		variables = append(variables, types.NewVar(token.NoPos, nil, "", typ))
	}
	return types.NewTuple(variables...), true
}
