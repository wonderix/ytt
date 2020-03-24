package workspace

import (
	"fmt"
	"strings"

	"github.com/k14s/ytt/pkg/filepos"
	"github.com/k14s/ytt/pkg/files"
	"github.com/k14s/ytt/pkg/template"
	"github.com/k14s/ytt/pkg/template/core"
	"github.com/k14s/ytt/pkg/yamlmeta"
	"github.com/k14s/ytt/pkg/yamltemplate"
	"github.com/k14s/ytt/pkg/yttlibrary"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

type LibraryModule struct {
	libraryCtx              LibraryExecutionContext
	libraryExecutionFactory *LibraryExecutionFactory
	libraryValues           []*yamlmeta.Document
}

func NewLibraryModule(libraryCtx LibraryExecutionContext,
	libraryExecutionFactory *LibraryExecutionFactory,
	libraryValues []*yamlmeta.Document) LibraryModule {

	return LibraryModule{libraryCtx, libraryExecutionFactory, libraryValues}
}

func (b LibraryModule) AsModule() starlark.StringDict {
	return starlark.StringDict{
		"library": &starlarkstruct.Module{
			Name: "library",
			Members: starlark.StringDict{
				"get": starlark.NewBuiltin("library.get", core.ErrWrapper(b.Get)),
			},
		},
	}
}

func (l LibraryModule) Get(thread *starlark.Thread, f *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {

	if args.Len() != 1 {
		return starlark.None, fmt.Errorf("expected exactly one argument")
	}

	libPath, err := core.NewStarlarkValue(args.Index(0)).AsString()
	if err != nil {
		return starlark.None, err
	}

	if strings.HasPrefix(libPath, "@") {
		return starlark.None, fmt.Errorf(
			"Expected library '%s' to be specified without '@'", libPath)
	}

	foundLib, err := l.libraryCtx.Current.FindAccessibleLibrary(libPath)
	if err != nil {
		return starlark.None, err
	}

	libraryCtx := LibraryExecutionContext{Current: foundLib, Root: foundLib}

	values, childLibValues := GetValuesForLibraryAndChildren(l.libraryValues, libraryCtx.Current)
	if err != nil {
		return starlark.None, err
	}

	return (&libraryValue{libPath, libraryCtx, values, childLibValues, l.libraryExecutionFactory}).AsStarlarkValue(), nil
}

type libraryValue struct {
	desc                    string // used in error messages
	libraryCtx              LibraryExecutionContext
	dataValuess             []*yamlmeta.Document
	libraryDataValues       []*yamlmeta.Document
	libraryExecutionFactory *LibraryExecutionFactory
}

func (l *libraryValue) AsStarlarkValue() starlark.Value {
	evalErrMsg := fmt.Sprintf("Evaluating library '%s'", l.desc)
	exportErrMsg := fmt.Sprintf("Exporting from library '%s'", l.desc)

	// TODO technically not a module; switch to struct?
	return &starlarkstruct.Module{
		Name: "library",
		Members: starlark.StringDict{
			"with_data_values": starlark.NewBuiltin("library.with_data_values", core.ErrWrapper(l.WithDataValues)),
			"eval":             starlark.NewBuiltin("library.eval", core.ErrWrapper(core.ErrDescWrapper(evalErrMsg, l.Eval))),
			"export":           starlark.NewBuiltin("library.export", core.ErrWrapper(core.ErrDescWrapper(exportErrMsg, l.Export))),
		},
	}
}

func (l *libraryValue) WithDataValues(thread *starlark.Thread, f *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {

	if args.Len() != 1 {
		return starlark.None, fmt.Errorf("expected exactly one argument")
	}

	dataValues := core.NewStarlarkValue(args.Index(0)).AsGoValue()

	libVal := &libraryValue{l.desc, l.libraryCtx, l.dataValuess, l.libraryDataValues, l.libraryExecutionFactory}
	libVal.dataValuess = append([]*yamlmeta.Document{}, l.dataValuess...)
	libVal.dataValuess = append(libVal.dataValuess, &yamlmeta.Document{
		Value:    yamlmeta.NewASTFromInterface(dataValues),
		Position: filepos.NewUnknownPosition(),
	})

	return libVal.AsStarlarkValue(), nil
}

func (l *libraryValue) Eval(thread *starlark.Thread, f *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {

	if args.Len() != 0 {
		return starlark.None, fmt.Errorf("expected no arguments")
	}

	libraryLoader := l.libraryExecutionFactory.New(l.libraryCtx)

	astValues, libValues, err := libraryLoader.Values(l.dataValuess)
	if err != nil {
		return starlark.None, err
	}

	libraryAttachedValues := append([]*yamlmeta.Document{}, libValues...)
	libraryAttachedValues = append(libValues, l.libraryDataValues...)

	result, err := libraryLoader.Eval(astValues, libraryAttachedValues)
	if err != nil {
		return starlark.None, err
	}

	return yamltemplate.NewStarlarkFragment(result.DocSet), nil
}

func (l *libraryValue) Export(thread *starlark.Thread, f *starlark.Builtin,
	args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {

	symbolName, locationPath, err := l.exportArgs(args, kwargs)
	if err != nil {
		return starlark.None, err
	}

	if strings.HasPrefix(symbolName, "_") {
		return starlark.None, fmt.Errorf(
			"Symbols starting with '_' are private, and cannot be exported")
	}

	libraryLoader := l.libraryExecutionFactory.New(l.libraryCtx)

	astValues, libValues, err := libraryLoader.Values(l.dataValuess)
	if err != nil {
		return starlark.None, err
	}

	libraryAttachedValues := append([]*yamlmeta.Document{}, libValues...)
	libraryAttachedValues = append(libValues, l.libraryDataValues...)

	result, err := libraryLoader.Eval(astValues, libraryAttachedValues)
	if err != nil {
		return starlark.None, err
	}

	foundExports := []EvalExport{}

	for _, exp := range result.Exports {
		if _, found := exp.Symbols[symbolName]; found {
			if len(locationPath) == 0 || locationPath == exp.Path {
				foundExports = append(foundExports, exp)
			}
		}
	}

	switch len(foundExports) {
	case 0:
		return starlark.None, fmt.Errorf(
			"Expected to find exported symbol '%s', but did not", symbolName)

	case 1:
		return foundExports[0].Symbols[symbolName], nil

	default:
		var paths []string
		for _, exp := range foundExports {
			paths = append(paths, exp.Path)
		}

		return starlark.None, fmt.Errorf("Expected to find exactly "+
			"one exported symbol '%s', but found multiple across files: %s",
			symbolName, strings.Join(paths, ", "))
	}
}

func (l *libraryValue) exportArgs(args starlark.Tuple, kwargs []starlark.Tuple) (string, string, error) {
	if args.Len() != 1 {
		return "", "", fmt.Errorf("expected exactly one argument")
	}

	symbolName, err := core.NewStarlarkValue(args.Index(0)).AsString()
	if err != nil {
		return "", "", err
	}

	var locationPath string

	for _, kwarg := range kwargs {
		kwargName := string(kwarg[0].(starlark.String))

		switch kwargName {
		case "path":
			var err error
			locationPath, err = core.NewStarlarkValue(kwarg[1]).AsString()
			if err != nil {
				return "", "", err
			}

		default:
			return "", "", fmt.Errorf("Unexpected keyword argument '%s'", kwargName)
		}
	}

	return symbolName, locationPath, nil
}

const (
	ForChildLib int = iota
	ForCurrentLib
	ForOtherLib
)

func GetValuesForLibraryAndChildren(valueDocs []*yamlmeta.Document,
	currentLib *Library) ([]*yamlmeta.Document, []*yamlmeta.Document) {

	var currentLibValues []*yamlmeta.Document
	var childLibValues []*yamlmeta.Document

	for _, doc := range valueDocs {
		kwargs := template.NewAnnotations(doc).Kwargs(yttlibrary.AnnotationDataValues)
		for _, kwarg := range kwargs {
			if kwargName := string(kwarg[0].(starlark.String)); kwargName == "library" {
				switch forLib := getValuesDocLibrary(string(kwarg[1].(starlark.String)), currentLib); forLib {
				case ForChildLib:
					childLibValues = append(childLibValues, doc)
				case ForCurrentLib:
					currentLibValues = append(currentLibValues, doc)
				default:
				}
			}
		}
	}

	return currentLibValues, childLibValues
}

func getValuesDocLibrary(libArg string, currentLib *Library) int {
	argLibraries := strings.Split(libArg, "@")
	for idx, libraryPath := range argLibraries {
		_, libraryName := files.SplitPath(libraryPath)
		if libraryName == currentLib.name && idx == (len(argLibraries)-1) {
			return ForCurrentLib
		} else if foundLib, _ := currentLib.FindAccessibleLibrary(libraryPath); foundLib != nil {
			return ForChildLib
		}
	}
	return ForOtherLib
}
