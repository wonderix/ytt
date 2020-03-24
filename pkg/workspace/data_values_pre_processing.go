package workspace

import (
	"fmt"
	"strings"

	"github.com/k14s/ytt/pkg/filepos"
	"github.com/k14s/ytt/pkg/yamlmeta"
	"github.com/k14s/ytt/pkg/yamltemplate"
	"github.com/k14s/ytt/pkg/yttlibrary"
	yttoverlay "github.com/k14s/ytt/pkg/yttlibrary/overlay"
	"go.starlark.net/starlark"
)

type DataValuesPreProcessing struct {
	valuesFiles           []*FileInLibrary
	valuesOverlays        []*yamlmeta.Document
	loader                *TemplateLoader
	IgnoreUnknownComments bool // TODO remove?
}

func (o DataValuesPreProcessing) Apply() (*yamlmeta.Document, []*yamlmeta.Document, error) {
	files := append([]*FileInLibrary{}, o.valuesFiles...)

	// Respect assigned file order for data values overlaying to succeed
	SortFilesInLibrary(files)

	dataDocs, libraryDataDocs, err := o.apply(files)
	if err != nil {
		errMsg := "Overlaying data values (in following order: %s): %s"
		return nil, nil, fmt.Errorf(errMsg, o.allFileDescs(files), err)
	}

	return dataDocs, libraryDataDocs, nil
}

func (o DataValuesPreProcessing) apply(files []*FileInLibrary) (*yamlmeta.Document, []*yamlmeta.Document, error) {
	var values *yamlmeta.Document
	var libraryValues []*yamlmeta.Document
	for _, fileInLib := range files {
		valuesDocs, libraryValuesDocs, err := o.templateFile(fileInLib)
		if err != nil {
			return nil, nil, fmt.Errorf("Templating file '%s': %s", fileInLib.File.RelativePath(), err)
		}
		libraryValues = append(libraryValues, libraryValuesDocs...)

		for _, valuesDoc := range valuesDocs {
			if values == nil {
				values = valuesDoc
				continue
			}

			var err error
			values, err = o.overlay(values, valuesDoc)
			if err != nil {
				return nil, nil, err
			}
		}
	}

	values, err := o.overlayValuesOverlays(values)
	if err != nil {
		return nil, nil, err
	}

	return values, libraryValues, nil
}

func (p DataValuesPreProcessing) allFileDescs(files []*FileInLibrary) string {
	var result []string
	for _, fileInLib := range files {
		result = append(result, fileInLib.File.RelativePath())
	}
	if len(p.valuesOverlays) > 0 {
		result = append(result, "additional data values")
	}
	return strings.Join(result, ", ")
}

func (p DataValuesPreProcessing) templateFile(fileInLib *FileInLibrary) ([]*yamlmeta.Document, []*yamlmeta.Document, error) {
	libraryCtx := LibraryExecutionContext{Current: fileInLib.Library, Root: NewRootLibrary(nil)}

	_, resultDocSet, err := p.loader.EvalYAML(libraryCtx, fileInLib.File)
	if err != nil {
		return nil, nil, err
	}

	tplOpts := yamltemplate.MetasOpts{IgnoreUnknown: p.IgnoreUnknownComments}

	// Extract _all_ data values docs from the templated result
	valuesDocs, libraryValuesDocs, nonValuesDocs, err := yttlibrary.DataValues{resultDocSet, tplOpts}.Extract()
	if err != nil {
		return nil, nil, err
	}

	// Fail if there any non-empty docs that are not data values
	if len(nonValuesDocs) > 0 {
		for _, doc := range nonValuesDocs {
			if !doc.IsEmpty() {
				errStr := "Expected data values file '%s' to only have data values documents"
				return nil, nil, fmt.Errorf(errStr, fileInLib.File.RelativePath())
			}
		}
	}

	return valuesDocs, libraryValuesDocs, nil
}

func (p DataValuesPreProcessing) overlay(valuesDoc, newValuesDoc *yamlmeta.Document) (*yamlmeta.Document, error) {
	op := yttoverlay.OverlayOp{
		Left:   &yamlmeta.DocumentSet{Items: []*yamlmeta.Document{valuesDoc}},
		Right:  &yamlmeta.DocumentSet{Items: []*yamlmeta.Document{newValuesDoc}},
		Thread: &starlark.Thread{Name: "data-values-pre-processing"},

		ExactMatch: true,
	}

	newLeft, err := op.Apply()
	if err != nil {
		return nil, err
	}

	return newLeft.(*yamlmeta.DocumentSet).Items[0], nil
}

func (p DataValuesPreProcessing) overlayValuesOverlays(valuesDoc *yamlmeta.Document) (*yamlmeta.Document, error) {
	if valuesDoc == nil {
		// TODO get rid of assumption that data values is a map?
		valuesDoc = &yamlmeta.Document{
			Value:    &yamlmeta.Map{},
			Position: filepos.NewUnknownPosition(),
		}
	}

	var result *yamlmeta.Document

	// by default return itself
	result = valuesDoc

	for _, valuesOverlay := range p.valuesOverlays {
		var err error

		result, err = p.overlay(result, valuesOverlay)
		if err != nil {
			// TODO improve error message?
			return nil, fmt.Errorf("Overlaying additional data values on top of "+
				"data values from files (marked as @data/values): %s", err)
		}
	}

	return result, nil
}
